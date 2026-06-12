package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rustyguts/oarlock/engine/internal/mcpclient"
)

// Workspace resources: secrets and MCP servers. Both are referenced from
// step configs by *name*; deletion is blocked while any workflow's current
// version references them.

func (s *Server) resourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/secrets", s.listSecrets)
	mux.HandleFunc("POST /v1/secrets", s.createSecret)
	mux.HandleFunc("DELETE /v1/secrets/{id}", s.deleteSecret)
	mux.HandleFunc("GET /v1/mcp-servers", s.listMCPServers)
	mux.HandleFunc("POST /v1/mcp-servers", s.createMCPServer)
	mux.HandleFunc("PUT /v1/mcp-servers/{id}", s.updateMCPServer)
	mux.HandleFunc("DELETE /v1/mcp-servers/{id}", s.deleteMCPServer)
	mux.HandleFunc("GET /v1/mcp-servers/{id}/tools", s.listMCPServerTools)
}

// referencingWorkflows returns names of workflows whose *current* version has
// a step of type matching typePrefix with config[configKey] == value.
func (s *Server) referencingWorkflows(r *http.Request, typePrefix, configKey, value string) ([]string, error) {
	rows, err := s.DB.Query(r.Context(), `
		select distinct w.name
		from workflows w
		join workflow_versions v on v.id = w.current_version_id,
		     jsonb_array_elements(coalesce(v.definition->'steps', '[]'::jsonb)) st
		where w.workspace_id = $1
		  and st->>'type' like $2
		  and st->'config'->>$3 = $4
		order by w.name`, s.workspace(r), typePrefix, configKey, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// --- secrets (Configuration UI) ---
// type 'api_key' = BYOK for ai.* steps; 'generic' = any sensitive value,
// usable anywhere via {{secrets.<name>}}. Values are write-only.

var allowedProviders = map[string]bool{"anthropic": true, "openai": true, "openrouter": true}

var secretNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select id, name, type, coalesce(provider, ''), encrypted_data, created_at::text
		from secrets where workspace_id = $1 order by name`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type secretRow struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Type      string    `json:"type"`
		Provider  string    `json:"provider,omitempty"`
		ValueHint string    `json:"value_hint"`
		CreatedAt string    `json:"created_at"`
	}
	// Masked hints only — values never leave the API.
	all, _ := s.Vault.WorkspaceSecrets(r.Context(), s.workspace(r))
	out := []secretRow{}
	for rows.Next() {
		var c secretRow
		var sealed []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Provider, &sealed, &c.CreatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		if v := all[c.Name]; len(v) > 4 {
			c.ValueHint = "…" + v[len(v)-4:]
		}
		out = append(out, c)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Provider string `json:"provider"`
		Value    string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Value) == "" {
		s.error(w, http.StatusBadRequest, fmt.Errorf("name, type, and value are required"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if !secretNamePattern.MatchString(name) {
		s.error(w, http.StatusUnprocessableEntity,
			fmt.Errorf("name must be alphanumeric/_/- (it is referenced as secrets.<name> in expressions)"))
		return
	}
	switch req.Type {
	case "api_key":
		if !allowedProviders[req.Provider] {
			s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("api_key secrets need a provider: anthropic, openai, or openrouter"))
			return
		}
	case "generic":
		req.Provider = ""
	default:
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("type must be generic or api_key"))
		return
	}
	sealed, err := s.Vault.SealSecret(strings.TrimSpace(req.Value))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	a := authFrom(r)
	var provider *string
	if req.Provider != "" {
		provider = &req.Provider
	}
	var id uuid.UUID
	err = s.DB.QueryRow(r.Context(), `
		insert into secrets (workspace_id, name, type, provider, encrypted_data, key_id, created_by)
		values ($1, $2, $3, $4, $5, $6, $7) returning id`,
		s.workspace(r), name, req.Type, provider, sealed, "local-v1", a.UserID).Scan(&id)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("a secret named %q already exists", name))
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid secret id"))
		return
	}
	var name string
	err = s.DB.QueryRow(r.Context(),
		`select name from secrets where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("secret not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	// Referenced either as an ai.* api_key or anywhere as secrets.<name>.
	refs, err := s.referencingWorkflows(r, "ai.%", "api_key", name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	textRefs, err := s.workflowsMentioning(r, "secrets."+name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	seen := map[string]bool{}
	var all []string
	for _, n := range append(refs, textRefs...) {
		if !seen[n] {
			seen[n] = true
			all = append(all, n)
		}
	}
	if len(all) > 0 {
		s.json(w, http.StatusConflict, map[string]any{
			"error":     fmt.Sprintf("secret %q is used by %d workflow(s)", name, len(all)),
			"workflows": all,
		})
		return
	}
	if _, err := s.DB.Exec(r.Context(),
		`delete from secrets where id = $1 and workspace_id = $2`, id, s.workspace(r)); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workflowsMentioning finds workflows whose current definition contains a
// literal substring (used for {{secrets.<name>}} references).
func (s *Server) workflowsMentioning(r *http.Request, needle string) ([]string, error) {
	rows, err := s.DB.Query(r.Context(), `
		select distinct w.name
		from workflows w
		join workflow_versions v on v.id = w.current_version_id
		where w.workspace_id = $1 and v.definition::text like '%' || $2 || '%'
		order by w.name`, s.workspace(r), needle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// --- MCP servers ---

type mcpServerRow struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	HasAuth   bool      `json:"has_auth"`
	IsEnabled bool      `json:"is_enabled"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

func (s *Server) listMCPServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select id, name, url, auth_encrypted is not null, is_enabled,
		       created_at::text, updated_at::text
		from mcp_servers where workspace_id = $1 order by name`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []mcpServerRow{}
	for rows.Next() {
		var m mcpServerRow
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.HasAuth, &m.IsEnabled, &m.CreatedAt, &m.UpdatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, m)
	}
	s.json(w, http.StatusOK, out)
}

type mcpServerReq struct {
	Name       string  `json:"name"`
	URL        string  `json:"url"`
	AuthHeader *string `json:"auth_header"` // null = keep, "" = clear, value = set
	IsEnabled  *bool   `json:"is_enabled"`
}

func (req *mcpServerReq) validate() error {
	if strings.TrimSpace(req.Name) == "" {
		return fmt.Errorf("name is required")
	}
	u := strings.TrimSpace(req.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("url must start with http:// or https://")
	}
	return nil
}

func (s *Server) sealAuth(header *string) ([]byte, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return nil, nil
	}
	return s.Vault.Encrypt([]byte(strings.TrimSpace(*header)))
}

func (s *Server) createMCPServer(w http.ResponseWriter, r *http.Request) {
	var req mcpServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if err := req.validate(); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}
	sealed, err := s.sealAuth(req.AuthHeader)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	a := authFrom(r)
	var id uuid.UUID
	err = s.DB.QueryRow(r.Context(), `
		insert into mcp_servers (workspace_id, name, url, auth_encrypted, key_id, created_by)
		values ($1, $2, $3, $4, $5, $6) returning id`,
		s.workspace(r), strings.TrimSpace(req.Name), strings.TrimSpace(req.URL),
		sealed, "local-v1", a.UserID).Scan(&id)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("an MCP server named %q already exists", req.Name))
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) updateMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid mcp server id"))
		return
	}
	var req mcpServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if err := req.validate(); err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}

	// Renaming would orphan step configs that reference the old name.
	var current string
	err = s.DB.QueryRow(r.Context(),
		`select name from mcp_servers where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("mcp server not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if current != strings.TrimSpace(req.Name) {
		refs, err := s.referencingWorkflows(r, "mcp.%", "server", current)
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
	if req.AuthHeader != nil {
		sealed, err := s.sealAuth(req.AuthHeader)
		if err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		_, err = s.DB.Exec(r.Context(), `
			update mcp_servers set name = $3, url = $4, is_enabled = $5,
			  auth_encrypted = $6, updated_at = now()
			where id = $1 and workspace_id = $2`,
			id, s.workspace(r), strings.TrimSpace(req.Name), strings.TrimSpace(req.URL), enabled, sealed)
		if err != nil {
			s.error(w, http.StatusConflict, err)
			return
		}
	} else {
		_, err = s.DB.Exec(r.Context(), `
			update mcp_servers set name = $3, url = $4, is_enabled = $5, updated_at = now()
			where id = $1 and workspace_id = $2`,
			id, s.workspace(r), strings.TrimSpace(req.Name), strings.TrimSpace(req.URL), enabled)
		if err != nil {
			s.error(w, http.StatusConflict, err)
			return
		}
	}
	s.json(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid mcp server id"))
		return
	}
	var name string
	err = s.DB.QueryRow(r.Context(),
		`select name from mcp_servers where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("mcp server not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	refs, err := s.referencingWorkflows(r, "mcp.%", "server", name)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if len(refs) > 0 {
		s.json(w, http.StatusConflict, map[string]any{
			"error":     fmt.Sprintf("MCP server %q is used by %d workflow(s)", name, len(refs)),
			"workflows": refs,
		})
		return
	}
	if _, err := s.DB.Exec(r.Context(),
		`delete from mcp_servers where id = $1 and workspace_id = $2`, id, s.workspace(r)); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMCPServerTools connects to the server live and returns its tools —
// used by the config UI for discovery and as the connection test.
func (s *Server) listMCPServerTools(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid mcp server id"))
		return
	}
	var name string
	err = s.DB.QueryRow(r.Context(),
		`select name from mcp_servers where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("mcp server not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	url, auth, err := s.Vault.Server(r.Context(), s.workspace(r), name)
	if err != nil {
		s.error(w, http.StatusUnprocessableEntity, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tools, err := mcpclient.ListTools(ctx, url, auth)
	if err != nil {
		s.error(w, http.StatusBadGateway, err)
		return
	}
	s.json(w, http.StatusOK, tools)
}
