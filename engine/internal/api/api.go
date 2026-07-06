// Package api implements the REST API: workflows, versions, runs, triggers,
// secrets, MCP servers/tokens. Every request resolves a workspace from the
// session cookie (auto-login bootstrap until real signup lands) and all
// queries are scoped by it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/rustyguts/oarlock/engine/internal/definition"
	"github.com/rustyguts/oarlock/engine/internal/engine"
	"github.com/rustyguts/oarlock/engine/internal/vault"
)

type Server struct {
	DB     *pgxpool.Pool
	Engine *engine.Engine
	Cache  *redis.Client
	Vault  *vault.Vault
	Log    *slog.Logger
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("GET /v1/stats", s.stats)
	mux.HandleFunc("GET /v1/step-types", s.listStepTypes)
	mux.HandleFunc("GET /v1/workflows", s.listWorkflows)
	mux.HandleFunc("POST /v1/workflows", s.createWorkflow)
	mux.HandleFunc("GET /v1/workflows/{id}", s.getWorkflow)
	mux.HandleFunc("PATCH /v1/workflows/{id}", s.updateWorkflow)
	mux.HandleFunc("DELETE /v1/workflows/{id}", s.deleteWorkflow)
	mux.HandleFunc("PUT /v1/workflows/{id}/definition", s.putDefinition)
	mux.HandleFunc("GET /v1/workflows/{id}/versions", s.listVersions)
	mux.HandleFunc("GET /v1/workflows/{id}/versions/{version}", s.getVersion)
	mux.HandleFunc("POST /v1/logout", s.logout)
	mux.HandleFunc("POST /v1/workflows/{id}/runs", s.startRun)
	mux.HandleFunc("GET /v1/workflows/{id}/runs", s.listRuns)
	mux.HandleFunc("GET /v1/runs/{id}", s.getRun)
	mux.HandleFunc("POST /v1/runs/{id}/cancel", s.cancelRun)
	mux.HandleFunc("POST /v1/runs/{id}/retry", s.retryRun)
	mux.HandleFunc("GET /v1/runs/{id}/events", s.runEvents)
	mux.HandleFunc("GET /v1/runs/{id}/logs", s.runLogs)
	mux.HandleFunc("GET /v1/runs/{id}/logs.txt", s.runLogsDownload)
	s.resourceRoutes(mux)
	s.triggerRoutes(mux)
	s.tokenRoutes(mux)
}

