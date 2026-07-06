package engine

// DB-backed tests for suspensions (design: long waits): a long delay and
// the callback/approval step park their task 'suspended' (freeing the worker
// slot), then a scheduled resume_task job or the ResumeSuspendedTask callback
// path revives it. They reuse the dbtest_test.go harness and drive the workers
// directly.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// --- suspension query helpers -------------------------------------------------

// suspensionRow returns a task's suspension, if any: its kind, stored (hashed)
// resume token, and whether resume_at is null.
func suspensionRow(t *testing.T, e *Engine, taskID uuid.UUID) (kind, token string, resumeAtNull, exists bool) {
	t.Helper()
	err := e.Pool.QueryRow(context.Background(),
		`select kind, coalesce(resume_token,''), resume_at is null from suspensions where task_id=$1`,
		taskID).Scan(&kind, &token, &resumeAtNull)
	if err != nil {
		return "", "", false, false // ErrNoRows → no suspension
	}
	return kind, token, resumeAtNull, true
}

func taskOutput(t *testing.T, e *Engine, id uuid.UUID) string {
	t.Helper()
	var out string
	if err := e.Pool.QueryRow(context.Background(),
		`select coalesce(output::text,'') from tasks where id=$1`, id).Scan(&out); err != nil {
		t.Fatalf("task output: %v", err)
	}
	return out
}

// resumeJobCount counts scheduled resume_task River jobs for a task.
func resumeJobCount(t *testing.T, e *Engine, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(),
		`select count(*) from river_job where kind='resume_task' and args->>'task_id'=$1`,
		taskID.String()).Scan(&n); err != nil {
		t.Fatalf("resume job count: %v", err)
	}
	return n
}

