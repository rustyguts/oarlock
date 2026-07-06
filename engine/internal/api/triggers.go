package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/robfig/cron/v3"
)

// Triggers fire runs of a workflow: 'schedule' (cron, swept by the engine) and
// 'webhook' (unauthenticated POST /hooks/{ws}/{path}). Both are workspace-scoped
// — schedule/webhook config lives in the triggers.config jsonb. A webhook's path
// is unique per workspace (partial unique index, migration 0008).

var webhookPathPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (s *Server) triggerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/workflows/{id}/triggers", s.listTriggers)
	mux.HandleFunc("POST /v1/workflows/{id}/triggers", s.createTrigger)
	mux.HandleFunc("PATCH /v1/triggers/{id}", s.updateTrigger)
	mux.HandleFunc("DELETE /v1/triggers/{id}", s.deleteTrigger)
}

type triggerRow struct {
	ID        uuid.UUID       `json:"id"`
	Type      string          `json:"type"`
	Config    json.RawMessage `json:"config"`
	IsEnabled bool            `json:"is_enabled"`
	CreatedAt string          `json:"created_at"`
}

// isUniqueViolation reports whether err is a Postgres unique-constraint failure
// (SQLSTATE 23505) — used to turn a duplicate webhook path into a clean 409.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// validateTriggerConfig checks a trigger's config against its type and returns
// the normalized config to persist (only the known fields, so junk isn't
// stored). The error is client-facing (surfaced as 422).
func validateTriggerConfig(typ string, raw json.RawMessage) (json.RawMessage, error) {
	switch typ {
	case "schedule":
		var cfg struct {
			Cron string `json:"cron"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("invalid schedule config: %w", err)
		}
		expr := strings.TrimSpace(cfg.Cron)
		if expr == "" {
			return nil, fmt.Errorf("schedule config requires a cron expression")
		}
		if _, err := cron.ParseStandard(expr); err != nil {
			return nil, fmt.Errorf("invalid cron expression: %w", err)
		}
		return json.Marshal(map[string]string{"cron": expr})
	case "webhook":
		var cfg struct {
			Path   string `json:"path"`
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("invalid webhook config: %w", err)
		}
		path := strings.TrimSpace(cfg.Path)
		if !webhookPathPattern.MatchString(path) {
			return nil, fmt.Errorf("webhook path must match ^[a-z0-9][a-z0-9-]*$")
		}
		out := map[string]string{"path": path}
		if cfg.Secret != "" {
			out["secret"] = cfg.Secret
		}
		return json.Marshal(out)
	default:
		return nil, fmt.Errorf("type must be schedule or webhook")
	}
}

func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	// Scope via the workflow so a mismatched workspace yields an empty list, not
	// a leak. (A nonexistent/other-workspace workflow simply has no triggers.)
	rows, err := s.DB.Query(r.Context(), `
		select t.id, t.type, t.config, t.is_enabled, t.created_at::text
		from triggers t
		join workflows wf on wf.id = t.workflow_id
		where t.workflow_id = $1 and wf.workspace_id = $2
		order by t.created_at`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []triggerRow{}
	for rows.Next() {
		var t triggerRow
		if err := rows.Scan(&t.ID, &t.Type, &t.Config, &t.IsEnabled, &t.CreatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, t)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) createTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid workflow id"))
		return
	}
	var req struct {
		Type      string          `json:"type"`
		Config    json.RawMessage `json:"config"`
		IsEnabled *bool           `json:"is_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	config, err := validateTriggerConfig(req.Type, req.Config)
	if err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}

	// Confirm the workflow is in this workspace before inserting a trigger that
	// FKs to it — otherwise a cross-workspace workflow id would attach a trigger
	// under the wrong tenant.
	var wsID uuid.UUID
	err = s.DB.QueryRow(r.Context(),
		`select workspace_id from workflows where id = $1 and workspace_id = $2`,
		id, s.workspace(r)).Scan(&wsID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	var tid uuid.UUID
	err = s.DB.QueryRow(r.Context(), `
		insert into triggers (workspace_id, workflow_id, type, config, is_enabled)
		values ($1, $2, $3, $4, $5) returning id`,
		wsID, id, req.Type, config, enabled).Scan(&tid)
	if isUniqueViolation(err) {
		s.error(w, http.StatusConflict, fmt.Errorf("a webhook with that path already exists in this workspace"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": tid})
}

func (s *Server) updateTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid trigger id"))
		return
	}
	var req struct {
		Config    json.RawMessage `json:"config"`
		IsEnabled *bool           `json:"is_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}

	// Type is immutable, so re-validate any new config against the stored type.
	var typ string
	err = s.DB.QueryRow(r.Context(),
		`select type from triggers where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("trigger not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	var config *json.RawMessage
	if len(req.Config) > 0 && string(req.Config) != "null" {
		normalized, err := validateTriggerConfig(typ, req.Config)
		if err != nil {
			s.error(w, http.StatusUnprocessableEntity, err)
			return
		}
		config = &normalized
	}

	tag, err := s.DB.Exec(r.Context(), `
		update triggers set
		  config = coalesce($3, config),
		  is_enabled = coalesce($4, is_enabled)
		where id = $1 and workspace_id = $2`, id, s.workspace(r), config, req.IsEnabled)
	if isUniqueViolation(err) {
		s.error(w, http.StatusConflict, fmt.Errorf("a webhook with that path already exists in this workspace"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if tag.RowsAffected() == 0 {
		s.error(w, http.StatusNotFound, fmt.Errorf("trigger not found"))
		return
	}
	s.json(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid trigger id"))
		return
	}
	tag, err := s.DB.Exec(r.Context(),
		`delete from triggers where id = $1 and workspace_id = $2`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if tag.RowsAffected() == 0 {
		s.error(w, http.StatusNotFound, fmt.Errorf("trigger not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
