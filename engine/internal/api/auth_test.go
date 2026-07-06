package api

// DB-backed tests for built-in auth (project.md §7): first-run setup claiming
// the seeded owner, login/logout, forced password change, admin-only user
// management with last-admin guards, and oak_ bearer tokens on /v1 with the
// admin surface excluded. Reuses the mcp_test.go harness (apiTestPool,
// newTestServer, fullMux, seedWorkspace, doJSON).

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupAdmin completes first-run setup (claiming the seeded owner) and
// returns a cookie-jar client logged in as that admin.
func setupAdmin(t *testing.T, ts *httptest.Server, email, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	doJSON(t, client, http.MethodPost, ts.URL+"/v1/setup",
		map[string]any{"email": email, "name": "Admin", "password": password},
		http.StatusCreated, nil)
	return client
}

// loginClient returns a fresh cookie-jar client logged in as email.
func loginClient(t *testing.T, ts *httptest.Server, email, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	doJSON(t, client, http.MethodPost, ts.URL+"/v1/login",
		map[string]any{"email": email, "password": password}, http.StatusOK, nil)
	return client
}

// doBearer performs a request with an Authorization: Bearer header and
// asserts the status code.
func doBearer(t *testing.T, method, url, token string, wantStatus int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d (%s)", method, url, resp.StatusCode, wantStatus, body)
	}
	return body
}

func TestSetupAndLoginFlow(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedWorkspace(t, srv.DB, `{"steps":[{"key":"a","type":"echo"}]}`)

	ts := httptest.NewServer(fullMux(srv))
	defer ts.Close()

	// Unauthenticated before setup → 401 flagged setup_required.
	resp, err := http.Get(ts.URL + "/v1/workflows")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"setup_required":true`) {
		t.Fatalf("pre-setup = %d %s, want 401 with setup_required", resp.StatusCode, body)
	}

	admin := setupAdmin(t, ts, "admin@test", "first-password")

	// The claimed account is the workspace owner.
	var me struct {
		AuthKind string `json:"auth_kind"`
		Role     string `json:"role"`
		User     struct {
			Email string `json:"email"`
		} `json:"user"`
		MustChangePassword bool `json:"must_change_password"`
	}
	doJSON(t, admin, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusOK, &me)
	if me.Role != "owner" || me.User.Email != "admin@test" || me.AuthKind != "session" {
		t.Fatalf("me = %+v, want owner admin@test via session", me)
	}

	// Second setup attempt → 409; unauthenticated is no longer setup_required.
	doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/v1/setup",
		map[string]any{"email": "x@test", "password": "whatever-long"}, http.StatusConflict, nil)
	resp, _ = http.Get(ts.URL + "/v1/workflows")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"setup_required":false`) {
		t.Fatalf("post-setup unauth = %d %s, want 401 without setup_required", resp.StatusCode, body)
	}

	// Wrong password → uniform 401. Right password → fresh session works.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	doJSON(t, c, http.MethodPost, ts.URL+"/v1/login",
		map[string]any{"email": "admin@test", "password": "wrong"}, http.StatusUnauthorized, nil)
	user := loginClient(t, ts, "admin@test", "first-password")
	doJSON(t, user, http.MethodGet, ts.URL+"/v1/workflows", nil, http.StatusOK, nil)

	// Logout kills the session.
	doJSON(t, user, http.MethodPost, ts.URL+"/v1/logout", nil, http.StatusNoContent, nil)
	doJSON(t, user, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusUnauthorized, nil)
}

