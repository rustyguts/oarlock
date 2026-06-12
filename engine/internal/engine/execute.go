package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/definition"
	"github.com/rustyguts/oarlock/engine/internal/steps"
)

type executeTaskWorker struct {
	river.WorkerDefaults[ExecuteTaskArgs]
	e *Engine
}

func (w *executeTaskWorker) Work(ctx context.Context, job *river.Job[ExecuteTaskArgs]) error {
	taskID := job.Args.TaskID

	var (
		runID         uuid.UUID
		workspaceID   uuid.UUID
		stepKey       string
		attempt       int
		taskStatus    string
		runInputRaw   []byte
		definitionRaw []byte
	)
	err := w.e.Pool.QueryRow(ctx, `
		select t.run_id, t.workspace_id, t.step_key, t.attempt, t.status::text, r.input, v.definition
		from tasks t
		join runs r on r.id = t.run_id
		join workflow_versions v on v.id = r.workflow_version_id
		where t.id = $1`, taskID).Scan(
		&runID, &workspaceID, &stepKey, &attempt, &taskStatus, &runInputRaw, &definitionRaw)
	if err != nil {
		return fmt.Errorf("load task %s: %w", taskID, err)
	}
	if taskStatus != "queued" {
		return nil // already handled (job replay) or canceled
	}

	t := taskRef{id: taskID, runID: runID, workspaceID: workspaceID, stepKey: stepKey, attempt: attempt}

	// Workspace secrets: bound as expression context, and their values are
	// scrubbed from everything this task persists or logs.
	var secrets map[string]string
	if w.e.Secrets != nil {
		secrets, err = w.e.Secrets.WorkspaceSecrets(ctx, workspaceID)
		if err != nil {
			return w.finishTask(ctx, t, "failed", nil, fmt.Errorf("load secrets: %w", err))
		}
	}
	t.redact = newRedactor(secrets)

	def, err := definition.Parse(definitionRaw)
	if err != nil {
		return w.finishTask(ctx, t, "failed", nil, err)
	}
	step := def.Step(stepKey)
	if step == nil {
		return w.finishTask(ctx, t, "failed", nil, fmt.Errorf("step %q not in definition", stepKey))
	}
	t.retries = step.Retries
	executor, ok := w.e.Registry.Get(step.Type)
	if !ok {
		return w.finishTask(ctx, t, "failed", nil, fmt.Errorf("unknown step type %q", step.Type))
	}

	// Assemble the frozen context: run input + succeeded dependency outputs
	// + workspace secrets ({{secrets.<name>}}).
	runContext, err := w.buildContext(ctx, runID, runInputRaw)
	if err != nil {
		return w.finishTask(ctx, t, "failed", nil, err)
	}
	secretsAny := map[string]any{}
	for k, v := range secrets {
		secretsAny[k] = v
	}
	runContext["secrets"] = secretsAny

	config, err := interpolateConfig(ctx, step.Config, runContext)
	if err != nil {
		return w.finishTask(ctx, t, "failed", nil, fmt.Errorf("interpolation: %w", err))
	}

	// Guarded transition: a concurrent cancel wins, and this job drops out.
	// Persisted input is redacted — secrets interpolated into config must
	// never land in task rows.
	tag, err := w.e.Pool.Exec(ctx, `
		update tasks set status = 'running', started_at = now(), input = $2
		where id = $1 and status = 'queued'`, taskID, t.redact.JSON(marshalJSON(config)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	w.e.notify(ctx, runID)

	// Every task logs to task_logs by default — lifecycle lines from the
	// engine plus whatever the executor writes through TaskInput.Log.
	taskLog := w.e.taskLogger(t)
	taskLog.Info("task started", "type", step.Type)

	out, execErr := executor.Execute(ctx, steps.TaskInput{
		WorkspaceID: workspaceID,
		RunID:       runID,
		TaskID:      taskID,
		StepKey:     stepKey,
		Config:      config,
		Context:     runContext,
		Log:         taskLog,
	})
	if execErr != nil {
		return w.finishTask(ctx, t, "failed", out.Data, execErr)
	}
	return w.finishTask(ctx, t, "succeeded", out.Data, nil)
}

type taskRef struct {
	id          uuid.UUID
	runID       uuid.UUID
	workspaceID uuid.UUID
	stepKey     string
	attempt     int
	retries     int
	redact      *redactor // nil-safe; scrubs secrets from rows + logs
}

// finishTask persists the terminal task state and enqueues advance_run in the
// same transaction. A failure with attempts left also inserts the next task
// attempt, scheduled with exponential backoff. Worker errors are absorbed
// into task state — the job itself only fails on infrastructure errors.
// The status guard makes a concurrent cancel win over an in-flight result.
func (w *executeTaskWorker) finishTask(ctx context.Context, t taskRef, status string, output any, taskErr error) error {
	tx, err := w.e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var errJSON []byte
	if taskErr != nil {
		errJSON = marshalJSON(map[string]string{"message": t.redact.String(taskErr.Error())})
	}
	tag, err := tx.Exec(ctx, `
		update tasks set status = $2, output = $3, error = $4, finished_at = now()
		where id = $1 and status in ('queued','running')`,
		t.id, status, t.redact.JSON(marshalJSON(output)), errJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // canceled underneath us; discard the result
	}

	retrying := status == "failed" && t.attempt <= t.retries
	if retrying {
		var nextID uuid.UUID
		err := tx.QueryRow(ctx, `
			insert into tasks (run_id, workspace_id, step_key, attempt, status)
			values ($1, $2, $3, $4, 'queued') returning id`,
			t.runID, t.workspaceID, t.stepKey, t.attempt+1).Scan(&nextID)
		if err != nil {
			return err
		}
		backoff := time.Duration(1<<t.attempt) * time.Second // 2s, 4s, 8s, …
		if _, err := w.e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: nextID},
			&river.InsertOpts{ScheduledAt: time.Now().Add(backoff)}); err != nil {
			return err
		}
	}
	if _, err := w.e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: t.runID}, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.e.notify(ctx, t.runID)
	taskLog := w.e.taskLogger(t)
	if taskErr != nil {
		taskLog.Error("task failed", "will_retry", retrying, "error", taskErr.Error())
	} else {
		taskLog.Info("task "+status, "attempt", t.attempt)
	}
	return nil
}

