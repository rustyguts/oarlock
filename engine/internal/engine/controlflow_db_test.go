package engine

// DB-backed tests for step-level `if` guards (design §6 step 19). They reuse the
// dbtest_test.go harness (seedWorkflow, newTestEngine, the query helpers) but
// drive with driveToTerminal below rather than drive: a guard that skips or
// fails a step leaves a pass with no queued task yet the run is not terminal
// (it enqueues a follow-up advance), which drive would mistake for convergence.
//
// Point these at a dedicated test DB so they don't collide with other suites:
//   DATABASE_URL_TEST=postgres://oarlock:oarlock@localhost:5432/oarlock_test_cf

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// driveToTerminal advances the run and executes queued tasks until the run
// reaches a terminal state. Unlike drive it keeps advancing across passes that
// leave no queued task (a guard-skip pass), which is exactly the `if` path.
func driveToTerminal(t *testing.T, e *Engine, runID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	adv := &advanceRunWorker{e: e}
	exe := &executeTaskWorker{e: e}
	for i := 0; i < 200; i++ {
		if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
			t.Fatalf("advance: %v", err)
		}
		switch runStatus(t, e, runID) {
		case "succeeded", "failed", "canceled":
			return
		}
		for _, id := range queuedTaskIDs(t, e, runID) {
			if err := exe.Work(ctx, &river.Job[ExecuteTaskArgs]{Args: ExecuteTaskArgs{TaskID: id}}); err != nil {
				t.Fatalf("execute %s: %v", id, err)
			}
		}
	}
	t.Fatalf("run %s did not converge", runID)
}

// emitValueExec echoes the run input's "v" as {"value": v}, so a downstream
// guard can be exercised against a real upstream step output.
type emitValueExec struct{}

func (emitValueExec) Execute(_ context.Context, in steps.TaskInput) (steps.TaskOutput, error) {
	var v any
	if inp, ok := in.Context["input"].(map[string]any); ok {
		v = inp["v"]
	}
	return steps.TaskOutput{Data: map[string]any{"value": v}}, nil
}

func taskError(t *testing.T, e *Engine, id uuid.UUID) string {
	t.Helper()
	var errJSON []byte
	if err := e.Pool.QueryRow(context.Background(),
		`select error from tasks where id=$1`, id).Scan(&errJSON); err != nil {
		t.Fatalf("task error: %v", err)
	}
	return string(errJSON)
}

// TestIfGuardTrueRuns: a truthy guard runs the step normally.
func TestIfGuardTrueRuns(t *testing.T) {
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.echo","needs":["a"],"if":"true"}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	driveToTerminal(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "b", 1)); got != "succeeded" {
		t.Fatalf("b status = %q, want succeeded (guard true)", got)
	}
}

// TestIfGuardFalseSkips: a falsy guard writes a terminal 'skipped' row with no
// output and no execute_task job, yet a downstream step whose needs include the
// skipped step still runs and sees steps.<skipped> as undefined. The run
// succeeds.
func TestIfGuardFalseSkips(t *testing.T) {
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{rec: rec}}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.echo","needs":["a"],"if":"false"},
		{"key":"c","type":"test.echo","needs":["b"]}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	driveToTerminal(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	bID := taskID(t, e, runID, "b", 1)
	if got := taskStatusByID(t, e, bID); got != "skipped" {
		t.Fatalf("b status = %q, want skipped", got)
	}
	if !taskOutputNull(t, e, bID) {
		t.Fatal("skipped task must have no output")
	}
	// c depends on the skipped b; a skipped dependency satisfies needs.
	if got := taskStatusByID(t, e, taskID(t, e, runID, "c", 1)); got != "succeeded" {
		t.Fatalf("c status = %q, want succeeded (skipped dep satisfies needs)", got)
	}
	// c must not see steps.b — a skipped step produces no output.
	cctx := rec.contexts["c"]
	stepsMap, ok := cctx["steps"].(map[string]any)
	if !ok {
		t.Fatalf("c context has no steps map: %#v", cctx)
	}
	if _, leaked := stepsMap["b"]; leaked {
		t.Fatalf("c saw steps.b despite b being skipped: %#v", stepsMap)
	}
}