func TestUsersCRUDAndGuards(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedWorkspace(t, srv.DB, `{"steps":[{"key":"a","type":"echo"}]}`)

	ts := httptest.NewServer(fullMux(srv))
	defer ts.Close()
	admin := setupAdmin(t, ts, "root@test", "root-password")

	// Create an editor with a temp password.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/users",
		map[string]any{"email": "eve@test", "name": "Eve", "password": "temp-password", "role": "editor"},
		http.StatusCreated, &created)

	// Duplicate email → 409; bad role → 422.
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/users",
		map[string]any{"email": "eve@test", "password": "temp-password", "role": "editor"},
		http.StatusConflict, nil)
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/users",
		map[string]any{"email": "o@test", "password": "temp-password", "role": "owner"},
		http.StatusUnprocessableEntity, nil)

	// Eve logs in, is flagged for a forced change, changes password (no
	// current password needed), and the flag clears.
	eve := loginClient(t, ts, "eve@test", "temp-password")
	var me struct {
		Role               string `json:"role"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusOK, &me)
	if me.Role != "editor" || !me.MustChangePassword {
		t.Fatalf("eve me = %+v, want editor with must_change_password", me)
	}
	doJSON(t, eve, http.MethodPost, ts.URL+"/v1/password",
		map[string]any{"new_password": "eve-own-password"}, http.StatusOK, nil)
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusOK, &me)
	if me.MustChangePassword {
		t.Fatal("must_change_password should clear after the change")
	}
	// Now the current password is required (and checked) for further changes.
	doJSON(t, eve, http.MethodPost, ts.URL+"/v1/password",
		map[string]any{"current_password": "nope", "new_password": "whatever-long"},
		http.StatusForbidden, nil)

	// Members can use the API but not the admin surface.
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/workflows", nil, http.StatusOK, nil)
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/users", nil, http.StatusForbidden, nil)
	doJSON(t, eve, http.MethodPost, ts.URL+"/v1/api-tokens",
		map[string]any{"name": "sneaky"}, http.StatusForbidden, nil)

	// Admin reset: eve's sessions die and the temp password forces a change.
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/users/"+created.ID+"/reset-password",
		map[string]any{"password": "reset-password"}, http.StatusOK, nil)
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusUnauthorized, nil)
	eve = loginClient(t, ts, "eve@test", "reset-password")
	doJSON(t, eve, http.MethodGet, ts.URL+"/v1/me", nil, http.StatusOK, &me)
	if !me.MustChangePassword {
		t.Fatal("admin reset must flag must_change_password")
	}

	// Last-admin guards: the owner is the only admin-tier member, so demoting
	// or deleting them is refused. Promote eve, then the owner can demote.
	var users []struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	doJSON(t, admin, http.MethodGet, ts.URL+"/v1/users", nil, http.StatusOK, &users)
	var ownerID string
	for _, u := range users {
		if u.Role == "owner" {
			ownerID = u.ID
		}
	}
	doJSON(t, admin, http.MethodPatch, ts.URL+"/v1/users/"+ownerID,
		map[string]any{"role": "editor"}, http.StatusConflict, nil)
	doJSON(t, admin, http.MethodDelete, ts.URL+"/v1/users/"+ownerID, nil, http.StatusConflict, nil)
	doJSON(t, admin, http.MethodPatch, ts.URL+"/v1/users/"+created.ID,
		map[string]any{"role": "admin"}, http.StatusOK, nil)
	doJSON(t, admin, http.MethodPatch, ts.URL+"/v1/users/"+ownerID,
		map[string]any{"role": "editor"}, http.StatusOK, nil)

	// The demoted owner lost the admin surface; eve (now the sole admin) can't
	// be deleted as the last admin, but deleting the demoted owner works.
	doJSON(t, admin, http.MethodGet, ts.URL+"/v1/users", nil, http.StatusForbidden, nil)
	doJSON(t, eve, http.MethodDelete, ts.URL+"/v1/users/"+created.ID, nil, http.StatusConflict, nil)
	doJSON(t, eve, http.MethodDelete, ts.URL+"/v1/users/"+ownerID, nil, http.StatusNoContent, nil)
}

func TestBearerTokenOnAPI(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedWorkspace(t, srv.DB, `{"steps":[{"key":"a","type":"echo"}]}`)

	ts := httptest.NewServer(fullMux(srv))
	defer ts.Close()
	admin := setupAdmin(t, ts, "boss@test", "boss-password")

	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/api-tokens",
		map[string]any{"name": "robot"}, http.StatusCreated, &created)

	// Token drives the API at member tier…
	doBearer(t, http.MethodGet, ts.URL+"/v1/workflows", created.Token, http.StatusOK)
	body := doBearer(t, http.MethodGet, ts.URL+"/v1/me", created.Token, http.StatusOK)
	if !strings.Contains(string(body), `"auth_kind":"token"`) {
		t.Fatalf("token /v1/me = %s, want auth_kind token", body)
	}
	// …but never the admin surface or self-service auth.
	doBearer(t, http.MethodGet, ts.URL+"/v1/users", created.Token, http.StatusForbidden)
	doBearer(t, http.MethodGet, ts.URL+"/v1/api-tokens", created.Token, http.StatusForbidden)
	doBearer(t, http.MethodPost, ts.URL+"/v1/password", created.Token, http.StatusForbidden)
	// Garbage tokens are rejected outright.
	doBearer(t, http.MethodGet, ts.URL+"/v1/workflows", "oak_deadbeef", http.StatusUnauthorized)

	// Rotation: the old secret dies, the new one works, id/name are stable.
	var rotated struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, admin, http.MethodPost, ts.URL+"/v1/api-tokens/"+created.ID+"/rotate",
		nil, http.StatusOK, &rotated)
	if rotated.ID != created.ID || rotated.Token == created.Token || !strings.HasPrefix(rotated.Token, "oak_") {
		t.Fatalf("rotate = %+v, want same id with a fresh oak_ token", rotated)
	}
	doBearer(t, http.MethodGet, ts.URL+"/v1/workflows", created.Token, http.StatusUnauthorized)
	doBearer(t, http.MethodGet, ts.URL+"/v1/workflows", rotated.Token, http.StatusOK)
}
