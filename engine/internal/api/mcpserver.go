package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rustyguts/oarlock/engine/internal/engine"
)

// The workspace MCP server (design §6 step 18): every workspace's workflows
// become tools an AI agent can call. The design doc routes the workspace via
// the URL; we scope via the token instead — one stable /mcp URL, and the
// bearer token IS the workspace credential. This is consistent with the future
// cell-routing JWT plan, where a signed token likewise carries the tenant.
//
// A single StreamableHTTPHandler serves every workspace. Auth runs per request
// (bearer -> sha256 -> workspace_api_tokens), stashing the workspace id in the
// request context; the SDK's getServer callback then builds a per-request
// server whose three tools close over that workspace id. Stateless mode keeps
// each request self-contained, which suits these read/start tools.

type mcpWorkspaceKey struct{}

// MCPHandler authenticates the bearer token, resolves the workspace, and
// delegates to the MCP streamable handler. Mount it OUTSIDE the session-auth
// wrapper (the token is the credential), inside MaxBody + CORS.
func (s *Server) MCPHandler() http.Handler {
	handler := mcp.NewStreamableHTTPHandler(s.mcpServerForRequest, &mcp.StreamableHTTPOptions{Stateless: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsID, ok := s.authenticateToken(r)
		if !ok {
			s.json(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing API token"})
			return
		}
		ctx := context.WithValue(r.Context(), mcpWorkspaceKey{}, wsID)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticateToken resolves the workspace for an "Authorization: Bearer oak_..."
// header via a sha256 hash lookup. last_used_at is bumped best-effort.
func (s *Server) authenticateToken(r *http.Request) (uuid.UUID, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return uuid.Nil, false
	}
	raw := strings.TrimSpace(h[len(prefix):])
	if !strings.HasPrefix(raw, "oak_") {
		return uuid.Nil, false
	}
	var tokenID, wsID uuid.UUID
	err := s.DB.QueryRow(r.Context(),
		`select id, workspace_id from workspace_api_tokens where token_hash = $1`, hashToken(raw)).
		Scan(&tokenID, &wsID)
	if err != nil {
		return uuid.Nil, false
	}
	_, _ = s.DB.Exec(r.Context(),
		`update workspace_api_tokens set last_used_at = now() where id = $1`, tokenID)
	return wsID, true
}

// mcpServerForRequest builds a per-request MCP server whose tools are bound to
// the workspace resolved by authenticateToken.
func (s *Server) mcpServerForRequest(r *http.Request) *mcp.Server {
	wsID, _ := r.Context().Value(mcpWorkspaceKey{}).(uuid.UUID)
	srv := mcp.NewServer(&mcp.Implementation{Name: "oarlock", Version: mcpServerVersion}, nil)
	s.addWorkspaceTools(srv, wsID)
	return srv
}

// mcpServerVersion is reported to MCP clients (independent of the API build version).
const mcpServerVersion = "0.1"

// --- tool schemas ---

type runWorkflowInput struct {
	Workflow    string         `json:"workflow" jsonschema:"the workflow id (uuid) or slug to run"`
	Input       map[string]any `json:"input,omitempty" jsonschema:"optional JSON object passed as the run input"`
	WaitSeconds int            `json:"wait_seconds,omitempty" jsonschema:"seconds to wait for the run to finish, 0-60; 0 returns immediately with status queued"`
}

type getRunStatusInput struct {
	RunID string `json:"run_id" jsonschema:"the run id returned by run_workflow"`
}

func (s *Server) addWorkspaceTools(srv *mcp.Server, wsID uuid.UUID) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_workflows",
		Description: "List this workspace's runnable workflows (those with a saved version). Returns id, name, slug, whether the workflow is enabled, current version number, and step count. Use a workflow's id or slug with run_workflow.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return s.mcpListWorkflows(ctx, wsID)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_workflow",
		Description: "Start a run of a workflow by id or slug, with an optional JSON input object. Set wait_seconds (0-60) to wait for completion; when the run finishes it returns each step's output. Disabled workflows cannot be started programmatically.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runWorkflowInput) (*mcp.CallToolResult, any, error) {
		return s.mcpRunWorkflow(ctx, wsID, in)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_run_status",
		Description: "Get the status of a run started with run_workflow: overall status, per-task status and errors, and the outputs of succeeded steps.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getRunStatusInput) (*mcp.CallToolResult, any, error) {
		return s.mcpGetRunStatus(ctx, wsID, in)
	})
}