// CORS lets the web UI (different origin) call the API directly. Sessions ride
// a cookie, so credentialed requests can't use a wildcard origin: the request
// origin is echoed only when it's on the explicit allowlist
// (OARLOCK_ALLOWED_ORIGINS). Vary: Origin keeps caches from serving the wrong
// ACAO to a different origin.
func CORS(allowed []string, next http.Handler) http.Handler {
	allow := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		if o = strings.TrimSpace(o); o != "" {
			allow[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary on every response (not just allowed ones) so a shared cache
		// never replays a response minus ACAO headers to an allowed origin.
		w.Header().Set("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && allow[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MaxBody caps request bodies so a single request can't force an unbounded
// read. Oversize bodies surface through the handlers' JSON-decode error paths
// as 400s (http.MaxBytesError is returned by Decode).
func MaxBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

// --- step types ---

func (s *Server) listStepTypes(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, s.Engine.Registry.Types())
}

// --- workflows ---

type workflowRow struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	IsEnabled   bool            `json:"is_enabled"`
	Version     *int            `json:"version"`
	RunCount    int             `json:"run_count"`
	FailedCount int             `json:"failed_count"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Definition  json.RawMessage `json:"definition,omitempty"`
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select w.id, w.name, w.slug, w.is_enabled, v.version,
		       count(r.id) as run_count,
		       count(r.id) filter (where r.status = 'failed') as failed_count,
		       w.created_at::text, w.updated_at::text
		from workflows w
		left join workflow_versions v on v.id = w.current_version_id
		left join runs r on r.workflow_id = w.id
		where w.workspace_id = $1
		group by w.id, v.version
		order by w.created_at desc`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []workflowRow{}
	for rows.Next() {
		var wf workflowRow
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Slug, &wf.IsEnabled, &wf.Version,
			&wf.RunCount, &wf.FailedCount, &wf.CreatedAt, &wf.UpdatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, wf)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		s.error(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	defRaw := req.Definition
	if len(defRaw) == 0 {
		defRaw = json.RawMessage(`{"steps": []}`)
	}
	if err := s.validateDefinition(defRaw); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}

	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback(r.Context())

	var wfID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		insert into workflows (workspace_id, name, slug)
		values ($1, $2, $3) returning id`,
		s.workspace(r), req.Name, slugify(req.Name)).Scan(&wfID)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("create workflow: %w", err))
		return
	}
	var versionID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		insert into workflow_versions (workflow_id, version, definition)
		values ($1, 1, $2) returning id`, wfID, defRaw).Scan(&versionID)
	if err == nil {
		_, err = tx.Exec(r.Context(),
			`update workflows set current_version_id = $2 where id = $1`, wfID, versionID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": wfID, "version": 1})
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	var wf workflowRow
	err = s.DB.QueryRow(r.Context(), `
		select w.id, w.name, w.slug, w.is_enabled, v.version, v.definition,
		       w.created_at::text, w.updated_at::text
		from workflows w
		left join workflow_versions v on v.id = w.current_version_id
		where w.id = $1 and w.workspace_id = $2`, id, s.workspace(r)).
		Scan(&wf.ID, &wf.Name, &wf.Slug, &wf.IsEnabled, &wf.Version, &wf.Definition, &wf.CreatedAt, &wf.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, wf)
}

// updateWorkflow patches workflow metadata (name, is_enabled). Only provided
// fields change; slug is immutable because it's referenced elsewhere.
// is_enabled gates triggers and programmatic (MCP) starts — manual runs stay
// allowed regardless of it.
func (s *Server) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	var req struct {
		Name      *string `json:"name"`
		IsEnabled *bool   `json:"is_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	var name *string
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			s.error(w, http.StatusBadRequest, fmt.Errorf("name cannot be empty"))
			return
		}
		name = &trimmed
	}
	var outName string
	var enabled bool
	err = s.DB.QueryRow(r.Context(), `
		update workflows set
		  name = coalesce($3, name),
		  is_enabled = coalesce($4, is_enabled),
		  updated_at = now()
		where id = $1 and workspace_id = $2
		returning name, is_enabled`, id, s.workspace(r), name, req.IsEnabled).Scan(&outName, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{"id": id, "name": outName, "is_enabled": enabled})
}

func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	tag, err := s.DB.Exec(r.Context(),
		`delete from workflows where id = $1 and workspace_id = $2`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if tag.RowsAffected() == 0 {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putDefinition saves the canvas: validate, insert the next immutable
// version, point current_version_id at it.
func (s *Server) putDefinition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	var req struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Definition) == 0 {
		s.error(w, http.StatusBadRequest, fmt.Errorf("definition is required"))
		return
	}
	if err := s.validateDefinition(req.Definition); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}

	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback(r.Context())

	var version int
	err = tx.QueryRow(r.Context(), `
		insert into workflow_versions (workflow_id, version, definition)
		select w.id, coalesce(max(v.version), 0) + 1, $3
		from workflows w
		left join workflow_versions v on v.workflow_id = w.id
		where w.id = $1 and w.workspace_id = $2
		group by w.id
		returning version`, id, s.workspace(r), req.Definition).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	_, err = tx.Exec(r.Context(), `
		update workflows set
		  current_version_id = (select id from workflow_versions where workflow_id = $1 and version = $2),
		  updated_at = now()
		where id = $1`, id, version)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{"id": id, "version": version})
}

func (s *Server) validateDefinition(raw json.RawMessage) error {
	def, err := definition.Parse(raw)
	if err != nil {
		return err
	}
	return def.Validate(s.Engine.Registry.Has)
}

// listVersions returns the immutable version history, newest first. Rollback is
// re-saving an old definition through PUT .../definition, so there's no mutating
// version endpoint. Workspace-scoped via the workflows join.
func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	rows, err := s.DB.Query(r.Context(), `
		select v.version, v.created_at::text,
		       jsonb_array_length(coalesce(v.definition->'steps', '[]'::jsonb)) as step_count,
		       (v.id = w.current_version_id) as is_current
		from workflow_versions v
		join workflows w on w.id = v.workflow_id
		where w.id = $1 and w.workspace_id = $2
		order by v.version desc`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type versionRow struct {
		Version   int    `json:"version"`
		CreatedAt string `json:"created_at"`
		StepCount int    `json:"step_count"`
		IsCurrent bool   `json:"is_current"`
	}
	out := []versionRow{}
	for rows.Next() {
		var v versionRow
		if err := rows.Scan(&v.Version, &v.CreatedAt, &v.StepCount, &v.IsCurrent); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, v)
	}
	s.json(w, http.StatusOK, out)
}

// getVersion returns one pinned version's definition (for preview / rollback).
func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid version"))
		return
	}
	var out struct {
		Version    int             `json:"version"`
		CreatedAt  string          `json:"created_at"`
		Definition json.RawMessage `json:"definition"`
	}
	err = s.DB.QueryRow(r.Context(), `
		select v.version, v.created_at::text, v.definition
		from workflow_versions v
		join workflows w on w.id = v.workflow_id
		where w.id = $1 and w.workspace_id = $2 and v.version = $3`,
		id, s.workspace(r), version).Scan(&out.Version, &out.CreatedAt, &out.Definition)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("version not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, out)
}

// --- runs ---

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	var req struct {
		Input          map[string]any `json:"input"`
		IdempotencyKey string         `json:"idempotency_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // empty body = no input

	runID, created, err := s.Engine.StartRunOpts(r.Context(), s.workspace(r), id, req.Input,
		engine.RunOpts{IdempotencyKey: req.IdempotencyKey})
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found or has no version"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	// 201 for a new run, 200 when an idempotency key replayed an existing one.
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	s.json(w, status, map[string]any{"run_id": runID})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	// Keyset pagination: ?limit=1..200 (default 50) and ?before=<run id> for
	// the next page. `before` anchors on that run's (created_at, id) so ties on
	// created_at can't skip or repeat rows.
	limit := clampLimit(r.URL.Query().Get("limit"), 50, 200)
	var beforeCreated *string
	var beforeID *uuid.UUID
	if b := r.URL.Query().Get("before"); b != "" {
		bid, err := uuid.Parse(b)
		if err != nil {
			s.error(w, http.StatusBadRequest, fmt.Errorf("invalid before cursor"))
			return
		}
		var created string
		err = s.DB.QueryRow(r.Context(),
			`select created_at::text from runs where id = $1 and workspace_id = $2`,
			bid, s.workspace(r)).Scan(&created)
		if errors.Is(err, pgx.ErrNoRows) {
			s.error(w, http.StatusBadRequest, fmt.Errorf("unknown before cursor"))
			return
		}
		if err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		beforeCreated, beforeID = &created, &bid
	}
	rows, err := s.DB.Query(r.Context(), `
		select r.id, r.status::text, r.created_at::text, r.started_at::text, r.finished_at::text, v.version,
		       (select count(*) from tasks t where t.run_id = r.id) as task_count,
		       (select te.error->>'message' from tasks te
		        where te.run_id = r.id and te.status = 'failed'
		        order by te.finished_at desc nulls last limit 1) as error_summary
		from runs r join workflow_versions v on v.id = r.workflow_version_id
		where r.workflow_id = $1 and r.workspace_id = $2
		  and ($4::timestamptz is null or (r.created_at, r.id) < ($4, $5))
		order by r.created_at desc, r.id desc limit $3`,
		id, s.workspace(r), limit, beforeCreated, beforeID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type runRow struct {
		ID           uuid.UUID `json:"id"`
		Status       string    `json:"status"`
		CreatedAt    string    `json:"created_at"`
		StartedAt    *string   `json:"started_at"`
		FinishedAt   *string   `json:"finished_at"`
		Version      int       `json:"version"`
		TaskCount    int       `json:"task_count"`
		ErrorSummary *string   `json:"error_summary"`
	}
	out := []runRow{}
	for rows.Next() {
		var rr runRow
		if err := rows.Scan(&rr.ID, &rr.Status, &rr.CreatedAt, &rr.StartedAt, &rr.FinishedAt,
			&rr.Version, &rr.TaskCount, &rr.ErrorSummary); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, rr)
	}
	s.json(w, http.StatusOK, out)
}

type runDetail struct {
	ID           uuid.UUID       `json:"id"`
	WorkflowID   uuid.UUID       `json:"workflow_id"`
	WorkflowName string          `json:"workflow_name"`
	Version      int             `json:"version"`
	Definition   json.RawMessage `json:"definition"` // pinned version the run executed
	Status       string          `json:"status"`
	Input        json.RawMessage `json:"input"`
	Error        json.RawMessage `json:"error"`
	CreatedAt    string          `json:"created_at"`
	StartedAt    *string         `json:"started_at"`
	FinishedAt   *string         `json:"finished_at"`
	Tasks        []taskRow       `json:"tasks"`
}

func (s *Server) fetchRun(ctx context.Context, workspaceID, id uuid.UUID) (*runDetail, error) {
	var run runDetail
	err := s.DB.QueryRow(ctx, `
		select r.id, r.workflow_id, w.name, v.version, v.definition, r.status::text, r.input, r.error,
		       r.created_at::text, r.started_at::text, r.finished_at::text
		from runs r
		join workflows w on w.id = r.workflow_id
		join workflow_versions v on v.id = r.workflow_version_id
		where r.id = $1 and r.workspace_id = $2`, id, workspaceID).
		Scan(&run.ID, &run.WorkflowID, &run.WorkflowName, &run.Version, &run.Definition,
			&run.Status, &run.Input, &run.Error,
			&run.CreatedAt, &run.StartedAt, &run.FinishedAt)
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.Query(ctx, `
		select id, step_key, attempt, status::text, output, error,
		       queued_at::text, started_at::text, finished_at::text
		from tasks where run_id = $1
		order by queued_at, step_key, attempt`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	run.Tasks = []taskRow{}
	for rows.Next() {
		var t taskRow
		if err := rows.Scan(&t.ID, &t.StepKey, &t.Attempt, &t.Status, &t.Output, &t.Error,
			&t.QueuedAt, &t.StartedAt, &t.FinishedAt); err != nil {
			return nil, err
		}
		run.Tasks = append(run.Tasks, t)
	}
	return &run, rows.Err()
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	run, err := s.fetchRun(r.Context(), s.workspace(r), id)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, run)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	if err := s.Engine.CancelRun(r.Context(), s.workspace(r), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.error(w, http.StatusNotFound, fmt.Errorf("run not found"))
		} else {
			s.error(w, http.StatusConflict, err)
		}
		return
	}
	s.json(w, http.StatusOK, map[string]string{"status": "canceled"})
}

func (s *Server) retryRun(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	if err := s.Engine.RetryRun(r.Context(), s.workspace(r), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.error(w, http.StatusNotFound, fmt.Errorf("run not found"))
		} else {
			s.error(w, http.StatusConflict, err)
		}
		return
	}
	s.json(w, http.StatusOK, map[string]string{"status": "retrying"})
}

// runEvents streams run snapshots over SSE. Workers publish change pings to
// Valkey (fire-and-forget); each ping triggers a refetch from Postgres, which
// stays the source of truth. A slow ticker covers missed pings.
func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.error(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	ctx := r.Context()

	// Subscribe to change pings when Valkey is available. Valkey is never
	// load-bearing for correctness — Postgres is truth — so a nil cache or a
	// failed subscribe just falls back to a faster poll-only loop.
	var ch <-chan *redis.Message
	if s.Cache != nil {
		sub := s.Cache.Subscribe(ctx, engine.RunChannel(id))
		defer sub.Close()
		if _, err := sub.Receive(ctx); err == nil {
			ch = sub.Channel()
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	workspaceID := s.workspace(r)
	send := func() (terminal bool, err error) {
		run, err := s.fetchRun(ctx, workspaceID, id)
		if err != nil {
			return true, err
		}
		payload, _ := json.Marshal(run)
		if _, err := fmt.Fprintf(w, "event: run\ndata: %s\n\n", payload); err != nil {
			return true, err
		}
		flusher.Flush()
		switch run.Status {
		case "succeeded", "failed", "canceled":
			return true, nil
		}
		return false, nil
	}

	if terminal, err := send(); terminal || err != nil {
		return
	}

	// Poll cadence backs up (or fully covers) pings: slow when pings drive
	// refetches, fast when poll-only.
	interval := 5 * time.Second
	if ch == nil {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				// Subscription closed: drop it so this case (a receive on a
				// nil channel) blocks forever instead of spinning hot; the
				// ticker keeps the stream alive.
				ch = nil
				continue
			}
			// Coalesce a burst: a chatty task pings per log line, and each
			// ping is a full Postgres refetch per subscriber. Absorb further
			// pings for ~250ms, then refetch once.
			coalesce := time.NewTimer(250 * time.Millisecond)
		drain:
			for {
				select {
				case <-ctx.Done():
					coalesce.Stop()
					return
				case _, ok := <-ch:
					if !ok {
						ch = nil
						break drain
					}
				case <-coalesce.C:
					break drain
				}
			}
		case <-ticker.C:
		}
		if terminal, err := send(); terminal || err != nil {
			return
		}
	}
}

// runLogs returns the run's log lines, newest first.
func (s *Server) runLogs(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	// Keyset pagination, newest first: ?limit=1..2000 (default 500) and
	// ?before_id=<log id> for older pages.
	limit := clampLimit(r.URL.Query().Get("limit"), 500, 2000)
	var beforeID *int64
	if b := r.URL.Query().Get("before_id"); b != "" {
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil {
			s.error(w, http.StatusBadRequest, fmt.Errorf("invalid before_id cursor"))
			return
		}
		beforeID = &n
	}
	rows, err := s.DB.Query(r.Context(), `
		select id, task_id, step_key, ts::text, level, message, fields
		from task_logs
		where run_id = $1 and workspace_id = $2
		  and ($4::bigint is null or id < $4)
		order by id desc
		limit $3`, id, s.workspace(r), limit, beforeID)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type logLine struct {
		ID      int64           `json:"id"`
		TaskID  uuid.UUID       `json:"task_id"`
		StepKey string          `json:"step_key"`
		TS      string          `json:"ts"`
		Level   int             `json:"level"`
		Message string          `json:"message"`
		Fields  json.RawMessage `json:"fields"`
	}
	out := []logLine{}
	for rows.Next() {
		var l logLine
		if err := rows.Scan(&l.ID, &l.TaskID, &l.StepKey, &l.TS, &l.Level, &l.Message, &l.Fields); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, l)
	}
	s.json(w, http.StatusOK, out)
}

