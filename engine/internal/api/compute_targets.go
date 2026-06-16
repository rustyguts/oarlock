package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Compute targets: referenced by name from container.* step configs
// (config.compute_target). Renaming/deleting one referenced by a workflow's
// current version is blocked (mirrors mcp_servers).

type computeTargetRow struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Backend         string    `json:"backend"`
	Namespace       string    `json:"namespace,omitempty"`
	RuntimeClass    string    `json:"runtime_class,omitempty"`
	CPULimit        string    `json:"cpu_limit"`
	MemoryMBLimit   int       `json:"memory_mb_limit"`
	TimeoutSecLimit int       `json:"timeout_sec_limit"`
	ImageAllowlist  []string  `json:"image_allowlist"`
	RegistrySecret  string    `json:"registry_secret_name,omitempty"`
	IsEnabled       bool      `json:"is_enabled"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
}

func (s *Server) listComputeTargets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select id, name, backend, coalesce(namespace,''), coalesce(runtime_class,''),
		       cpu_limit, memory_mb_limit, timeout_sec_limit, image_allowlist,
		       coalesce(registry_secret_name,''), is_enabled, created_at::text, updated_at::text
		from compute_targets where workspace_id = $1 order by name`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []computeTargetRow{}
	for rows.Next() {
		var t computeTargetRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Backend, &t.Namespace, &t.RuntimeClass,
			&t.CPULimit, &t.MemoryMBLimit, &t.TimeoutSecLimit, &t.ImageAllowlist,
			&t.RegistrySecret, &t.IsEnabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, t)
	}
	s.json(w, http.StatusOK, out)
}

type computeTargetReq struct {
	Name            string   `json:"name"`
	Backend         string   `json:"backend"`
	Namespace       string   `json:"namespace"`
	RuntimeClass    string   `json:"runtime_class"`
	CPULimit        string   `json:"cpu_limit"`
	MemoryMBLimit   int      `json:"memory_mb_limit"`
	TimeoutSecLimit int      `json:"timeout_sec_limit"`
	ImageAllowlist  []string `json:"image_allowlist"`
	RegistrySecret  string   `json:"registry_secret_name"`
	IsEnabled       *bool    `json:"is_enabled"`
}

func (req *computeTargetReq) normalize() error {
	req.Name = strings.TrimSpace(req.Name)
	if !secretNamePattern.MatchString(req.Name) {
		return fmt.Errorf("name must be alphanumeric/_/- (referenced as compute_target in step configs)")
	}
	if req.Backend != "docker" && req.Backend != "k8s" {
		return fmt.Errorf("backend must be docker or k8s")
	}
	if strings.TrimSpace(req.CPULimit) == "" {
		req.CPULimit = "1"
	}
	if req.MemoryMBLimit <= 0 {
		req.MemoryMBLimit = 1024
	}
	if req.TimeoutSecLimit <= 0 {
		req.TimeoutSecLimit = 600
	}
	if req.ImageAllowlist == nil {
		req.ImageAllowlist = []string{}
	}
	return nil
}

func nullStr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) createComputeTarget(w http.ResponseWriter, r *http.Request) {
	var req computeTargetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if err := req.normalize(); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	a := authFrom(r)
	var id uuid.UUID
	err := s.DB.QueryRow(r.Context(), `
		insert into compute_targets (workspace_id, name, backend, namespace, runtime_class,
		  cpu_limit, memory_mb_limit, timeout_sec_limit, image_allowlist, registry_secret_name,
		  is_enabled, created_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) returning id`,
		s.workspace(r), req.Name, req.Backend, nullStr(req.Namespace), nullStr(req.RuntimeClass),
		req.CPULimit, req.MemoryMBLimit, req.TimeoutSecLimit, req.ImageAllowlist,
		nullStr(req.RegistrySecret), enabled, a.UserID).Scan(&id)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("a compute target named %q already exists", req.Name))
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) updateComputeTarget(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid compute target id"))
		return
	}
	var req computeTargetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if err := req.normalize(); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}
	var current string
	err = s.DB.QueryRow(r.Context(),
		`select name from compute_targets where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("compute target not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if current != req.Name {
		refs, err := s.referencingWorkflows(r, "container.%", "compute_target", current)
		if err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		if len(refs) > 0 {
			s.json(w, http.StatusConflict, map[string]any{
				"error":     fmt.Sprintf("cannot rename: %q is used by %d workflow(s)", current, len(refs)),
				"workflows": refs,
			})
			return
		}
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	_, err = s.DB.Exec(r.Context(), `
		update compute_targets set name=$3, backend=$4, namespace=$5, runtime_class=$6,
		  cpu_limit=$7, memory_mb_limit=$8, timeout_sec_limit=$9, image_allowlist=$10,
		  registry_secret_name=$11, is_enabled=$12, updated_at=now()
		where id=$1 and workspace_id=$2`,
		id, s.workspace(r), req.Name, req.Backend, nullStr(req.Namespace), nullStr(req.RuntimeClass),
		req.CPULimit, req.MemoryMBLimit, req.TimeoutSecLimit, req.ImageAllowlist,
		nullStr(req.RegistrySecret), enabled)
	if err != nil {
		s.error(w, http.StatusConflict, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteComputeTarget(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid compute target id"))
		return
	}
	var name string
	err = s.DB.QueryRow(r.Context(),
		`select name from compute_targets where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("compute target not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	refs, err := s.referencingWorkflows(r, "container.%", "compute_target", name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if len(refs) > 0 {
		s.json(w, http.StatusConflict, map[string]any{
			"error":     fmt.Sprintf("compute target %q is used by %d workflow(s)", name, len(refs)),
			"workflows": refs,
		})
		return
	}
	if _, err := s.DB.Exec(r.Context(),
		`delete from compute_targets where id = $1 and workspace_id = $2`, id, s.workspace(r)); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