// --- tool handlers ---

func (s *Server) mcpListWorkflows(ctx context.Context, wsID uuid.UUID) (*mcp.CallToolResult, any, error) {
	rows, err := s.DB.Query(ctx, `
		select w.id, w.name, w.slug, w.is_enabled, v.version,
		       jsonb_array_length(coalesce(v.definition->'steps', '[]'::jsonb))
		from workflows w
		join workflow_versions v on v.id = w.current_version_id
		where w.workspace_id = $1
		order by w.name`, wsID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type wfSummary struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Slug      string    `json:"slug"`
		IsEnabled bool      `json:"is_enabled"`
		Version   int       `json:"version"`
		Steps     int       `json:"steps"`
	}
	out := []wfSummary{}
	for rows.Next() {
		var wf wfSummary
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Slug, &wf.IsEnabled, &wf.Version, &wf.Steps); err != nil {
			return nil, nil, err
		}
		out = append(out, wf)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func (s *Server) mcpRunWorkflow(ctx context.Context, wsID uuid.UUID, in runWorkflowInput) (*mcp.CallToolResult, any, error) {
	arg := strings.TrimSpace(in.Workflow)
	if arg == "" {
		return nil, nil, fmt.Errorf("workflow is required")
	}
	// Resolve by id or slug in one query (id cast to text avoids a uuid parse).
	var wfID uuid.UUID
	var enabled, hasVersion bool
	err := s.DB.QueryRow(ctx, `
		select id, is_enabled, current_version_id is not null
		from workflows
		where workspace_id = $1 and (id::text = $2 or slug = $2)
		limit 1`, wsID, arg).Scan(&wfID, &enabled, &hasVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("workflow not found")
	}
	if err != nil {
		return nil, nil, err
	}
	// The enable toggle gates programmatic starts (like triggers); manual UI
	// runs remain the only exception.
	if !enabled {
		return nil, nil, fmt.Errorf("workflow is disabled")
	}
	if !hasVersion {
		return nil, nil, fmt.Errorf("workflow has no saved version")
	}

	runID, _, err := s.Engine.StartRunOpts(ctx, wsID, wfID, in.Input, engine.RunOpts{})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("workflow is disabled")
	}
	if err != nil {
		return nil, nil, err
	}

	wait := in.WaitSeconds
	if wait <= 0 {
		return nil, map[string]any{"run_id": runID.String(), "status": "queued"}, nil
	}
	if wait > 60 {
		wait = 60
	}

	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := s.fetchRun(ctx, wsID, runID)
		if err != nil {
			return nil, nil, err
		}
		if isTerminal(run.Status) {
			return nil, map[string]any{
				"run_id":  runID.String(),
				"status":  run.Status,
				"outputs": stepOutputs(run),
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, map[string]any{"run_id": runID.String(), "status": run.Status}, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) mcpGetRunStatus(ctx context.Context, wsID uuid.UUID, in getRunStatusInput) (*mcp.CallToolResult, any, error) {
	// Parse + workspace-scoped fetch: an unknown or cross-workspace run is
	// indistinguishable from a missing one (hard rule 7 — never leak).
	runID, err := uuid.Parse(strings.TrimSpace(in.RunID))
	if err != nil {
		return nil, nil, fmt.Errorf("run not found")
	}
	run, err := s.fetchRun(ctx, wsID, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("run not found")
	}
	if err != nil {
		return nil, nil, err
	}
	type taskInfo struct {
		StepKey string          `json:"step_key"`
		Attempt int             `json:"attempt"`
		Status  string          `json:"status"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	tasks := []taskInfo{}
	for _, t := range run.Tasks {
		tasks = append(tasks, taskInfo{StepKey: t.StepKey, Attempt: t.Attempt, Status: t.Status, Error: t.Error})
	}
	return nil, map[string]any{
		"run_id":      run.ID.String(),
		"status":      run.Status,
		"created_at":  run.CreatedAt,
		"finished_at": run.FinishedAt,
		"tasks":       tasks,
		"outputs":     stepOutputs(run),
	}, nil
}

// stepOutputs maps step_key -> output for succeeded tasks (latest attempt wins,
// since fetchRun orders attempts ascending). Redaction already happened when
// the task output was persisted.
func stepOutputs(run *runDetail) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, t := range run.Tasks {
		if t.Status == "succeeded" && len(t.Output) > 0 {
			out[t.StepKey] = t.Output
		}
	}
	return out
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	}
	return false
}
