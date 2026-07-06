package api

// DB-backed harness for the MCP endpoint and API tokens — the product's launch
// hook, so it earns a real test. The MCP server is driven end-to-end through
// the official SDK client (internal/mcpclient) over a live httptest streamable
// HTTP endpoint, against a throwaway Postgres database with the engine actually
// running (echo executor) so run_workflow observes a terminal run.
//
// If Postgres is unreachable the whole suite t.Skips, keeping an offline
// `go test ./...` green. Point DATABASE_URL_TEST at another instance to
// override the default.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustyguts/oarlock/engine/internal/db"
	"github.com/rustyguts/oarlock/engine/internal/engine"
	"github.com/rustyguts/oarlock/engine/internal/mcpclient"
	"github.com/rustyguts/oarlock/engine/internal/steps"
	"github.com/rustyguts/oarlock/engine/internal/vault"
)

const defaultTestDBURL = "postgres://oarlock:oarlock@localhost:5432/oarlock_test_mcpapi"

var (
	testPoolOnce sync.Once
	testPool     *pgxpool.Pool
	testPoolErr  error
)

func apiTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testPoolOnce.Do(func() {
		ctx := context.Background()
		dbURL := os.Getenv("DATABASE_URL_TEST")
		if dbURL == "" {
			dbURL = defaultTestDBURL
		}
		if testPoolErr = ensureTestDB(ctx, dbURL); testPoolErr != nil {
			return
		}
		testPool, testPoolErr = pgxpool.New(ctx, dbURL)
		if testPoolErr != nil {
			return
		}
		if testPoolErr = testPool.Ping(ctx); testPoolErr != nil {
			return
		}
		testPoolErr = db.Migrate(ctx, testPool)
	})
	if testPoolErr != nil {
		t.Skipf("test database unavailable (%v); skipping DB-backed api tests", testPoolErr)
	}
	return testPool
}

// ensureTestDB creates the target database if absent (mirrors the engine's
// dbtest harness). Identifiers can't be parameterized; the name only ever comes
// from our own default/env.
func ensureTestDB(ctx context.Context, dbURL string) error {
	u, err := url.Parse(dbURL)
	if err != nil {
		return err
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return fmt.Errorf("no database name in %q", dbURL)
	}
	admin := *u
	admin.Path = "/oarlock"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx,
		`select exists(select 1 from pg_database where datname=$1)`, dbName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := conn.Exec(ctx, `create database "`+dbName+`"`); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	return nil
}

// echoExecutor succeeds immediately, echoing its step key — enough for the
// run_workflow end-to-end assertion.
type echoExecutor struct{}

func (echoExecutor) Execute(_ context.Context, in steps.TaskInput) (steps.TaskOutput, error) {
	return steps.TaskOutput{Data: map[string]any{"echoed": in.StepKey}}, nil
}

type seededWS struct {
	wsID, userID, wfID uuid.UUID
	slug               string
}

func seedWorkspace(t *testing.T, pool *pgxpool.Pool, def string) seededWS {
	t.Helper()
	ctx := context.Background()
	s := seededWS{wsID: uuid.New(), userID: uuid.New(), wfID: uuid.New(), slug: "echo-wf"}
	versionID := uuid.New()
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n  sql: %s", err, sql)
		}
	}
	exec(`insert into workspaces (id, slug, name) values ($1,$2,$3)`, s.wsID, s.wsID.String(), "test ws")
	exec(`insert into users (id, email, name) values ($1,$2,$3)`, s.userID, s.userID.String()+"@test", "tester")
	exec(`insert into workspace_members (workspace_id, user_id, role) values ($1,$2,'owner')`, s.wsID, s.userID)
	exec(`insert into workflows (id, workspace_id, name, slug, is_enabled) values ($1,$2,$3,$4,true)`,
		s.wfID, s.wsID, "Echo WF", s.slug)
	exec(`insert into workflow_versions (id, workflow_id, version, definition) values ($1,$2,1,$3)`,
		versionID, s.wfID, def)
	exec(`update workflows set current_version_id=$1 where id=$2`, versionID, s.wfID)
	return s
}

