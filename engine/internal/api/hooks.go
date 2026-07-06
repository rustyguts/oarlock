package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rustyguts/oarlock/engine/internal/engine"
)

// engineRunOpts builds RunOpts for a webhook fire: the trigger id for
// provenance and, when the caller supplied X-Idempotency-Key, a "hook:"-prefixed
// key so webhook dedup can't collide with cron keys ("cron:...").
func engineRunOpts(triggerID uuid.UUID, idempotencyKey string) engine.RunOpts {
	opts := engine.RunOpts{TriggerID: &triggerID}
	if idempotencyKey != "" {
		opts.IdempotencyKey = "hook:" + idempotencyKey
	}
	return opts
}

// Webhook ingress: POST /hooks/{ws}/{path}. This endpoint is UNAUTHENTICATED —
// it's mounted outside the session-auth wrapper (main.go) so external callers
// can fire runs. Security comes from three places: the workspace slug + path
// must resolve to an enabled webhook trigger (unknown → generic 404, so paths
// can't be probed), an optional HMAC secret verifies the sender, and an
// optional idempotency key dedupes retries.

// verifyHMAC reports whether signature is a valid hex HMAC-SHA256 of body under
// secret. Comparison is constant-time (hmac.Equal). An empty/malformed
// signature simply fails to match.
func verifyHMAC(secret string, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// WebhookHandler exposes the unauthenticated webhook ingress handler for
// mounting in main.go outside the session-auth wrapper.
func (s *Server) WebhookHandler() http.Handler { return http.HandlerFunc(s.webhook) }

// webhook resolves the trigger from {ws}/{path}, verifies the request, and
// starts a run. Because it's unauthenticated it does NOT use s.workspace(r);
// the workspace is resolved from the URL slug.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("ws")
	path := r.PathValue("path")

	// Read the raw body first — HMAC verification needs the exact bytes. The
	// MaxBody wrapper bounds this read.
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("could not read body"))
		return
	}

	var (
		workspaceID uuid.UUID
		triggerID   uuid.UUID
		workflowID  uuid.UUID
		trigEnabled bool
		wfEnabled   bool
		hasVersion  bool
		secret      *string
	)
	// Resolve workspace + webhook trigger + workflow enablement in one query.
	// Any miss (bad slug, unknown path, wrong workspace) collapses to ErrNoRows
	// → a single generic 404 that reveals nothing about which part failed.
	err = s.DB.QueryRow(r.Context(), `
		select ws.id, t.id, t.workflow_id, t.is_enabled, t.config->>'secret',
		       wf.is_enabled, wf.current_version_id is not null
		from workspaces ws
		join triggers t on t.workspace_id = ws.id
		join workflows wf on wf.id = t.workflow_id
		where ws.slug = $1 and t.type = 'webhook' and t.config->>'path' = $2`,
		slug, path).Scan(&workspaceID, &triggerID, &workflowID, &trigEnabled, &secret, &wfEnabled, &hasVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}

	if !trigEnabled || !wfEnabled || !hasVersion {
		s.error(w, http.StatusForbidden, fmt.Errorf("webhook is disabled"))
		return
	}

	if secret != nil && *secret != "" {
		if !verifyHMAC(*secret, raw, r.Header.Get("X-Oarlock-Signature")) {
			s.error(w, http.StatusForbidden, fmt.Errorf("invalid signature"))
			return
		}
	}

	// Body becomes parsed JSON when it parses, else the raw string. Query params
	// collapse to their first value each.
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		body = string(raw)
	}
	query := map[string]any{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}
	input := map[string]any{"body": body, "query": query}

	opts := engineRunOpts(triggerID, r.Header.Get("X-Idempotency-Key"))
	runID, created, err := s.Engine.StartRunOpts(r.Context(), workspaceID, workflowID, input, opts)
	if errors.Is(err, pgx.ErrNoRows) {
		// current_version_id vanished between the check and the insert.
		s.error(w, http.StatusForbidden, fmt.Errorf("webhook is disabled"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	// 202 for a freshly-created run, 200 when an idempotency key replayed one.
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	s.json(w, status, map[string]any{"run_id": runID})
}