func (w *executeTaskWorker) buildContext(ctx context.Context, runID uuid.UUID, runInputRaw []byte) (map[string]any, error) {
	var input any
	if len(runInputRaw) > 0 {
		_ = json.Unmarshal(runInputRaw, &input)
	}

	stepOutputs := map[string]any{}
	rows, err := w.e.Pool.Query(ctx, `
		select distinct on (step_key) step_key, output
		from tasks
		where run_id = $1 and status = 'succeeded'
		order by step_key, attempt desc`, runID)
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
		var out any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		stepOutputs[key] = out
	}
	return map[string]any{"input": input, "steps": stepOutputs}, rows.Err()
}

var exprPattern = regexp.MustCompile(`\{\{(.+?)\}\}`)

// interpolateConfig resolves {{ expr }} in string config values against the
// frozen run context. A value that is exactly one expression keeps its native
// type; mixed text stringifies each expression result.
func interpolateConfig(ctx context.Context, config map[string]any, runContext map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(config))
	for k, v := range config {
		s, isString := v.(string)
		if !isString {
			out[k] = v
			continue
		}
		matches := exprPattern.FindAllStringSubmatchIndex(s, -1)
		if len(matches) == 0 {
			out[k] = v
			continue
		}
		// Whole value is a single expression → keep native type.
		if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(s) {
			val, err := steps.EvalExpression(ctx, strings.TrimSpace(s[matches[0][2]:matches[0][3]]), runContext)
			if err != nil {
				return nil, fmt.Errorf("config %q: %w", k, err)
			}
			out[k] = val
			continue
		}
		var evalFailure error
		result := exprPattern.ReplaceAllStringFunc(s, func(m string) string {
			inner := strings.TrimSpace(m[2 : len(m)-2])
			val, evalErr := steps.EvalExpression(ctx, inner, runContext)
			if evalErr != nil {
				evalFailure = fmt.Errorf("config %q: %w", k, evalErr)
				return ""
			}
			return stringify(val)
		})
		if evalFailure != nil {
			return nil, evalFailure
		}
		out[k] = result
	}
	return out, nil
}

func stringify(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return string(marshalJSON(v))
	}
}
