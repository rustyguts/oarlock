package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Workspace API tokens authenticate the MCP endpoint (see mcpserver.go). Tokens
// are shown once at creation; only their sha256 hash (via hashToken) is stored,
// alongside an 8-char prefix for display.

func (s *Server) tokenRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api-tokens", s.listAPITokens)
	mux.HandleFunc("POST /v1/api-tokens", s.createAPIToken)
	mux.HandleFunc("DELETE /v1/api-tokens/{id}", s.deleteAPIToken)
}

// newAPIToken mints a token in the form oak_<48 hex> (24 random bytes) and
// returns the raw token, its stored hash, and the display prefix (first 8 raw
// chars). The raw token is returned to the caller exactly once.
func newAPIToken() (raw, hash, prefix string, err error) {
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	raw = "oak_" + hex.EncodeToString(b)
	return raw, hashToken(raw), raw[:8], nil
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select id, name, prefix, created_at::text, last_used_at::text
		from workspace_api_tokens where workspace_id = $1 order by created_at desc`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type tokenRow struct {
		ID         uuid.UUID `json:"id"`
		Name       string    `json:"name"`
		Prefix     string    `json:"prefix"`
		CreatedAt  string    `json:"created_at"`
		LastUsedAt *string   `json:"last_used_at"`
	}
	out := []tokenRow{}
	for rows.Next() {
		var t tokenRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, t)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		s.error(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	name := strings.TrimSpace(req.Name)
	raw, hash, prefix, err := newAPIToken()
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	a := authFrom(r)
	var id uuid.UUID
	err = s.DB.QueryRow(r.Context(), `
		insert into workspace_api_tokens (workspace_id, name, token_hash, prefix, created_by)
		values ($1, $2, $3, $4, $5) returning id`,
		s.workspace(r), name, hash, prefix, a.UserID).Scan(&id)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("a token named %q already exists", name))
		return
	}
	// The raw token is returned once and never persisted in cleartext.
	s.json(w, http.StatusCreated, map[string]any{"id": id, "token": raw})
}

func (s *Server) deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid token id"))
		return
	}
	tag, err := s.DB.Exec(r.Context(),
		`delete from workspace_api_tokens where id = $1 and workspace_id = $2`, id, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if tag.RowsAffected() == 0 {
		s.error(w, http.StatusNotFound, fmt.Errorf("token not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
