package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rustyguts/oarlock/engine/internal/engine"
)

// Callback resume: POST /resume/{token}. This endpoint is UNAUTHENTICATED — it's
// mounted outside the session-auth wrapper (main.go) so external approvers can
// resume a suspended step. The unguessable resume token IS the credential; the
// database stores only its sha256 (see engine.generateResumeToken), so a leak of
// the suspensions table can't be replayed. An optional JSON request body becomes
// the resumed step's payload; downstream steps see it via steps.<key>.payload.

// ResumeHandler exposes the unauthenticated callback resume handler for mounting
// in main.go outside the session-auth wrapper, alongside /hooks.
func (s *Server) ResumeHandler() http.Handler { return http.HandlerFunc(s.resume) }

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		s.error(w, http.StatusNotFound, fmt.Errorf("not found"))
		return
	}

	// Optional JSON body → the resumed step's payload. No body is a null payload
	// (an approver may just hit the URL); a non-empty body that isn't JSON is a
	// 400 rather than silently swallowed.
	var payload any
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("could not read body"))
		return
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			s.error(w, http.StatusBadRequest, fmt.Errorf("body must be JSON"))
			return
		}
	}

	// hashToken (api/auth.go) computes the same sha256 hex the engine stored.
	runID, err := s.Engine.ResumeSuspendedTask(r.Context(), hashToken(token), payload)
	switch {
	case errors.Is(err, engine.ErrSuspensionNotFound):
		s.error(w, http.StatusNotFound, fmt.Errorf("not found"))
		return
	case errors.Is(err, engine.ErrNotWaiting):
		s.error(w, http.StatusConflict, fmt.Errorf("not waiting"))
		return
	case err != nil:
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusOK, map[string]any{"run_id": runID})
}
