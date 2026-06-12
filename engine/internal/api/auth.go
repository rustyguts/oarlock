package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Session auth with first-run auto-login: until real signup lands (Phase 2
// step 14), a request without a valid session is automatically logged in as
// the seeded default user — owner of the default workspace — so a fresh
// install works with zero setup. The tenant is the Workspace (design §3);
// every request resolves to one workspace + role, and handlers must scope
// every query by it (hard rule 3).

const (
	sessionCookie = "oarlock_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type authInfo struct {
	UserID        uuid.UUID
	Email         string
	UserName      *string
	WorkspaceID   uuid.UUID
	WorkspaceName string
	Role          string
}

type ctxKey struct{}

func authFrom(r *http.Request) *authInfo {
	a, _ := r.Context().Value(ctxKey{}).(*authInfo)
	return a
}

// workspace returns the authenticated workspace id for a request.
func (s *Server) workspace(r *http.Request) uuid.UUID {
	if a := authFrom(r); a != nil {
		return a.WorkspaceID
	}
	return uuid.Nil
}

// WithAuth resolves (or bootstraps) the session and attaches user + workspace
// to the request context.
func (s *Server) WithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a *authInfo
		if c, err := r.Cookie(sessionCookie); err == nil {
			a, _ = s.loadSession(r.Context(), c.Value)
		}
		if a == nil {
			var err error
			a, err = s.bootstrapSession(r.Context(), w)
			if err != nil {
				s.error(w, http.StatusUnauthorized, fmt.Errorf("auto-login failed: %w", err))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	})
}

func (s *Server) loadSession(ctx context.Context, token string) (*authInfo, error) {
	var a authInfo
	err := s.DB.QueryRow(ctx, `
		select u.id, u.email, u.name, m.workspace_id, w.name, m.role
		from sessions se
		join users u on u.id = se.user_id
		join workspace_members m on m.user_id = u.id
		join workspaces w on w.id = m.workspace_id
		where se.token = $1 and se.expires_at > now()
		order by m.created_at
		limit 1`, token).
		Scan(&a.UserID, &a.Email, &a.UserName, &a.WorkspaceID, &a.WorkspaceName, &a.Role)
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.Exec(ctx, `update sessions set last_seen_at = now() where token = $1`, token)
	return &a, nil
}

// bootstrapSession logs the seeded default user in automatically and sets the
// session cookie. The default user is the workspace owner (role ladder:
// owner > admin > editor > viewer), created by migration 0001.
func (s *Server) bootstrapSession(ctx context.Context, w http.ResponseWriter) (*authInfo, error) {
	var a authInfo
	err := s.DB.QueryRow(ctx, `
		select u.id, u.email, u.name, m.workspace_id, ws.name, m.role
		from users u
		join workspace_members m on m.user_id = u.id
		join workspaces ws on ws.id = m.workspace_id
		order by u.created_at, m.created_at
		limit 1`).
		Scan(&a.UserID, &a.Email, &a.UserName, &a.WorkspaceID, &a.WorkspaceName, &a.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("no bootstrap user; check migration seed")
	}
	if err != nil {
		return nil, err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	if _, err := s.DB.Exec(ctx, `
		insert into sessions (user_id, token, expires_at)
		values ($1, $2, now() + $3::interval)`,
		a.UserID, token, fmt.Sprintf("%d hours", int(sessionTTL.Hours()))); err != nil {
		return nil, err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.Log.Info("bootstrap auto-login", "user", a.Email, "workspace", a.WorkspaceName, "role", a.Role)
	return &a, nil
}

// me returns the authenticated user, workspace, and role.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r)
	if a == nil {
		s.error(w, http.StatusUnauthorized, fmt.Errorf("no session"))
		return
	}
	s.json(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":    a.UserID,
			"email": a.Email,
			"name":  a.UserName,
		},
		"workspace": map[string]any{
			"id":   a.WorkspaceID,
			"name": a.WorkspaceName,
		},
		"role": a.Role,
	})
}