// runLogsDownload streams the full run log as a plaintext file, oldest first.
func (s *Server) runLogsDownload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	rows, err := s.DB.Query(r.Context(), `
		select ts::text, level, step_key, message, fields
		from task_logs
		where run_id = $1 and workspace_id = $2
		order by id asc`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="oarlock-run-%s.log"`, id.String()[:8]))

	levelName := func(l int) string {
		switch {
		case l >= 8:
			return "ERROR"
		case l >= 4:
			return "WARN"
		default:
			return "INFO"
		}
	}
	for rows.Next() {
		var ts, stepKey, message string
		var level int
		var fields []byte
		if err := rows.Scan(&ts, &level, &stepKey, &message, &fields); err != nil {
			return
		}
		line := fmt.Sprintf("%s %-5s [%s] %s", ts, levelName(level), stepKey, message)
		if len(fields) > 0 && string(fields) != "null" {
			line += " " + string(fields)
		}
		fmt.Fprintln(w, line)
	}
}

type taskRow struct {
	ID         uuid.UUID       `json:"id"`
	StepKey    string          `json:"step_key"`
	Attempt    int             `json:"attempt"`
	Status     string          `json:"status"`
	Output     json.RawMessage `json:"output"`
	Error      json.RawMessage `json:"error"`
	QueuedAt   string          `json:"queued_at"`
	StartedAt  *string         `json:"started_at"`
	FinishedAt *string         `json:"finished_at"`
}

// --- helpers ---

func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) error(w http.ResponseWriter, status int, err error) {
	msg := err.Error()
	if status >= 500 {
		// Log the real cause (SQL details etc.) but never leak it to clients.
		s.Log.Error("api error", "error", err)
		msg = "internal error"
	}
	s.json(w, status, map[string]string{"error": msg})
}

// clampLimit parses a ?limit= value, falling back to def and capping at max.
func clampLimit(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(s, "-")
}
