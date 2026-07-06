package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Built-in auth (project.md §7): email+password login over the existing
// hashed-token session mechanism. While no user has a password the API is in
// setup mode — authenticated routes 401 with setup_required and POST
// /v1/setup claims the migration-seeded owner account in place, making the
// first account the admin by construction. oak_ API tokens authenticate the
// whole /v1 API at member tier (never the admin surface). The old first-run
// auto-login bootstrap is gone.

const (
	sessionCookie = "oarlock_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// principalKind distinguishes session users from API-token access.
type principalKind string

const (
	principalSession principalKind = "session"
	principalToken   principalKind = "token"
)

type authInfo struct {
	Kind               principalKind
	UserID             uuid.UUID // Nil for token principals
	Email              string
	UserName           *string
	WorkspaceID        uuid.UUID
	WorkspaceName      string
	WorkspaceSlug      string
	Role               string // workspace_members role; "editor" for tokens
	MustChangePassword bool
	sessionTokenHash   string // hashed cookie token; used to keep the current session on password change
}

// admin reports whether the principal is admin-tier (owner|admin). Token
// principals are never admin — a leaked key must not manage users or mint
// credentials.
func (a *authInfo) admin() bool {
	return a != nil && a.Kind == principalSession && (a.Role == "owner" || a.Role == "admin")
}

// userIDOrNil is for created_by columns: token principals record NULL.
func (a *authInfo) userIDOrNil() *uuid.UUID {
	if a == nil || a.UserID == uuid.Nil {
		return nil
	}
	id := a.UserID
	return &id
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

// authSkip are /v1 paths that must work without a principal: they establish
// one (or tear one down). Their handlers enforce their own rules.
var authSkip = map[string]bool{
	"/v1/setup":  true,
	"/v1/login":  true,
	"/v1/logout": true,
}

// WithAuth resolves the request principal: a Bearer oak_ token (workspace
// credential, member tier) wins over a session cookie. Without either the
// request is 401 — flagged setup_required while no user has a password yet.
func (s *Server) WithAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authSkip[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			a, err := s.tokenPrincipal(r)
			if err != nil {
				s.error(w, http.StatusUnauthorized, fmt.Errorf("invalid API token"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
			return
		}

		if c, err := r.Cookie(sessionCookie); err == nil {
			if a, err := s.loadSession(r.Context(), c.Value); err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
				return
			}
		}

		setup, err := s.setupRequired(r.Context())
		if err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		s.json(w, http.StatusUnauthorized, map[string]any{
			"error":          "authentication required",
			"setup_required": setup,
		})
	})
}

// requireAdmin gates the admin surface (user management, API tokens).
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authFrom(r).admin() {
			s.error(w, http.StatusForbidden, fmt.Errorf("admin access required"))
			return
		}
		next(w, r)
	}
}

// requireSession gates self-service auth endpoints token principals must
// never reach (password changes).
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := authFrom(r)
		if a == nil || a.Kind != principalSession {
			s.error(w, http.StatusForbidden, fmt.Errorf("session required"))
			return
		}
		next(w, r)
	}
}

// tokenPrincipal resolves an Authorization: Bearer oak_ token into a
// member-tier workspace principal.
func (s *Server) tokenPrincipal(r *http.Request) (*authInfo, error) {
	wsID, ok := s.authenticateToken(r)
	if !ok {
		return nil, fmt.Errorf("invalid token")
	}
	var a authInfo
	a.Kind = principalToken
	a.Role = "editor"
	a.WorkspaceID = wsID
	err := s.DB.QueryRow(r.Context(),
		`select name, slug from workspaces where id = $1`, wsID).
		Scan(&a.WorkspaceName, &a.WorkspaceSlug)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// setupConfigured caches the one-way "a password exists" transition so the
// common 401 path doesn't query Postgres once the instance is set up.
var setupConfigured atomic.Bool

// setupRequired reports whether no user can log in yet (first run).
func (s *Server) setupRequired(ctx context.Context) (bool, error) {
	if setupConfigured.Load() {
		return false, nil
	}
	var exists bool
	if err := s.DB.QueryRow(ctx,
		`select exists(select 1 from users where password_hash is not null)`).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		setupConfigured.Store(true)
	}
	return !exists, nil
}

// loginAttempts rate-limits failed logins process-wide (per-replica in HA).
var loginAttempts = newLoginLimiter()

// hashToken maps a raw cookie/bearer token to the value stored at rest, so a
// database leak never exposes usable credentials.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) loadSession(ctx context.Context, token string) (*authInfo, error) {
	hashed := hashToken(token)
	a := authInfo{Kind: principalSession, sessionTokenHash: hashed}
	err := s.DB.QueryRow(ctx, `
		select u.id, u.email, u.name, u.must_change_password, m.workspace_id, w.name, w.slug, m.role
		from sessions se
		join users u on u.id = se.user_id
		join workspace_members m on m.user_id = u.id
		join workspaces w on w.id = m.workspace_id
		where se.token = $1 and se.expires_at > now()
		order by m.created_at
		limit 1`, hashed).
		Scan(&a.UserID, &a.Email, &a.UserName, &a.MustChangePassword,
			&a.WorkspaceID, &a.WorkspaceName, &a.WorkspaceSlug, &a.Role)
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.Exec(ctx, `update sessions set last_seen_at = now() where token = $1`, hashed)
	return &a, nil
}

// --- setup -----------------------------------------------------------------

