package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/definition"
)

// advanceRunWorker recomputes run state from rows and enqueues ready steps.
// Idempotent: it derives everything from the database, so duplicate or
// concurrent advances converge (the runs row is locked for the transaction).
type advanceRunWorker struct {
	river.WorkerDefaults[AdvanceRunArgs]
	e *Engine
}

func (w *advanceRunWorker) Work(ctx context.Context, job *river.Job[AdvanceRunArgs]) error {
	runID := job.Args.RunID
	tx, err := w.e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var (
		workspaceID   uuid.UUID
		status        string
		definitionRaw []byte
	)
	err = tx.QueryRow(ctx, `
		select r.workspace_id, r.status, v.definition
		from runs r join workflow_versions v on v.id = r.workflow_version_id
		where r.id = $1
		for update of r`, runID).Scan(&workspaceID, &status, &definitionRaw)
	if err != nil {
		return fmt.Errorf("load run %s: %w", runID, err)
	}

	switch status {
	case "succeeded", "failed", "canceled":
		return nil // terminal; nothing to advance
	}

	def, err := definition.Parse(definitionRaw)
	if err != nil {
		return err
	}

	// Latest attempt status per step.
	stepStatus := map[string]string{}
	rows, err := tx.Query(ctx, `
		select distinct on (step_key) step_key, status::text
		from tasks where run_id = $1
		order by step_key, attempt desc`, runID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, st string
		if err := rows.Scan(&key, &st); err != nil {
			rows.Close()
			return err
		}
		stepStatus[key] = st
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	anyFailed := false
	allSucceeded := true
	var ready []string
	for _, step := range def.Steps {
		st, exists := stepStatus[step.Key]
		if exists {
			switch st {
			case "failed", "canceled":
				anyFailed = true
				allSucceeded = false
			case "succeeded", "skipped":
				// done
			default:
				allSucceeded = false // queued/running/suspended
			}
			continue
		}
		allSucceeded = false
		depsOK := true
		for _, n := range step.Needs {
			if stepStatus[n] != "succeeded" && stepStatus[n] != "skipped" {
				depsOK = false
				break
			}
		}
		if depsOK && !anyFailed {
			ready = append(ready, step.Key)
		}
	}

	switch {
	case anyFailed:
		_, err = tx.Exec(ctx, `
			update runs set status = 'failed', finished_at = coalesce(finished_at, now())
			where id = $1 and status not in ('failed','canceled')`, runID)
		if err != nil {
			return err
		}
	case allSucceeded:
		_, err = tx.Exec(ctx, `
			update runs set status = 'succeeded', finished_at = now()
			where id = $1`, runID)
		if err != nil {
			return err
		}
	default:
		// Insert tasks + jobs for ready steps, one transaction (hard rule 2).
		for _, key := range ready {
			var taskID uuid.UUID
			err = tx.QueryRow(ctx, `
				insert into tasks (run_id, workspace_id, step_key, attempt, status)
				values ($1, $2, $3, 1, 'queued')
				on conflict (run_id, step_key, attempt) do nothing
				returning id`, runID, workspaceID, key).Scan(&taskID)
			if err != nil {
				continue // conflict: another advance already inserted it
			}
			if _, err := w.e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: taskID}, nil); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			update runs set status = 'running', started_at = coalesce(started_at, now())
			where id = $1 and status = 'queued'`, runID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.e.notify(ctx, runID)
	if len(ready) > 0 {
		w.e.Log.Info("run advanced", "run_id", runID, "enqueued", ready)
	}
	return nil
}

// marshalJSON is a small helper that never fails the caller: marshal errors
// surface as a JSON string so task rows always get written.
func marshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"marshal_error": err.Error()})
	}
	return b
}
