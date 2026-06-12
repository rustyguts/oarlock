// Package engine implements the event-driven state machine (design §4.1).
// No process owns a run: truth lives in Postgres rows, workers are stateless,
// and job insert + state write always share a transaction (hard rules 1–2).
package engine

import (
	"context"
	"encoding/json"
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

func (e *Engine) Start(ctx context.Context) error { return e.Client.Start(ctx) }
func (e *Engine) Stop(ctx context.Context) error  { return e.Client.Stop(ctx) }

// StartRun creates a run for the workflow's current version and enqueues the
// first advance_run — run row and job in one transaction.
func (e *Engine) StartRun(ctx context.Context, workspaceID, workflowID uuid.UUID, input map[string]any) (uuid.UUID, error) {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var versionID uuid.UUID
	err = tx.QueryRow(ctx, `
		select current_version_id from workflows
		where id = $1 and workspace_id = $2 and current_version_id is not null`,
		workflowID, workspaceID).Scan(&versionID)
	if err != nil {
		return uuid.Nil, err
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return uuid.Nil, err
	}

	var runID uuid.UUID
	err = tx.QueryRow(ctx, `
		insert into runs (workspace_id, workflow_id, workflow_version_id, status, input)
		values ($1, $2, $3, 'queued', $4)
		returning id`,
		workspaceID, workflowID, versionID, inputJSON).Scan(&runID)
	if err != nil {
		return uuid.Nil, err
	}

	if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: runID}, nil); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	e.notify(ctx, runID)
	return runID, nil
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