// TestIfGuardUpstreamOutputBothWays: a guard referencing an upstream output
// (steps.a.value > 5) runs the step when true and skips it when false. Two runs
// of the same workflow with different inputs exercise both branches.
func TestIfGuardUpstreamOutputBothWays(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{
		"test.emit": emitValueExec{},
		"test.echo": echoExec{},
	}))
	def := `{"steps":[
		{"key":"a","type":"test.emit"},
		{"key":"b","type":"test.echo","needs":["a"],"if":"steps.a.value > 5"}
	]}`
	s := seedWorkflow(t, e, def)

	// value 7 > 5 → b runs.
	run1, err := e.StartRun(ctx, s.wsID, s.wfID, map[string]any{"v": 7})
	if err != nil {
		t.Fatalf("StartRun (true): %v", err)
	}
	driveToTerminal(t, e, run1)
	if got := runStatus(t, e, run1); got != "succeeded" {
		t.Fatalf("run1 status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, run1, "b", 1)); got != "succeeded" {
		t.Fatalf("b(7) status = %q, want succeeded", got)
	}

	// value 3 > 5 → b skipped.
	run2, err := e.StartRun(ctx, s.wsID, s.wfID, map[string]any{"v": 3})
	if err != nil {
		t.Fatalf("StartRun (false): %v", err)
	}
	driveToTerminal(t, e, run2)
	if got := runStatus(t, e, run2); got != "succeeded" {
		t.Fatalf("run2 status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, run2, "b", 1)); got != "skipped" {
		t.Fatalf("b(3) status = %q, want skipped", got)
	}
}

// TestIfGuardEvalErrorFailsRun: a guard that raises at eval time (referencing an
// undefined binding) writes a terminal 'failed' guard row whose error names the
// condition, and the run fails through normal advancement.
func TestIfGuardEvalErrorFailsRun(t *testing.T) {
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.echo","needs":["a"],"if":"nosuch.thing.x"}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	driveToTerminal(t, e, runID)

	if got := runStatus(t, e, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "a", 1)); got != "succeeded" {
		t.Fatalf("a status = %q, want succeeded", got)
	}
	bID := taskID(t, e, runID, "b", 1)
	if got := taskStatusByID(t, e, bID); got != "failed" {
		t.Fatalf("b status = %q, want failed (guard eval error)", got)
	}
	if e := taskError(t, e, bID); !strings.Contains(e, "if condition") {
		t.Fatalf("b error = %q, want it to name the if condition", e)
	}
}

// TestIfGuardDiamondSkippedBranch: in a diamond a→(b,c)→d where c is guarded
// false, the fan-in d still completes (both a succeeded b and a skipped c
// satisfy its needs) and sees only b's output, not c's.
func TestIfGuardDiamondSkippedBranch(t *testing.T) {
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{rec: rec}}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.echo","needs":["a"]},
		{"key":"c","type":"test.echo","needs":["a"],"if":"false"},
		{"key":"d","type":"test.echo","needs":["b","c"]}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	driveToTerminal(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "c", 1)); got != "skipped" {
		t.Fatalf("c status = %q, want skipped", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "d", 1)); got != "succeeded" {
		t.Fatalf("d status = %q, want succeeded (fan-in over skipped branch)", got)
	}
	dctx := rec.contexts["d"]
	stepsMap, _ := dctx["steps"].(map[string]any)
	if _, ok := stepsMap["b"]; !ok {
		t.Fatalf("d context missing steps.b: %#v", stepsMap)
	}
	if _, leaked := stepsMap["c"]; leaked {
		t.Fatalf("d saw steps.c despite c being skipped: %#v", stepsMap)
	}
}
