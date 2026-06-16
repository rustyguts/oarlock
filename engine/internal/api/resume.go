package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ResumeByToken resumes a callback-suspended task. The token IS the capability
// — unguessable, single-use, and workspace-scoped via the suspension row — so
// this route is intentionally unauthenticated and registered OUTSIDE WithAuth
// (the caller is an external system, e.g. a finished container, with no
// session). It only enqueues a resume; the resume worker re-checks the task
// status under the row guard, so a replayed or stale callback is a harmless
// no-op (the suspension row is deleted on finalize).
func (s *Server) ResumeByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		s.error(w, http.StatusBadRequest, fmt.Errorf("missing token"))
		return
	}
	var taskID, suspID uuid.UUID
	err := s.DB.QueryRow(r.Context(), `
		select s.task_id, s.id
		from suspensions s
		join tasks t on t.id = s.task_id
		where s.resume_token = $1 and s.kind = 'callback' and t.status = 'suspended'`, token).
		Scan(&taskID, &suspID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("no pending suspension for token"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.Engine.EnqueueResume(r.Context(), taskID, suspID, "callback"); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
