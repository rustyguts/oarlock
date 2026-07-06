package engine

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/definition"
)

const reaperInterval = 60 * time.Second

// runReaper fails tasks stuck in 'running' after their worker process died.
// The execute_task guard (status must be 'queued') makes replay a no-op and
// MaxAttempts:1 means River's rescuer discards the job, so without this the
// task never resolves and advance_run never fires again — the run hangs
// forever. Runs until ctx is canceled.
func (e *Engine) runReaper(ctx context.Context) {
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.reapOrphans(ctx); err != nil {
				e.Log.Warn("reaper sweep failed", "error", err)
			}
		}
	}
}

type orphan struct {
	id            uuid.UUID
	runID         uuid.UUID
	workspaceID   uuid.UUID
	stepKey       string
	attempt       int
	definitionRaw []byte
}

func (e *Engine) reapOrphans(ctx context.Context) error {
	// 20min exceeds the 15min execute_task ceiling, so only tasks whose worker
	// is genuinely gone are reaped, never live long-running ones.
	rows, err := e.Pool.Query(ctx, `
		select t.id, t.run_id, t.workspace_id, t.step_key, t.attempt, v.definition
		from tasks t
		join runs r on r.id = t.run_id
		join workflow_versions v on v.id = r.workflow_version_id
		where t.status = 'running' and t.started_at < now() - interval '20 minutes'`)
	if err != nil {
		return err
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.runID, &o.workspaceID, &o.stepKey, &o.attempt, &o.definitionRaw); err != nil {
			rows.Close()
			return err
		}
		orphans = append(orphans, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, o := range orphans {
		if err := e.reapTask(ctx, o); err != nil {
			e.Log.Warn("reap task failed", "task_id", o.id, "run_id", o.runID, "error", err)
		}
	}
	return nil
}

// reapTask fails one orphaned task and, mirroring finishTask, inserts the next
// attempt with backoff when the step has retries left — a worker killed mid-
// task is exactly the transient failure retries exist for. Task update, next
// attempt, and job inserts share one transaction (hard rule 2). The status
// guard makes concurrent replicas safe: only the sweep that flips
// 'running'→'failed' proceeds.
func (e *Engine) reapTask(ctx context.Context, o orphan) error {
	retries := 0
	if def, err := definition.Parse(o.definitionRaw); err == nil {
		if step := def.Step(o.stepKey); step != nil {
			retries = step.Retries
		}
	}

	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	errJSON := marshalJSON(map[string]string{"message": "task orphaned: worker exited or timed out"})
	tag, err := tx.Exec(ctx, `
		update tasks set status = 'failed', error = $2, finished_at = now()
		where id = $1 and status = 'running'`, o.id, errJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // another replica reaped it, or it finished underneath us
	}

	retrying := o.attempt <= retries
	if retrying {
		var nextID uuid.UUID
		err := tx.QueryRow(ctx, `
			insert into tasks (run_id, workspace_id, step_key, attempt, status)
			values ($1, $2, $3, $4, 'queued') returning id`,
			o.runID, o.workspaceID, o.stepKey, o.attempt+1).Scan(&nextID)
		if err != nil {
			return err
		}
		backoff := time.Duration(1<<o.attempt) * time.Second // 2s, 4s, 8s, …
		if _, err := e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: nextID},
			&river.InsertOpts{ScheduledAt: time.Now().Add(backoff)}); err != nil {
			return err
		}
	}
	if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: o.runID}, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.notify(ctx, o.runID)
	e.Log.Warn("task reaped", "task_id", o.id, "run_id", o.runID, "step", o.stepKey,
		"attempt", o.attempt, "will_retry", retrying)
	return nil
}
