// Package engine implements the event-driven state machine (docs/project.md,
// "Engine: event-driven state machine").
// No process owns a run: truth lives in Postgres rows, workers are stateless,
// and job insert + state write always share a transaction (hard rules 1–2).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

const (
	QueueControl = "control"
	QueueTasks   = "tasks"
)

type AdvanceRunArgs struct {
	RunID uuid.UUID `json:"run_id"`
}

func (AdvanceRunArgs) Kind() string { return "advance_run" }
func (AdvanceRunArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueControl}
}

type ExecuteTaskArgs struct {
	TaskID uuid.UUID `json:"task_id"`
}

func (ExecuteTaskArgs) Kind() string { return "execute_task" }
func (ExecuteTaskArgs) InsertOpts() river.InsertOpts {
	// v0: a failed task marks the run failed; retries arrive as new task
	// attempts in a later iteration, so don't let River retry the job blindly.
	return river.InsertOpts{Queue: QueueTasks, MaxAttempts: 1}
}

type Engine struct {
	Pool     *pgxpool.Pool
	Client   *river.Client[pgx.Tx]
	Registry *steps.Registry
	Cache    *redis.Client
	Secrets  steps.SecretSource
	Log      *slog.Logger
}

// RunChannel is the Valkey pub/sub channel for a run's change pings.
func RunChannel(runID uuid.UUID) string { return "run:" + runID.String() }

// notify publishes a fire-and-forget change ping for live UI updates.
// Postgres remains the source of truth; subscribers refetch on ping.
func (e *Engine) notify(ctx context.Context, runID uuid.UUID) {
	if e.Cache == nil {
		return
	}
	if err := e.Cache.Publish(ctx, RunChannel(runID), "changed").Err(); err != nil {
		e.Log.Debug("run notify failed", "run_id", runID, "error", err)
	}
}

func New(ctx context.Context, pool *pgxpool.Pool, registry *steps.Registry, cache *redis.Client, secrets steps.SecretSource, log *slog.Logger) (*Engine, error) {
	driver := riverpgxv5.New(pool)

	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, err
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, err
	}

	e := &Engine{Pool: pool, Registry: registry, Cache: cache, Secrets: secrets, Log: log}

	workers := river.NewWorkers()
	river.AddWorker(workers, &advanceRunWorker{e: e})
	river.AddWorker(workers, &executeTaskWorker{e: e})
	river.AddWorker(workers, &resumeTaskWorker{e: e})

	client, err := river.NewClient(driver, &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueControl: {MaxWorkers: 10},
			QueueTasks:   {MaxWorkers: 50},
		},
		Workers: workers,
		Logger:  log,
	})
	if err != nil {
		return nil, err
	}
	e.Client = client
	return e, nil
}

func (e *Engine) Start(ctx context.Context) error {
	if err := e.Client.Start(ctx); err != nil {
		return err
	}
	go e.runReaper(ctx)    // exits on ctx cancellation (same lifetime as the client)
	go e.runScheduler(ctx) // fires schedule triggers; exits on ctx cancellation
	return nil
}
func (e *Engine) Stop(ctx context.Context) error { return e.Client.Stop(ctx) }

// RunOpts carries optional provenance/dedup fields for StartRunOpts. The zero
// value reproduces the old StartRun behavior (untriggered, non-idempotent run).
type RunOpts struct {
	// TriggerID records which trigger fired this run (nil for manual API runs).
	TriggerID *uuid.UUID
	// IdempotencyKey, when non-empty, dedupes runs of THIS workflow: a second
	// start with the same key returns the existing run (created=false). The key
	// is scoped to the workflow before storage (StartRunOpts), so the same
	// caller key on a different workflow starts its own run.
	IdempotencyKey string
}

// StartRun creates a run for the workflow's current version and enqueues the
// first advance_run — run row and job in one transaction.
func (e *Engine) StartRun(ctx context.Context, workspaceID, workflowID uuid.UUID, input map[string]any) (uuid.UUID, error) {
	runID, _, err := e.StartRunOpts(ctx, workspaceID, workflowID, input, RunOpts{})
	return runID, err
}

