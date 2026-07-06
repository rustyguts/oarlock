package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/rustyguts/oarlock/engine/internal/api"
	"github.com/rustyguts/oarlock/engine/internal/db"
	"github.com/rustyguts/oarlock/engine/internal/engine"
	"github.com/rustyguts/oarlock/engine/internal/steps"
	"github.com/rustyguts/oarlock/engine/internal/vault"
)

const version = "0.1.0"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbURL := envOr("DATABASE_URL", "postgres://oarlock:oarlock@localhost:5432/oarlock")
	valkeyAddr := envOr("VALKEY_ADDR", "localhost:6379")
	addr := envOr("LISTEN_ADDR", ":9000")

	pool, err := connectDB(ctx, dbURL)
	if err != nil {
		log.Error("postgres connect failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	log.Info("connected to postgres")

	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("migrations failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied")

	// Valkey is never load-bearing for correctness (Postgres is truth); it only
	// speeds up live SSE updates. Keep the client on a failed ping — go-redis
	// reconnects when it returns, and /healthz reports the degraded state.
	cache := redis.NewClient(&redis.Options{Addr: valkeyAddr})
	if err := cache.Ping(ctx).Err(); err != nil {
		log.Warn("valkey unavailable at boot; continuing (live updates poll until it returns)", "error", err)
	} else {
		log.Info("connected to valkey")
	}
	defer cache.Close()

	// Secrets vault: BYOK connections + MCP server auth (hard rule 6).
	v, err := vault.New(pool, os.Getenv("OARLOCK_MASTER_KEY"), log)
	if err != nil {
		log.Error("vault init failed", "error", err)
		os.Exit(1)
	}

	// Engine: River migrations + control/tasks queues + workers, in-process
	// with the API for now (split into a dedicated worker binary later).
	eng, err := engine.New(ctx, pool, steps.Default(&steps.Services{Secrets: v, MCP: v}), cache, v, log)
	if err != nil {
		log.Error("engine init failed", "error", err)
		os.Exit(1)
	}
	if err := eng.Start(ctx); err != nil {
		log.Error("engine start failed", "error", err)
		os.Exit(1)
	}
	log.Info("river engine started", "queues", []string{engine.QueueControl, engine.QueueTasks})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"name": "oarlock", "version": version})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		hctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		checks := map[string]string{"postgres": "ok", "valkey": "ok"}
		status := http.StatusOK
		if err := pool.Ping(hctx); err != nil {
			checks["postgres"] = err.Error()
			status = http.StatusServiceUnavailable
		}
		if err := cache.Ping(hctx).Err(); err != nil {
			checks["valkey"] = err.Error()
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{
			"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK],
			"checks": checks,
		})
	})

	srv := &api.Server{DB: pool, Engine: eng, Cache: cache, Vault: v, Log: log}
	v1 := http.NewServeMux()
	srv.Routes(v1)
	// Body cap + session auth (auto-login bootstrap) on all API routes.
	mux.Handle("/v1/", api.MaxBody(srv.WithAuth(v1)))
	// Webhook ingress is UNAUTHENTICATED (external callers): body cap + CORS but
	// no session auth. The workspace is resolved from the {ws} slug in the path.
	mux.Handle("POST /hooks/{ws}/{path}", api.MaxBody(srv.WebhookHandler()))
	// Callback resume is UNAUTHENTICATED (external approvers): body cap + CORS but
	// no session auth. The unguessable resume token is the credential.
	mux.Handle("POST /resume/{token}", api.MaxBody(srv.ResumeHandler()))
	// Workspace MCP endpoint: body cap + CORS, but no session auth — the bearer
	// API token is the workspace credential (see api.MCPHandler). One stable URL
	// serves every workspace; the token scopes the request.
	mcpHandler := api.MaxBody(srv.MCPHandler())
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	// Credentialed CORS is restricted to an explicit origin allowlist.
	allowedOrigins := strings.Split(envOr("OARLOCK_ALLOWED_ORIGINS", "http://localhost:3001"), ",")
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: api.CORS(allowedOrigins, mux),
		// No WriteTimeout: it would kill long-lived SSE streams. Header and
		// idle timeouts bound slow-loris and abandoned keep-alives.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("api listening", "addr", addr, "version", version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown error", "error", err)
	}
	if err := eng.Stop(shutdownCtx); err != nil {
		log.Error("engine shutdown error", "error", err)
	}
}

func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < 10; i++ {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