// setup claims the migration-seeded owner account in place: sets email, name,
// and the first password. Guarded by password_hash IS NULL so concurrent
// setup attempts race safely — exactly one wins, the rest see 409.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	required, err := s.setupRequired(r.Context())
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if !required {
		s.error(w, http.StatusConflict, fmt.Errorf("setup is already complete"))
		return
	}
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	name := strings.TrimSpace(req.Name)
	if email == "" || !strings.Contains(email, "@") {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("a valid email is required"))
		return
	}
	if len(req.Password) < minPasswordLen {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("password must be at least %d characters", minPasswordLen))
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	var userID uuid.UUID
	err = s.DB.QueryRow(r.Context(), `
		update users set email = $1, name = $2, password_hash = $3, must_change_password = false
		where id = (
			select u.id from users u
			join workspace_members m on m.user_id = u.id and m.role = 'owner'
			where u.password_hash is null
			order by u.created_at
			limit 1
		) and password_hash is null
		returning id`, email, name, hash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusConflict, fmt.Errorf("setup is already complete"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	setupConfigured.Store(true)

	// Invalidate anything minted before the account had a password (the old
	// auto-login bootstrap sessions), then log the admin straight in.
	_, _ = s.DB.Exec(r.Context(), `delete from sessions where user_id = $1`, userID)
	if err := s.startSession(w, r, userID); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.Log.Info("setup complete: admin account created", "email", email)
	s.json(w, http.StatusCreated, map[string]any{"ok": true})
}

// --- login / logout ----------------------------------------------------------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	limitKey := clientIP(r) + "|" + email
	if !loginAttempts.allowed(limitKey) {
		s.error(w, http.StatusTooManyRequests, fmt.Errorf("too many attempts; try again later"))
		return
	}

	// Uniform failure path: same error for unknown email and wrong password.
	var userID uuid.UUID
	var hash *string
	err := s.DB.QueryRow(r.Context(),
		`select id, password_hash from users where email = $1`, email).Scan(&userID, &hash)
	if err != nil || hash == nil || !verifyPassword(*hash, req.Password) {
		loginAttempts.fail(limitKey)
		s.error(w, http.StatusUnauthorized, fmt.Errorf("invalid email or password"))
		return
	}
	loginAttempts.reset(limitKey)

	// Opportunistic cleanup, then a fresh token (no session fixation).
	_, _ = s.DB.Exec(r.Context(), `delete from sessions where expires_at < now()`)
	if err := s.startSession(w, r, userID); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{"ok": true})
}

// logout deletes the current session and clears the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.DB.Exec(r.Context(), `delete from sessions where token = $1`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- password change ---------------------------------------------------------

// changePassword lets the logged-in user set a new password. The current
// password is required unless the account is flagged must_change_password
// (admin-issued temp password). All other sessions are invalidated.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid body"))
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		s.error(w, http.StatusUnprocessableEntity, fmt.Errorf("password must be at least %d characters", minPasswordLen))
		return
	}
	if !a.MustChangePassword {
		var hash *string
		if err := s.DB.QueryRow(r.Context(),
			`select password_hash from users where id = $1`, a.UserID).Scan(&hash); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		if hash == nil || !verifyPassword(*hash, req.CurrentPassword) {
			s.error(w, http.StatusForbidden, fmt.Errorf("current password is incorrect"))
			return
		}
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.DB.Exec(r.Context(), `
		update users set password_hash = $2, must_change_password = false
		where id = $1`, a.UserID, hash); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	// Kill every other session for this user; the current one stays.
	_, _ = s.DB.Exec(r.Context(),
		`delete from sessions where user_id = $1 and token <> $2`, a.UserID, a.sessionTokenHash)
	s.json(w, http.StatusOK, map[string]any{"ok": true})
}

// --- session plumbing --------------------------------------------------------

// startSession mints a session for userID and sets the cookie. Only the
// sha256 of the token is stored; the raw token lives solely in the cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := hex.EncodeToString(raw)
	if _, err := s.DB.Exec(r.Context(), `
		insert into sessions (user_id, token, expires_at)
		values ($1, $2, now() + $3::interval)`,
		userID, hashToken(token), fmt.Sprintf("%d hours", int(sessionTTL.Hours()))); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// secureCookies decides the cookie Secure flag: "always"/"never" force it,
// "auto" (default) marks Secure when the request arrived over TLS, directly
// or via the ingress (X-Forwarded-Proto).
func (s *Server) secureCookies(r *http.Request) bool {
	switch s.SecureCookies {
	case "always":
		return true
	case "never":
		return false
	}
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// --- me ----------------------------------------------------------------------

// me returns the authenticated principal, workspace, and role.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a := authFrom(r)
	if a == nil {
		s.error(w, http.StatusUnauthorized, fmt.Errorf("no session"))
		return
	}
	var user any
	if a.Kind == principalSession {
		user = map[string]any{
			"id":    a.UserID,
			"email": a.Email,
			"name":  a.UserName,
		}
	}
	s.json(w, http.StatusOK, map[string]any{
		"auth_kind":            a.Kind,
		"user":                 user,
		"must_change_password": a.MustChangePassword,
		"workspace": map[string]any{
			"id":   a.WorkspaceID,
			"name": a.WorkspaceName,
			"slug": a.WorkspaceSlug,
		},
		"role": a.Role,
		"vault": map[string]any{
			"dev_key": s.Vault.DevKey(),
		},
	})
}
