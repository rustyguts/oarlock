package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	// A succeeded condition's chosen branch, so computePlan can prune the
	// untaken side. Skipped conditions are discovered by computePlan itself, so
	// only succeeded ones are loaded here.
	branchChoice, err := loadBranchChoice(ctx, tx, runID, def)
	if err != nil {
		return err
	}

	plan := computePlan(def, stepStatus, branchChoice)

	if plan.anyFailed {
		// Stop enqueuing; already-running siblings finish and their late results
		// are dropped by the status guard (v0 has no task interruption).
		if _, err = tx.Exec(ctx, `
			update runs set status = 'failed', finished_at = coalesce(finished_at, now())
			where id = $1 and status not in ('failed','canceled')`, runID); err != nil {
			return err
		}
	} else {
		// Untaken branches get skipped rows so the run can terminate (a run only
		// succeeds once every step has a succeeded/skipped row). Written even on
		// the all-succeeded path so the rows and the terminal status commit
		// together. Idempotent: unique (run_id, step_key, attempt) + on-conflict.
		for _, key := range plan.skip {
			if _, err := tx.Exec(ctx, `
				insert into tasks (run_id, workspace_id, step_key, attempt, status, finished_at)
				values ($1, $2, $3, 1, 'skipped', now())
				on conflict (run_id, step_key, attempt) do nothing`, runID, workspaceID, key); err != nil {
				return err
			}
		}
		if plan.allSucceeded {
			if _, err = tx.Exec(ctx, `
				update runs set status = 'succeeded', finished_at = now()
				where id = $1`, runID); err != nil {
				return err
			}
		} else {
			// Insert tasks + jobs for ready steps, one transaction (hard rule 2).
			for _, key := range plan.ready {
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
			// Belt-and-suspenders: if this pass only wrote skips (nothing ready to
			// carry the run forward), re-advance so a freshly-unblocked join is
			// picked up. computePlan resolves the full skip set in one pass, so
			// this is rarely needed, and it can't loop — next pass the skipped
			// steps have rows and drop out of plan.skip.
			if len(plan.skip) > 0 && len(plan.ready) == 0 {
				if _, err := w.e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
					return err
				}
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.e.notify(ctx, runID)
	if len(plan.ready) > 0 || len(plan.skip) > 0 {
		w.e.Log.Info("run advanced", "run_id", runID, "enqueued", plan.ready, "skipped", plan.skip)
	}
	return nil
}

// loadBranchChoice reads each succeeded condition's decided branch from its
// persisted output: the `result` boolean is the source of truth, with the
// `branch` label as a fallback. A condition whose decision can't be read (e.g.
// the pathological case of a workspace secret value literally equal to the
// stored token, which redaction would scrub) is simply left undecided — then
// computePlan prunes nothing for it and both branches run, rather than the run
// hanging.
func loadBranchChoice(ctx context.Context, tx pgx.Tx, runID uuid.UUID, def *definition.Definition) (map[string]string, error) {
	var condKeys []string
	for _, s := range def.Steps {
		if s.Type == definition.ConditionType {
			condKeys = append(condKeys, s.Key)
		}
	}
	choice := map[string]string{}
	if len(condKeys) == 0 {
		return choice, nil
	}
	rows, err := tx.Query(ctx, `
		select distinct on (step_key) step_key, output
		from tasks
		where run_id = $1 and step_key = any($2) and status = 'succeeded'
		order by step_key, attempt desc`, runID, condKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var out struct {
			Result *bool  `json:"result"`
			Branch string `json:"branch"`
		}
		_ = json.Unmarshal(raw, &out)
		switch {
		case out.Result != nil:
			if *out.Result {
				choice[key] = "then"
			} else {
				choice[key] = "else"
			}
		case out.Branch == "then" || out.Branch == "else":
			choice[key] = out.Branch
		}
	}
	return choice, rows.Err()
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
