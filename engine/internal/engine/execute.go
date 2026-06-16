package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	t, status, step, executor, in, err := w.e.prepareTask(ctx, job.Args.TaskID)
	if err != nil {
		if t.id == uuid.Nil {
			return err // couldn't load the task at all; surface as infra error
		}
		return w.e.finishTask(ctx, t, "failed", nil, err)
	}
	if status != "queued" {
		return nil // already handled (job replay) or canceled
	}

	// Guarded transition: a concurrent cancel wins, and this job drops out.
	// Persisted input is redacted — secrets interpolated into config must
	// never land in task rows.
	tag, err := w.e.Pool.Exec(ctx, `
		update tasks set status = 'running', started_at = now(), input = $2
		where id = $1 and status = 'queued'`, t.id, t.redact.JSON(marshalJSON(in.Config)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	w.e.notify(ctx, t.runID)

	// Every task logs to task_logs by default — lifecycle lines from the
	// engine plus whatever the executor writes through TaskInput.Log.
	in.Log.Info("task started", "type", step.Type)

	out, execErr := executor.Execute(ctx, in)
	var susp *steps.Suspended
	if errors.As(execErr, &susp) {
		return w.e.suspendTask(ctx, t, susp)
	}
	if execErr != nil {
		return w.e.finishTask(ctx, t, "failed", out.Data, execErr)
	}
	return w.e.finishTask(ctx, t, "succeeded", out.Data, nil)
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

// prepareTask loads a task with its run input and pinned definition, builds the
// redactor from workspace secrets, assembles the frozen expression context
// (input + succeeded step outputs + secrets), interpolates the step config, and
// builds the per-task logger. It is shared by the execute and resume workers so
// context and redaction are constructed identically across a suspend/resume
// boundary — the redactor is rebuilt every invocation, never carried across a
// suspension (hard rule 6).
//
// It returns the current task status so the caller applies its own guard
// (queued for execute, suspended for resume). If the task row itself can't be
// loaded, t.id is uuid.Nil and the error is an infrastructure error; any later
// error returns a populated t so the caller can fail the task.
func (e *Engine) prepareTask(ctx context.Context, taskID uuid.UUID) (
	t taskRef, status string, step *definition.Step, executor steps.Executor, in steps.TaskInput, err error,
) {
	var (
		runID, workspaceID         uuid.UUID
		stepKey                    string
		attempt                    int
		runInputRaw, definitionRaw []byte
	)
	err = e.Pool.QueryRow(ctx, `
		select t.run_id, t.workspace_id, t.step_key, t.attempt, t.status::text, r.input, v.definition
		from tasks t
		join runs r on r.id = t.run_id
		join workflow_versions v on v.id = r.workflow_version_id
		where t.id = $1`, taskID).Scan(
		&runID, &workspaceID, &stepKey, &attempt, &status, &runInputRaw, &definitionRaw)
	if err != nil {
		err = fmt.Errorf("load task %s: %w", taskID, err)
		return
	}
	t = taskRef{id: taskID, runID: runID, workspaceID: workspaceID, stepKey: stepKey, attempt: attempt}

	// Workspace secrets: bound as expression context, and their values are
	// scrubbed from everything this task persists or logs.
	var secrets map[string]string
	if e.Secrets != nil {
		secrets, err = e.Secrets.WorkspaceSecrets(ctx, workspaceID)
		if err != nil {
			err = fmt.Errorf("load secrets: %w", err)
			return
		}
	}
	t.redact = newRedactor(secrets)

	def, derr := definition.Parse(definitionRaw)
	if derr != nil {
		err = derr
		return
	}
	step = def.Step(stepKey)
	if step == nil {
		err = fmt.Errorf("step %q not in definition", stepKey)
		return
	}
	t.retries = step.Retries

	var ok bool
	executor, ok = e.Registry.Get(step.Type)
	if !ok {
		err = fmt.Errorf("unknown step type %q", step.Type)
		return
	}

	// Assemble the frozen context: run input + succeeded dependency outputs
	// + workspace secrets ({{secrets.<name>}}).
	runContext, cerr := e.buildContext(ctx, runID, runInputRaw)
	if cerr != nil {
		err = cerr
		return
	}
	secretsAny := make(map[string]any, len(secrets))
	for k, v := range secrets {
		secretsAny[k] = v
	}
	runContext["secrets"] = secretsAny

	config, ierr := interpolateConfig(ctx, step.Config, runContext)
	if ierr != nil {
		err = fmt.Errorf("interpolation: %w", ierr)
		return
	}

	in = steps.TaskInput{
		WorkspaceID: workspaceID,
		RunID:       runID,
		TaskID:      taskID,
		StepKey:     stepKey,
		Config:      config,
		Context:     runContext,
		Log:         e.taskLogger(t),
	}
	return
}

// finishTask persists the terminal task state, deletes any suspension, and
// enqueues advance_run in the same transaction. A failure with attempts left
// also inserts the next task attempt, scheduled with exponential backoff.
// Worker errors are absorbed into task state — the job itself only fails on
// infrastructure errors. The status guard makes a concurrent cancel win over an
// in-flight (or resuming) result; it accepts 'suspended' so the resume worker
// can finalize a previously-suspended task.
func (e *Engine) finishTask(ctx context.Context, t taskRef, status string, output any, taskErr error) error {
	tx, err := e.Pool.Begin(ctx)
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
		where id = $1 and status in ('queued','running','suspended')`,
		t.id, status, t.redact.JSON(marshalJSON(output)), errJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // canceled underneath us; discard the result
	}

	// A finalized task keeps no suspension (idempotent: 0 rows for tasks that
	// never suspended).
	if _, err := tx.Exec(ctx, `delete from suspensions where task_id = $1`, t.id); err != nil {
		return err
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
		if _, err := e.Client.InsertTx(ctx, tx, ExecuteTaskArgs{TaskID: nextID},
			&river.InsertOpts{ScheduledAt: time.Now().Add(backoff)}); err != nil {
			return err
		}
	}
	if _, err := e.Client.InsertTx(ctx, tx, AdvanceRunArgs{RunID: t.runID}, nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.notify(ctx, t.runID)
	taskLog := e.taskLogger(t)
	if taskErr != nil {
		taskLog.Error("task failed", "will_retry", retrying, "error", taskErr.Error())
	} else {
		taskLog.Info("task "+status, "attempt", t.attempt)
	}
	return nil
}

// suspendTask persists status=suspended + a suspensions row and (for poll/delay)
// schedules the resume job, all in one transaction (hard rule 2). It serves both
// the initial suspend (task running -> suspended) and a re-suspend from the
// resume worker (task already suspended), replacing any prior suspension row. A
// concurrent cancel wins: status no longer in (running,suspended) -> RowsAffected
// 0 -> the suspension is dropped.
func (e *Engine) suspendTask(ctx context.Context, t taskRef, s *steps.Suspended) error {
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		update tasks set status = 'suspended'
		where id = $1 and status in ('running','suspended')`, t.id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // canceled underneath us
	}
	if _, err := tx.Exec(ctx, `delete from suspensions where task_id = $1`, t.id); err != nil {
		return err
	}

	token := s.Token
	if s.Kind == "callback" && token == "" {
		token, err = newResumeToken()
		if err != nil {
			return err
		}
	}
	var resumeAt *time.Time
	if !s.ResumeAt.IsZero() {
		ra := s.ResumeAt
		resumeAt = &ra
	}

	var suspID uuid.UUID
	if err := tx.QueryRow(ctx, `
		insert into suspensions (task_id, workspace_id, kind, resume_token, resume_at, payload)
		values ($1, $2, $3, $4, $5, $6) returning id`,
		t.id, t.workspaceID, s.Kind, nullableText(token), resumeAt,
		t.redact.JSON(marshalJSON(s.Payload))).Scan(&suspID); err != nil {
		return err
	}

	// Poll/delay: schedule the resume at resume_at in the same tx (hard rule 2).
	// Callback-only (zero resume_at): no scheduled job; waits for the token hit.
	if resumeAt != nil {
		if _, err := e.Client.InsertTx(ctx, tx,
			ResumeTaskArgs{TaskID: t.id, SuspensionID: suspID, Reason: "poll"},
			&river.InsertOpts{ScheduledAt: *resumeAt}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	e.notify(ctx, t.runID)
	if resumeAt != nil {
		e.taskLogger(t).Info("task suspended", "kind", s.Kind, "resume_at", resumeAt.UTC().Format(time.RFC3339))
	} else {
		e.taskLogger(t).Info("task suspended", "kind", s.Kind)
	}
	return nil
}

func (e *Engine) buildContext(ctx context.Context, runID uuid.UUID, runInputRaw []byte) (map[string]any, error) {
	var input any
	if len(runInputRaw) > 0 {
		_ = json.Unmarshal(runInputRaw, &input)
	}

	stepOutputs := map[string]any{}
	rows, err := e.Pool.Query(ctx, `
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

// newResumeToken mints an unguessable, single-use callback resume token (the
// capability behind POST /v1/resume/{token}). Same shape as session tokens.
func newResumeToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// nullableText maps "" to a SQL NULL so the unique resume_token index isn't
// collided by every poll/delay suspension (which carry no token).
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
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
