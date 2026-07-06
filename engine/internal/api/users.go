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

// User management (project.md §7): admin-only. Signup stays closed — admins
// create accounts with an initial password and must_change_password=true, so
// the user is forced through a password change on first login. The last
// admin-tier member (owner|admin) can never be deleted or demoted.

var allowedRoles = map[string]bool{"admin": true, "editor": true, "viewer": true}

func (s *Server) userRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/users", s.requireAdmin(s.listUsers))
	mux.HandleFunc("POST /v1/users", s.requireAdmin(s.createUser))
	mux.HandleFunc("PATCH /v1/users/{id}", s.requireAdmin(s.updateUser))
	mux.HandleFunc("DELETE /v1/users/{id}", s.requireAdmin(s.deleteUser))
	mux.HandleFunc("POST /v1/users/{id}/reset-password", s.requireAdmin(s.resetUserPassword))
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		select u.id, u.email, u.name, m.role, u.must_change_password, u.created_at::text,
		       (select max(se.last_seen_at) from sessions se where se.user_id = u.id)::text
		from users u
		join workspace_members m on m.user_id = u.id
		where m.workspace_id = $1
		order by u.created_at`, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	type userRow struct {
		ID                 uuid.UUID `json:"id"`
		Email              string    `json:"email"`
		Name               *string   `json:"name"`
		Role               string    `json:"role"`
		MustChangePassword bool      `json:"must_change_password"`
		CreatedAt          string    `json:"created_at"`
		LastSeenAt         *string   `json:"last_seen_at"`
	}
	out := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.MustChangePassword,
			&u.CreatedAt, &u.LastSeenAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, u)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("a valid email is required"))
		return
	}
	if len(req.Password) < minPasswordLen {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("password must be at least %d characters", minPasswordLen))
		return
	}
	if !allowedRoles[req.Role] {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("role must be admin, editor, or viewer"))
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer tx.Rollback(r.Context())

	var id uuid.UUID
	err = tx.QueryRow(r.Context(), `
		insert into users (email, name, password_hash, must_change_password)
		values ($1, $2, $3, true) returning id`,
		email, strings.TrimSpace(req.Name), hash).Scan(&id)
	if err != nil {
		s.error(w, http.StatusConflict, fmt.Errorf("a user with email %q already exists", email))
		return
	}
	if _, err := tx.Exec(r.Context(), `
		insert into workspace_members (workspace_id, user_id, role)
		values ($1, $2, $3)`, s.workspace(r), id, req.Role); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusCreated, map[string]any{"id": id})
}

// adminCount counts admin-tier members (owner|admin) in the workspace.
func (s *Server) adminCount(r *http.Request) (int, error) {
	var n int
	err := s.DB.QueryRow(r.Context(), `
		select count(*) from workspace_members
		where workspace_id = $1 and role in ('owner','admin')`, s.workspace(r)).Scan(&n)
	return n, err
}

// memberRole loads a user's role in the request workspace (pgx.ErrNoRows if
// they are not a member).
func (s *Server) memberRole(r *http.Request, userID uuid.UUID) (string, error) {
	var role string
	err := s.DB.QueryRow(r.Context(), `
		select role from workspace_members
		where workspace_id = $1 and user_id = $2`, s.workspace(r), userID).Scan(&role)
	return role, err
}

func isAdminRole(role string) bool { return role == "owner" || role == "admin" }

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid user id"))
		return
	}
	var req struct {
		Name *string `json:"name"`
		Role *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}

	current, err := s.memberRole(r, id)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("user not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	if req.Role != nil && *req.Role != current {
		if !allowedRoles[*req.Role] {
			s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("role must be admin, editor, or viewer"))
			return
		}
		// Demoting the last admin-tier member would lock everyone out.
		if isAdminRole(current) && !isAdminRole(*req.Role) {
			n, err := s.adminCount(r)
			if err != nil {
				s.error(w, http.StatusInternalServerError, err)
				return
			}
			if n <= 1 {
				s.error(w, http.StatusConflict, fmt.Errorf("cannot demote the last admin"))
				return
			}
		}
		if _, err := s.DB.Exec(r.Context(), `
			update workspace_members set role = $3
			where workspace_id = $1 and user_id = $2`, s.workspace(r), id, *req.Role); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
	}
	if req.Name != nil {
		if _, err := s.DB.Exec(r.Context(),
			`update users set name = $2 where id = $1`, id, strings.TrimSpace(*req.Name)); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.json(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid user id"))
		return
	}
	role, err := s.memberRole(r, id)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("user not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if isAdminRole(role) {
		n, err := s.adminCount(r)
		if err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		if n <= 1 {
			s.error(w, http.StatusConflict, fmt.Errorf("cannot delete the last admin"))
			return
		}
	}
	// Sessions and memberships cascade; created_by references become NULL
	// (migration 0010).
	if _, err := s.DB.Exec(r.Context(), `delete from users where id = $1`, id); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetUserPassword sets an admin-issued temporary password and forces the
// user through a change on next login. All their sessions are invalidated.
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid user id"))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if len(req.Password) < minPasswordLen {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("password must be at least %d characters", minPasswordLen))
		return
	}
	if _, err := s.memberRole(r, id); errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("user not found"))
		return
	} else if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.DB.Exec(r.Context(), `
		update users set password_hash = $2, must_change_password = true
		where id = $1`, id, hash); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	_, _ = s.DB.Exec(r.Context(), `delete from sessions where user_id = $1`, id)
	s.json(w, http.StatusOK, map[string]any{"id": id})
}