func extractResumeToken(t *testing.T, outputJSON string) string {
	t.Helper()
	var out struct {
		ResumeURL string `json:"resume_url"`
	}
	if err := json.Unmarshal([]byte(outputJSON), &out); err != nil {
		t.Fatalf("parse suspended output %q: %v", outputJSON, err)
	}
	return strings.TrimPrefix(out.ResumeURL, "/resume/")
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// suspendOneStep advances the run to queue the single step and executes it,
// returning the (now suspended) task id.
func suspendOneStep(t *testing.T, e *Engine, runID uuid.UUID, step string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	adv := &advanceRunWorker{e: e}
	exe := &executeTaskWorker{e: e}
	if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	id := taskID(t, e, runID, step, 1)
	if err := exe.Work(ctx, &river.Job[ExecuteTaskArgs]{Args: ExecuteTaskArgs{TaskID: id}}); err != nil {
		t.Fatalf("execute %s: %v", step, err)
	}
	return id
}

// --- token unit test (no DB) --------------------------------------------------

// TestGenerateResumeToken: the raw token is rsm_<48 hex> and the stored value is
// its sha256; two calls never collide.
func TestGenerateResumeToken(t *testing.T) {
	raw, hashed, err := generateResumeToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(raw, "rsm_") {
		t.Fatalf("raw = %q, want rsm_ prefix", raw)
	}
	if len(raw) != len("rsm_")+48 {
		t.Fatalf("raw len = %d, want %d", len(raw), len("rsm_")+48)
	}
	if hashed != hashHex(raw) {
		t.Fatalf("hashed = %q, want sha256(raw) = %q", hashed, hashHex(raw))
	}
	raw2, _, _ := generateResumeToken()
	if raw2 == raw {
		t.Fatal("consecutive tokens must differ")
	}
}

// --- DB-backed suspension tests -----------------------------------------------

// TestDelaySuspends: a delay over the in-process ceiling parks the task
// 'suspended', writes a delay suspension with a future resume_at, and schedules
// exactly one resume_task job. The run stays running.
func TestDelaySuspends(t *testing.T) {
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"delay": &steps.Delay{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"d","type":"delay","config":{"seconds":600}}]}`)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dID := suspendOneStep(t, e, runID, "d")

	if got := taskStatusByID(t, e, dID); got != "suspended" {
		t.Fatalf("task status = %q, want suspended", got)
	}
	if got := runStatus(t, e, runID); got != "running" {
		t.Fatalf("run status = %q, want running (a suspended task keeps the run alive)", got)
	}
	kind, token, resumeAtNull, ok := suspensionRow(t, e, dID)
	if !ok {
		t.Fatal("expected a suspensions row")
	}
	if kind != "delay" {
		t.Fatalf("kind = %q, want delay", kind)
	}
	if token == "" {
		t.Fatal("resume_token should be set for every kind")
	}
	if resumeAtNull {
		t.Fatal("a delay suspension must carry resume_at")
	}
	if n := resumeJobCount(t, e, dID); n != 1 {
		t.Fatalf("scheduled resume_task jobs = %d, want 1", n)
	}
	if out := taskOutput(t, e, dID); !strings.Contains(out, "waited_seconds") {
		t.Fatalf("suspended output = %q, want it to carry waited_seconds", out)
	}
}

// TestResumeWorkerCompletes: the scheduled resume flips the delay task to
// succeeded, deletes the suspension, and the downstream step advances the run to
// completion.
func TestResumeWorkerCompletes(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{
		"delay":     &steps.Delay{},
		"test.echo": echoExec{},
	}))
	s := seedWorkflow(t, e, `{"steps":[
		{"key":"d","type":"delay","config":{"seconds":600}},
		{"key":"after","type":"test.echo","needs":["d"]}
	]}`)
	runID, err := e.StartRun(ctx, s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dID := suspendOneStep(t, e, runID, "d")
	if got := taskStatusByID(t, e, dID); got != "suspended" {
		t.Fatalf("d status = %q, want suspended", got)
	}

	rw := &resumeTaskWorker{e: e}
	if err := rw.Work(ctx, &river.Job[ResumeTaskArgs]{Args: ResumeTaskArgs{TaskID: dID}}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := taskStatusByID(t, e, dID); got != "succeeded" {
		t.Fatalf("d status = %q, want succeeded after resume", got)
	}
	if _, _, _, ok := suspensionRow(t, e, dID); ok {
		t.Fatal("suspension row must be deleted after resume")
	}
	if out := taskOutput(t, e, dID); !strings.Contains(out, "waited_seconds") {
		t.Fatalf("resumed output = %q, want waited_seconds", out)
	}

	driveToTerminal(t, e, runID)
	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "after", 1)); got != "succeeded" {
		t.Fatalf("downstream after status = %q, want succeeded", got)
	}
}

// TestCallbackSuspendAndResume: wait.callback parks its task with a resume URL
// (its raw token hashes to the stored one), schedules no job, and
// ResumeSuspendedTask succeeds it with {resumed, payload}, which the downstream
// step reads via steps.<key>.payload.
func TestCallbackSuspendAndResume(t *testing.T) {
	ctx := context.Background()
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{
		"wait.callback": &steps.WaitCallback{},
		"test.echo":     echoExec{rec: rec},
	}))
	s := seedWorkflow(t, e, `{"steps":[
		{"key":"gate","type":"wait.callback","config":{"note":"approve"}},
		{"key":"after","type":"test.echo","needs":["gate"]}
	]}`)
	runID, err := e.StartRun(ctx, s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	gID := suspendOneStep(t, e, runID, "gate")

	if got := taskStatusByID(t, e, gID); got != "suspended" {
		t.Fatalf("gate status = %q, want suspended", got)
	}
	kind, storedHash, resumeAtNull, ok := suspensionRow(t, e, gID)
	if !ok || kind != "callback" {
		t.Fatalf("suspension kind = %q (exists=%v), want callback", kind, ok)
	}
	if !resumeAtNull {
		t.Fatal("a callback suspension must have null resume_at")
	}
	if n := resumeJobCount(t, e, gID); n != 0 {
		t.Fatalf("callback must schedule no resume job, got %d", n)
	}

	// The raw token in the resume URL must hash to the stored value.
	rawToken := extractResumeToken(t, taskOutput(t, e, gID))
	if !strings.HasPrefix(rawToken, "rsm_") {
		t.Fatalf("resume URL token = %q, want rsm_ prefix", rawToken)
	}
	if hashHex(rawToken) != storedHash {
		t.Fatal("stored resume_token must be sha256 of the raw token")
	}

	// Resume through the engine method (what the api handler calls after hashing).
	gotRun, err := e.ResumeSuspendedTask(ctx, hashHex(rawToken), map[string]any{"approved": true})
	if err != nil {
		t.Fatalf("ResumeSuspendedTask: %v", err)
	}
	if gotRun != runID {
		t.Fatalf("resume returned run %s, want %s", gotRun, runID)
	}
	if got := taskStatusByID(t, e, gID); got != "succeeded" {
		t.Fatalf("gate status = %q, want succeeded", got)
	}
	// Parse (jsonb re-serializes with spaces, so substring matching is brittle).
	var resumedOut struct {
		Resumed bool `json:"resumed"`
		Payload struct {
			Approved bool `json:"approved"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(taskOutput(t, e, gID)), &resumedOut); err != nil {
		t.Fatalf("parse resumed output: %v", err)
	}
	if !resumedOut.Resumed || !resumedOut.Payload.Approved {
		t.Fatalf("resumed output = %+v, want resumed=true + payload.approved=true", resumedOut)
	}
	if _, _, _, ok := suspensionRow(t, e, gID); ok {
		t.Fatal("suspension row must be deleted after resume")
	}

	// A second resume with the (now consumed) token is not-waiting → 409-shaped.
	if _, err := e.ResumeSuspendedTask(ctx, storedHash, nil); err != ErrSuspensionNotFound {
		t.Fatalf("second resume err = %v, want ErrSuspensionNotFound", err)
	}

	// Downstream sees the payload via steps.gate.payload.
	driveToTerminal(t, e, runID)
	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	afterCtx := rec.contexts["after"]
	stepsMap, _ := afterCtx["steps"].(map[string]any)
	gateOut, _ := stepsMap["gate"].(map[string]any)
	payload, _ := gateOut["payload"].(map[string]any)
	if payload["approved"] != true {
		t.Fatalf("downstream should see steps.gate.payload.approved=true, got %#v", gateOut)
	}
}

