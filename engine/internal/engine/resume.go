package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// ResumeTaskArgs re-invokes a suspended task's executor (Resumable.Resume).
// Scheduled at resume_at for poll/delay; enqueued immediately on a callback hit.
// MaxAttempts 1 — like execute_task, the engine owns retries via new task rows,
// not River-level job retries.
type ResumeTaskArgs struct {
	TaskID       uuid.UUID `json:"task_id"`
	SuspensionID uuid.UUID `json:"suspension_id"`
	Reason       string    `json:"reason"` // "poll" | "callback"
}

func (ResumeTaskArgs) Kind() string { return "resume_task" }
func (ResumeTaskArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueTasks, MaxAttempts: 1}
}

// EnqueueResume schedules an immediate resume of a suspended task (the callback
// path). It only enqueues the job; the resume worker re-checks status under the
// row guard, so there is no coupled state write to share a transaction with.
func (e *Engine) EnqueueResume(ctx context.Context, taskID, suspensionID uuid.UUID, reason string) error {
	_, err := e.Client.Insert(ctx, ResumeTaskArgs{TaskID: taskID, SuspensionID: suspensionID, Reason: reason}, nil)
	return err
}

type resumeTaskWorker struct {
	river.WorkerDefaults[ResumeTaskArgs]
	e *Engine
}

func (w *resumeTaskWorker) Work(ctx context.Context, job *river.Job[ResumeTaskArgs]) error {
	t, status, step, executor, in, err := w.e.prepareTask(ctx, job.Args.TaskID)
	if err != nil {
		if t.id == uuid.Nil {
			return err
		}
		return w.e.finishTask(ctx, t, "failed", nil, err)
	}
	if status != "suspended" {
		return nil // already resumed, canceled, or finalized (job replay)
	}

	// Load the checkpoint. Gone => finalized/canceled concurrently; no-op.
	var kind string
	var token *string
	var payloadRaw []byte
	err = w.e.Pool.QueryRow(ctx, `
		select kind, resume_token, payload from suspensions where id = $1 and task_id = $2`,
		job.Args.SuspensionID, t.id).Scan(&kind, &token, &payloadRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var payload map[string]any
	if len(payloadRaw) > 0 {
		_ = json.Unmarshal(payloadRaw, &payload)
	}

	resumable, ok := executor.(steps.Resumable)
	if !ok {
		return w.e.finishTask(ctx, t, "failed", nil,
			fmt.Errorf("step type %q suspended but is not resumable", step.Type))
	}

	tokenStr := ""
	if token != nil {
		tokenStr = *token
	}
	out, execErr := resumable.Resume(ctx, in, steps.SuspensionState{
		Kind:    kind,
		Token:   tokenStr,
		Reason:  job.Args.Reason,
		Payload: payload,
	})
	var susp *steps.Suspended
	if errors.As(execErr, &susp) {
		return w.e.suspendTask(ctx, t, susp) // re-suspend (e.g. external work still running)
	}
	if execErr != nil {
		return w.e.finishTask(ctx, t, "failed", out.Data, execErr)
	}
	return w.e.finishTask(ctx, t, "succeeded", out.Data, nil)
}
