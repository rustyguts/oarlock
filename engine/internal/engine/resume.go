package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Errors surfaced by ResumeSuspendedTask so the callback endpoint can map them
// to HTTP status codes without leaking internals.
var (
	// ErrSuspensionNotFound is an unknown or already-consumed resume token → 404.
	ErrSuspensionNotFound = errors.New("suspension not found")
	// ErrNotWaiting is a token whose task already left 'suspended' → 409.
	ErrNotWaiting = errors.New("not waiting")
)

type ResumeTaskArgs struct {
	TaskID uuid.UUID `json:"task_id"`
}

func (ResumeTaskArgs) Kind() string { return "resume_task" }
func (ResumeTaskArgs) InsertOpts() river.InsertOpts {
	// Control queue like advance_run. Unlike execute_task we do NOT cap
	// MaxAttempts: a resume that fails on infrastructure (a DB blip) must retry,
	// and resumeSuspended is idempotent (guarded transition), so replay is safe.
	return river.InsertOpts{Queue: QueueControl}
}

// resumeTaskWorker fires when a scheduled (delay-kind) suspension comes due. It
// reads the stored resume output from the suspension payload and succeeds the
// task through resumeSuspended. A missing suspension means the task was already
// resumed or canceled — a clean no-op.
type resumeTaskWorker struct {
	river.WorkerDefaults[ResumeTaskArgs]
	e *Engine
}

func (w *resumeTaskWorker) Work(ctx context.Context, job *river.Job[ResumeTaskArgs]) error {
	taskID := job.Args.TaskID

	var payload []byte
	err := w.e.Pool.QueryRow(ctx,
		`select payload from suspensions where task_id = $1`, taskID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already resumed/canceled: nothing to do
	}
	if err != nil {
		return err
	}

	// The payload was redacted when the suspension was written, so no redactor is
	// needed here (and a delay output holds no secrets anyway).
	var output any
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &output)
	}
	runID, resumed, err := w.e.resumeSuspended(ctx, taskID, output, nil)
	if err != nil {
		return err
	}
	w.e.Log.Info("task resumed", "task_id", taskID, "run_id", runID, "kind", "delay", "resumed", resumed)
	return nil
}

// resumeSuspended flips one suspended task to succeeded with the given output,
// deletes its suspension row, and enqueues advance_run — all one transaction
// (hard rule 2). The status guard makes a concurrent cancel win: RowsAffected 0
// leaves the task alone (resumed=false) but still clears the now-stale
// suspension row so it can't linger. Notifies after commit.
func (e *Engine) resumeSuspended(ctx context.Context, taskID uuid.UUID, output any, redact *redactor) (uuid.UUID, bool, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `select run_id from tasks where id = $1`, taskID).Scan(&runID); err != nil {
		return uuid.Nil, false, err // ErrNoRows if the task vanished
	}

	tag, err := tx.Exec(ctx, `
		update tasks set status = 'succeeded', output = $2, finished_at = now()
		where id = $1 and status = 'suspended'`, taskID, redact.JSON(marshalJSON(output)))
	if err != nil {
		return uuid.Nil, false, err
	}
	resumed := tag.RowsAffected() > 0

	if _, err := tx.Exec(ctx, `delete from suspensions where task_id = $1`, taskID); err != nil {
		return uuid.Nil, false, err
	}
	if resumed {
		if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
			return uuid.Nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	e.notify(ctx, runID)
	return runID, resumed, nil
}

// ResumeSuspendedTask completes a callback-kind suspension identified by the
// sha256 of its resume token (hashedToken). It succeeds the suspended task with
// {"resumed": true, "payload": payload}, redacted with the workspace's secrets,
// then deletes the suspension and enqueues advance_run in one transaction. The
// payload is arbitrary external caller input; redaction is belt-and-suspenders.
// Returns ErrSuspensionNotFound for an unknown/consumed token and ErrNotWaiting
// when the task already left 'suspended' (canceled or already resumed).
func (e *Engine) ResumeSuspendedTask(ctx context.Context, hashedToken string, payload any) (uuid.UUID, error) {
	var taskID, workspaceID uuid.UUID
	err := e.Pool.QueryRow(ctx,
		`select task_id, workspace_id from suspensions where resume_token = $1`,
		hashedToken).Scan(&taskID, &workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrSuspensionNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}

	var redact *redactor
	if e.Secrets != nil {
		secrets, err := e.Secrets.WorkspaceSecrets(ctx, workspaceID)
		if err != nil {
			return uuid.Nil, err
		}
		redact = newRedactor(secrets)
	}

	output := map[string]any{"resumed": true, "payload": payload}
	runID, resumed, err := e.resumeSuspended(ctx, taskID, output, redact)
	if err != nil {
		return uuid.Nil, err
	}
	if !resumed {
		return uuid.Nil, ErrNotWaiting
	}
	return runID, nil
}

// generateResumeToken mints a resume credential. The raw token is the bearer
// secret (it appears only in the callback task's output / resume URL); the
// database stores only its sha256, mirroring the session-token precedent
// (api/auth.go hashToken), so a leak of the suspensions table can't be replayed.
func generateResumeToken() (raw, hashed string, err error) {
	b := make([]byte, 24) // 48 hex chars
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = "rsm_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}