// StartRunOpts creates a run for the workflow's current version and enqueues the
// first advance_run — run row and job in one transaction (hard rule 2). When
// opts.IdempotencyKey is set it inserts on-conflict-do-nothing against the
// (workspace_id, idempotency_key) unique index: on a fresh key created=true and
// the advance job is enqueued; on a replay created=false and the existing run's
// id is returned with no new job. This makes multi-replica trigger firing safe
// with zero locks — a lost race is a silent no-op.
func (e *Engine) StartRunOpts(ctx context.Context, workspaceID, workflowID uuid.UUID, input map[string]any, opts RunOpts) (uuid.UUID, bool, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	var versionID uuid.UUID
	err = tx.QueryRow(ctx, `
		select current_version_id from workflows
		where id = $1 and workspace_id = $2 and current_version_id is not null`,
		workflowID, workspaceID).Scan(&versionID)
	if err != nil {
		return uuid.Nil, false, err
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, false, err
	}

	var key *string
	if opts.IdempotencyKey != "" {
		// Scope the stored key to the workflow. The unique index is
		// (workspace_id, idempotency_key), so without this a caller key reused
		// across two workflows in one workspace would let the second start
		// silently return the first's run. Triggered runs additionally
		// namespace by trigger in their key prefix (api hooks / scheduler).
		scoped := workflowID.String() + ":" + opts.IdempotencyKey
		key = &scoped
	}

	var runID uuid.UUID
	if key != nil {
		// on-conflict-do-nothing returns no row when the key already exists;
		// that's the replay path — return the existing run and skip the job.
		err = tx.QueryRow(ctx, `
			insert into runs (workspace_id, workflow_id, workflow_version_id, status, input, trigger_id, idempotency_key)
			values ($1, $2, $3, 'queued', $4, $5, $6)
			on conflict (workspace_id, idempotency_key) do nothing
			returning id`,
			workspaceID, workflowID, versionID, inputJSON, opts.TriggerID, key).Scan(&runID)
		if errors.Is(err, pgx.ErrNoRows) {
			// The scoped key embeds workflowID, so a match is same-workflow by
			// construction; assert it so the invariant holds even if a future
			// caller bypasses scoping.
			var existing, existingWorkflow uuid.UUID
			if err := tx.QueryRow(ctx, `
				select id, workflow_id from runs where workspace_id = $1 and idempotency_key = $2`,
				workspaceID, *key).Scan(&existing, &existingWorkflow); err != nil {
				return uuid.Nil, false, err
			}
			if existingWorkflow != workflowID {
				return uuid.Nil, false, fmt.Errorf("idempotency key already used by another workflow")
			}
			if err := tx.Commit(ctx); err != nil {
				return uuid.Nil, false, err
			}
			return existing, false, nil
		}
		if err != nil {
			return uuid.Nil, false, err
		}
	} else {
		err = tx.QueryRow(ctx, `
			insert into runs (workspace_id, workflow_id, workflow_version_id, status, input, trigger_id)
			values ($1, $2, $3, 'queued', $4, $5)
			returning id`,
			workspaceID, workflowID, versionID, inputJSON, opts.TriggerID).Scan(&runID)
		if err != nil {
			return uuid.Nil, false, err
		}
	}

	if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
		return uuid.Nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	e.notify(ctx, runID)
	return runID, true, nil
}

// CancelRun marks a non-terminal run and its pending tasks canceled. An
// in-flight executor is not interrupted (v0); its result is discarded by the
// status guard in finishTask.
func (e *Engine) CancelRun(ctx context.Context, workspaceID, runID uuid.UUID) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `
		select status::text from runs
		where id = $1 and workspace_id = $2 for update`, runID, workspaceID).Scan(&status)
	if err != nil {
		return err
	}
	switch status {
	case "succeeded", "failed", "canceled":
		return fmt.Errorf("run is already %s", status)
	}
	if _, err := tx.Exec(ctx, `
		update runs set status = 'canceled', finished_at = now() where id = $1`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update tasks set status = 'canceled', finished_at = now()
		where run_id = $1 and status in ('queued','running','suspended')`, runID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.notify(ctx, runID)
	e.Log.Info("run canceled", "run_id", runID)
	return nil
}

// RetryRun re-attempts a failed or canceled run: every step whose latest
// attempt is failed/canceled gets a fresh task attempt; completed steps keep
// their outputs. Task inserts and job enqueues share one transaction.
func (e *Engine) RetryRun(ctx context.Context, workspaceID, runID uuid.UUID) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx, `
		select status::text from runs
		where id = $1 and workspace_id = $2 for update`, runID, workspaceID).Scan(&status)
	if err != nil {
		return err
	}
	if status != "failed" && status != "canceled" {
		return fmt.Errorf("only failed or canceled runs can be retried (run is %s)", status)
	}

	rows, err := tx.Query(ctx, `
		select distinct on (step_key) step_key, attempt, status::text
		from tasks where run_id = $1
		order by step_key, attempt desc`, runID)
	if err != nil {
		return err
	}
	type redo struct {
		key     string
		attempt int
	}
	var redos []redo
	for rows.Next() {
		var r redo
		var st string
		if err := rows.Scan(&r.key, &r.attempt, &st); err != nil {
			rows.Close()
			return err
		}
		if st == "failed" || st == "canceled" {
			redos = append(redos, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		update runs set status = 'running', finished_at = null, error = null
		where id = $1`, runID); err != nil {
		return err
	}
	for _, r := range redos {
		var taskID uuid.UUID
		err := tx.QueryRow(ctx, `
			insert into tasks (run_id, workspace_id, step_key, attempt, status)
			values ($1, $2, $3, $4, 'queued') returning id`,
			runID, workspaceID, r.key, r.attempt+1).Scan(&taskID)
		if err != nil {
			return err
		}
		if _, err := e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: taskID}, nil); err != nil {
			return err
		}
	}
	if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.notify(ctx, runID)
	e.Log.Info("run retried", "run_id", runID, "steps", len(redos))
	return nil
}