// TestCancelWhileSuspended: canceling a run with a suspended task cancels the
// task; the later scheduled resume is a clean no-op that leaves the run canceled
// and clears the stale suspension row.
func TestCancelWhileSuspended(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"delay": &steps.Delay{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"d","type":"delay","config":{"seconds":600}}]}`)
	runID, err := e.StartRun(ctx, s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	dID := suspendOneStep(t, e, runID, "d")
	if got := taskStatusByID(t, e, dID); got != "suspended" {
		t.Fatalf("d status = %q, want suspended", got)
	}

	if err := e.CancelRun(ctx, s.wsID, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if got := taskStatusByID(t, e, dID); got != "canceled" {
		t.Fatalf("d status = %q, want canceled (cancel reaches suspended tasks)", got)
	}

	rw := &resumeTaskWorker{e: e}
	if err := rw.Work(ctx, &river.Job[ResumeTaskArgs]{Args: ResumeTaskArgs{TaskID: dID}}); err != nil {
		t.Fatalf("resume no-op: %v", err)
	}
	if got := taskStatusByID(t, e, dID); got != "canceled" {
		t.Fatalf("resume must not revive a canceled task: status = %q", got)
	}
	if got := runStatus(t, e, runID); got != "canceled" {
		t.Fatalf("run status = %q, want canceled", got)
	}
	if _, _, _, ok := suspensionRow(t, e, dID); ok {
		t.Fatal("the no-op resume should delete the stale suspension row")
	}
}

// TestShortDelayInProcess: a delay within the in-process ceiling runs to
// completion the old way — no suspension row, run succeeds.
func TestShortDelayInProcess(t *testing.T) {
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"delay": &steps.Delay{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"d","type":"delay","config":{"seconds":0.05}}]}`)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	drive(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	dID := taskID(t, e, runID, "d", 1)
	if got := taskStatusByID(t, e, dID); got != "succeeded" {
		t.Fatalf("d status = %q, want succeeded", got)
	}
	if _, _, _, ok := suspensionRow(t, e, dID); ok {
		t.Fatal("a short in-process delay must not create a suspension")
	}
	if out := taskOutput(t, e, dID); !strings.Contains(out, "waited_seconds") {
		t.Fatalf("delay output = %q, want waited_seconds", out)
	}
}
