package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/definition"
	"github.com/rustyguts/oarlock/engine/internal/steps"
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
		runInputRaw   []byte // for `if` guard evaluation (input + upstream outputs)
		definitionRaw []byte
	)
	err = tx.QueryRow(ctx, `
		select r.workspace_id, r.status, r.input, v.definition
		from runs r join workflow_versions v on v.id = r.workflow_version_id
		where r.id = $1
		for update of r`, runID).Scan(&workspaceID, &status, &runInputRaw, &definitionRaw)
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
			// A skipped dependency satisfies needs like a succeeded one; a
			// skipped step produces no output, so steps.<n> is simply absent
			// (undefined) in this step's expression/guard context.
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
		// A step with an `if` guard is resolved here before enqueueing: a falsy
		// result writes a terminal 'skipped' row and an eval error a 'failed'
		// row, neither with an execute_task job.
		guardResolved := false
		for _, key := range ready {
			step := def.Step(key)
			if step.If != "" {
				skip, guardErr, err := w.evalGuard(ctx, def, runID, runInputRaw, step)
				if err != nil {
					return err
				}
				if guardErr != nil {
					// Eval error: fail the guard row terminally; the run fails
					// through normal advancement on the re-advance below.
					tag, err := tx.Exec(ctx, `
						insert into tasks (run_id, workspace_id, step_key, attempt, status, error, finished_at)
						values ($1, $2, $3, 1, 'failed', $4, now())
						on conflict (run_id, step_key, attempt) do nothing`,
						runID, workspaceID, key,
						marshalJSON(map[string]string{"message": "if condition: " + guardErr.Error()}))
					if err != nil {
						return err
					}
					guardResolved = guardResolved || tag.RowsAffected() > 0
					continue
				}
				if skip {
					tag, err := tx.Exec(ctx, `
						insert into tasks (run_id, workspace_id, step_key, attempt, status, finished_at)
						values ($1, $2, $3, 1, 'skipped', now())
						on conflict (run_id, step_key, attempt) do nothing`,
						runID, workspaceID, key)
					if err != nil {
						return err
					}
					guardResolved = guardResolved || tag.RowsAffected() > 0
					continue
				}
				// Truthy → fall through to the normal queued insert.
			}
			var taskID uuid.UUID
			err = tx.QueryRow(ctx, `
				insert into tasks (run_id, workspace_id, step_key, attempt, status)
				values ($1, $2, $3, 1, 'queued')
				on conflict (run_id, step_key, attempt) do nothing
				returning id`, runID, workspaceID, key).Scan(&taskID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue // conflict: another advance already inserted it
				}
				return err
			}
			if _, err := w.e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: taskID}, nil); err != nil {
				return err
			}
		}
		if guardResolved {
			// Skipped/failed guard rows carry no execute_task job, so nothing
			// else would re-advance the run. Enqueue one advance (idempotent)
			// to pick up newly-satisfied dependents and terminal state.
			if _, err := w.e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
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

// evalGuard evaluates a step's `if` expression against run input + the outputs
// of its transitive needs. Secrets are deliberately not bound: a condition must
// not require decrypting the vault on the control queue. It returns skip=true
// for a falsy guard, or a non-nil guardErr when the expression itself errors (a
// nil guardErr with skip=false means run the step). Wrapping in !!( ) lets goja
// compute JS truthiness and hand it back as a Go bool.
func (w *advanceRunWorker) evalGuard(ctx context.Context, def *definition.Definition, runID uuid.UUID, runInputRaw []byte, step *definition.Step) (skip bool, guardErr, err error) {
	allowed := make([]string, 0)
	for k := range def.TransitiveNeeds(step.Key) {
		allowed = append(allowed, k)
	}
	condContext, err := buildRunContext(ctx, w.e.Pool, runID, runInputRaw, allowed)
	if err != nil {
		return false, nil, err
	}
	val, guardErr := steps.EvalExpression(ctx, "!!("+step.If+")", condContext)
	if guardErr != nil {
		return false, guardErr, nil
	}
	truthy, _ := val.(bool)
	return !truthy, nil, nil
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