// newTestServer builds an api.Server backed by a started engine (echo executor)
// and truncates prior state so each top-level test starts clean.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	pool := apiTestPool(t)
	reg := steps.NewRegistry()
	reg.Register(steps.TypeInfo{Type: "echo"}, echoExecutor{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	// engine.New runs River's migrations, so river_job exists before we truncate.
	eng, err := engine.New(ctx, pool, reg, nil, nil, log)
	if err != nil {
		cancel()
		t.Fatalf("engine.New: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`truncate workspaces, users, task_logs, river_job restart identity cascade`); err != nil {
		cancel()
		t.Fatalf("reset db: %v", err)
	}
	if err := eng.Start(ctx); err != nil {
		cancel()
		t.Fatalf("engine.Start: %v", err)
	}
	// The truncate wiped all users, so the setup-done cache must reset too.
	setupConfigured.Store(false)
	v, err := vault.New(pool, "", log) // dev key; /v1/me reports vault.dev_key
	if err != nil {
		cancel()
		t.Fatalf("vault.New: %v", err)
	}
	srv := &Server{DB: pool, Engine: eng, Vault: v, Log: log}
	cleanup := func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = eng.Stop(stopCtx)
		cancel()
	}
	return srv, cleanup
}

// fullMux mirrors main.go's wiring: session-authed /v1 routes plus the
// token-authed /mcp endpoint.
func fullMux(srv *Server) http.Handler {
	v1 := http.NewServeMux()
	srv.Routes(v1)
	mux := http.NewServeMux()
	mux.Handle("/v1/", MaxBody(srv.WithAuth(v1)))
	mcpHandler := MaxBody(srv.MCPHandler())
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	return mux
}

// --- pure unit ---

func TestNewAPIToken(t *testing.T) {
	raw, hash, prefix, err := newAPIToken()
	if err != nil {
		t.Fatalf("newAPIToken: %v", err)
	}
	if !strings.HasPrefix(raw, "oak_") {
		t.Fatalf("raw %q missing oak_ prefix", raw)
	}
	if len(raw) != len("oak_")+48 {
		t.Fatalf("raw length = %d, want %d", len(raw), len("oak_")+48)
	}
	if prefix != raw[:8] {
		t.Fatalf("prefix = %q, want %q", prefix, raw[:8])
	}
	if hash == raw {
		t.Fatal("hash must not equal the raw token")
	}
	if hash != hashToken(raw) {
		t.Fatal("hash must equal hashToken(raw) so lookups match")
	}
	raw2, _, _, _ := newAPIToken()
	if raw2 == raw {
		t.Fatal("tokens must be unique across calls")
	}
}

// --- API tokens CRUD (DB-backed, over HTTP as the setup-claimed admin) ---

func TestAPITokensCRUD(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedWorkspace(t, srv.DB, `{"steps":[{"key":"a","type":"echo"}]}`)

	ts := httptest.NewServer(fullMux(srv))
	defer ts.Close()
	// First-run setup claims the seeded owner and logs in via the cookie jar.
	client := setupAdmin(t, ts, "tokens-admin@test", "hunter2hunter2")

	// Create.
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, client, http.MethodPost, ts.URL+"/v1/api-tokens",
		map[string]any{"name": "ci"}, http.StatusCreated, &created)
	if !strings.HasPrefix(created.Token, "oak_") {
		t.Fatalf("token %q missing oak_ prefix", created.Token)
	}
	if created.ID == "" {
		t.Fatal("expected a token id")
	}

	// List shows it with a display prefix and no raw token.
	var list []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}
	doJSON(t, client, http.MethodGet, ts.URL+"/v1/api-tokens", nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].Name != "ci" || list[0].Prefix != created.Token[:8] {
		t.Fatalf("list = %+v, want one token named ci with prefix %q", list, created.Token[:8])
	}

	// Duplicate name -> 409.
	doJSON(t, client, http.MethodPost, ts.URL+"/v1/api-tokens",
		map[string]any{"name": "ci"}, http.StatusConflict, nil)

	// The created token authenticates the MCP endpoint.
	tools, err := mcpclient.ListTools(context.Background(), ts.URL+"/mcp", "Bearer "+created.Token)
	if err != nil {
		t.Fatalf("MCP ListTools with created token: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected MCP tools for the authenticated token")
	}

	// Delete -> 204, then gone.
	doJSON(t, client, http.MethodDelete, ts.URL+"/v1/api-tokens/"+created.ID, nil, http.StatusNoContent, nil)
	doJSON(t, client, http.MethodGet, ts.URL+"/v1/api-tokens", nil, http.StatusOK, &list)
	if len(list) != 0 {
		t.Fatalf("after delete, list = %+v, want empty", list)
	}
}

// --- MCP endpoint end-to-end (DB-backed, real SDK client) ---

func TestMCPServerEndToEnd(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ws := seedWorkspace(t, srv.DB,
		`{"steps":[{"key":"a","type":"echo"},{"key":"b","type":"echo","needs":["a"]}]}`)

	// Mint a token for this workspace directly.
	raw, hash, prefix, err := newAPIToken()
	if err != nil {
		t.Fatalf("newAPIToken: %v", err)
	}
	if _, err := srv.DB.Exec(context.Background(), `
		insert into workspace_api_tokens (workspace_id, name, token_hash, prefix, created_by)
		values ($1,$2,$3,$4,$5)`, ws.wsID, "e2e", hash, prefix, ws.userID); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	ts := httptest.NewServer(fullMux(srv))
	defer ts.Close()
	auth := "Bearer " + raw
	ctx := context.Background()

	// Missing/unknown token -> auth failure surfaced by the client.
	if _, err := mcpclient.ListTools(ctx, ts.URL+"/mcp", "Bearer oak_deadbeef"); err == nil {
		t.Fatal("expected auth failure for an unknown token")
	}

	// tools/list exposes all three tools.
	tools, err := mcpclient.ListTools(ctx, ts.URL+"/mcp", auth)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"list_workflows", "run_workflow", "get_run_status"} {
		if !names[want] {
			t.Fatalf("tool %q missing; got %v", want, names)
		}
	}

	// list_workflows returns the seeded workflow with a step count.
	res, err := mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "list_workflows", nil)
	if err != nil {
		t.Fatalf("list_workflows: %v", err)
	}
	wfList := asArray(t, res)
	if len(wfList) != 1 {
		t.Fatalf("list_workflows returned %d workflows, want 1: %v", len(wfList), res)
	}
	wf0 := wfList[0].(map[string]any)
	if wf0["slug"] != ws.slug {
		t.Fatalf("workflow slug = %v, want %q", wf0["slug"], ws.slug)
	}
	if wf0["is_enabled"] != true {
		t.Fatalf("workflow is_enabled = %v, want true", wf0["is_enabled"])
	}
	if steps := wf0["steps"].(float64); steps != 2 {
		t.Fatalf("workflow steps = %v, want 2", steps)
	}

	// run_workflow by slug, waiting for completion -> succeeded with per-step outputs.
	res, err = mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "run_workflow",
		map[string]any{"workflow": ws.slug, "wait_seconds": 20})
	if err != nil {
		t.Fatalf("run_workflow: %v", err)
	}
	runRes := asObject(t, res)
	if runRes["status"] != "succeeded" {
		t.Fatalf("run status = %v, want succeeded: %v", runRes["status"], runRes)
	}
	outputs, ok := runRes["outputs"].(map[string]any)
	if !ok || outputs["a"] == nil || outputs["b"] == nil {
		t.Fatalf("expected outputs for steps a and b, got %v", runRes["outputs"])
	}
	runID, _ := runRes["run_id"].(string)
	if runID == "" {
		t.Fatalf("missing run_id in %v", runRes)
	}

	// get_run_status echoes the same terminal run with task rows.
	res, err = mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "get_run_status",
		map[string]any{"run_id": runID})
	if err != nil {
		t.Fatalf("get_run_status: %v", err)
	}
	statusRes := asObject(t, res)
	if statusRes["status"] != "succeeded" {
		t.Fatalf("get_run_status status = %v, want succeeded", statusRes["status"])
	}
	if tasks, ok := statusRes["tasks"].([]any); !ok || len(tasks) != 2 {
		t.Fatalf("expected 2 task rows, got %v", statusRes["tasks"])
	}

	// run_workflow by id, no wait -> immediate queued.
	res, err = mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "run_workflow",
		map[string]any{"workflow": ws.wfID.String()})
	if err != nil {
		t.Fatalf("run_workflow by id: %v", err)
	}
	if s := asObject(t, res)["status"]; s != "queued" {
		t.Fatalf("no-wait run status = %v, want queued", s)
	}

	// Unknown run id -> tool error, no cross-workspace leak.
	if _, err := mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "get_run_status",
		map[string]any{"run_id": uuid.New().String()}); err == nil {
		t.Fatal("expected 'run not found' error for an unknown run")
	}

	// Disabled workflow cannot be started programmatically.
	if _, err := srv.DB.Exec(ctx,
		`update workflows set is_enabled=false where id=$1`, ws.wfID); err != nil {
		t.Fatalf("disable workflow: %v", err)
	}
	if _, err := mcpclient.CallTool(ctx, ts.URL+"/mcp", auth, "run_workflow",
		map[string]any{"workflow": ws.slug}); err == nil {
		t.Fatal("expected 'workflow is disabled' error")
	}
}

// --- helpers ---

// asArray normalizes an MCP tool result (StructuredContent, or JSON text) to a
// slice.
func asArray(t *testing.T, res any) []any {
	t.Helper()
	switch v := res.(type) {
	case []any:
		return v
	case map[string]any:
		if txt, ok := v["text"].(string); ok {
			var arr []any
			if err := json.Unmarshal([]byte(txt), &arr); err != nil {
				t.Fatalf("decode text as array: %v (%q)", err, txt)
			}
			return arr
		}
	}
	t.Fatalf("result is not an array: %#v", res)
	return nil
}

func asObject(t *testing.T, res any) map[string]any {
	t.Helper()
	switch v := res.(type) {
	case map[string]any:
		if txt, ok := v["text"].(string); ok && len(v) == 1 {
			var obj map[string]any
			if err := json.Unmarshal([]byte(txt), &obj); err != nil {
				t.Fatalf("decode text as object: %v (%q)", err, txt)
			}
			return obj
		}
		return v
	}
	t.Fatalf("result is not an object: %#v", res)
	return nil
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any, wantStatus int, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: status = %d, want %d (%s)", method, url, resp.StatusCode, wantStatus, b)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
}
