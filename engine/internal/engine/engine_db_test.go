package engine

import (
	"context"
	"testing"

	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// TestDiamondDAG: a → (b, c) → d. All succeed. Every step gets exactly one
// succeeded task row, the run succeeds, d runs only after both b and c, and d's
// frozen context carries both upstream outputs.
func TestDiamondDAG(t *testing.T) {
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{rec: rec}}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.echo","needs":["a"]},
		{"key":"c","type":"test.echo","needs":["a"]},
		{"key":"d","type":"test.echo","needs":["b","c"]}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, map[string]any{"seed": "v"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	drive(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	tasks := allTasks(t, e, runID)
	if len(tasks) != 4 {
		t.Fatalf("expected 4 task rows, got %d: %+v", len(tasks), tasks)
	}
	for _, tr := range tasks {
		if tr.attempt != 1 || tr.status != "succeeded" {
			t.Fatalf("task %+v: want attempt 1 succeeded", tr)
		}
	}

	// d must run after both b and c.
	if od, ob, oc := rec.orderOf("d"), rec.orderOf("b"), rec.orderOf("c"); !(od > ob && od > oc) {
		t.Fatalf("d must run after b and c: order b=%d c=%d d=%d", ob, oc, od)
	}
	// d's context must contain b's and c's outputs (its transitive needs).
	dctx := rec.contexts["d"]
	stepsMap, _ := dctx["steps"].(map[string]any)
	if _, ok := stepsMap["b"]; !ok {
		t.Fatalf("d context missing steps.b: %#v", stepsMap)
	}
	if _, ok := stepsMap["c"]; !ok {
		t.Fatalf("d context missing steps.c: %#v", stepsMap)
	}
}

// TestFailureFailsRun: b fails with no retries → the run fails and the
// downstream step d never gets a task row.
func TestFailureFailsRun(t *testing.T) {
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{
		"test.echo": echoExec{rec: rec},
		"test.fail": failExec{rec: rec},
	}))
	def := `{"steps":[
		{"key":"a","type":"test.echo"},
		{"key":"b","type":"test.fail","needs":["a"]},
		{"key":"d","type":"test.echo","needs":["b"]}
	]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	drive(t, e, runID)

	if got := runStatus(t, e, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "a", 1)); got != "succeeded" {
		t.Fatalf("a status = %q, want succeeded", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "b", 1)); got != "failed" {
		t.Fatalf("b status = %q, want failed", got)
	}
	if n := countTaskAttempts(t, e, runID, "b"); n != 1 {
		t.Fatalf("b attempts = %d, want 1 (no retries)", n)
	}
	if hasTask(t, e, runID, "d") {
		t.Fatal("d should never have been scheduled after b failed")
	}
}

// TestRetrySemantics: a step with retries=2 whose executor fails twice then
// succeeds produces three task rows (attempts 1..3, fail/fail/succeed) and a
// succeeded run.
func TestRetrySemantics(t *testing.T) {
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.flaky": newFlaky(2)}))
	def := `{"steps":[{"key":"s","type":"test.flaky","retries":2}]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(context.Background(), s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	drive(t, e, runID)

	if got := runStatus(t, e, runID); got != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", got)
	}
	tasks := allTasks(t, e, runID)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 attempts, got %d: %+v", len(tasks), tasks)
	}
	want := []taskRow{
		{"s", 1, "failed"},
		{"s", 2, "failed"},
		{"s", 3, "succeeded"},
	}
	for i, w := range want {
		if tasks[i] != w {
			t.Fatalf("attempt %d = %+v, want %+v", i+1, tasks[i], w)
		}
	}
}

// TestCancelBeatsLateResult: a task is 'running', the run is canceled, then a
// late finishTask arrives for that task. The status guard makes cancel win — the
// task stays canceled and the late output is discarded.
func TestCancelBeatsLateResult(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"s","type":"test.echo"}]}`)
	runID := insertRun(t, e, s, "running")
	tid := insertTask(t, e, runID, s.wsID, "s", 1, "running", 0)

	if err := e.CancelRun(ctx, s.wsID, runID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if got := runStatus(t, e, runID); got != "canceled" {
		t.Fatalf("run status = %q, want canceled", got)
	}

	// The in-flight executor finishes late with a result that must not land.
	w := &executeTaskWorker{e: e}
	tr := taskRef{id: tid, runID: runID, workspaceID: s.wsID, stepKey: "s", attempt: 1, retries: 0}
	if err := w.finishTask(ctx, tr, "succeeded", map[string]any{"leaked": "late-output"}, nil); err != nil {
		t.Fatalf("finishTask: %v", err)
	}

	if got := taskStatusByID(t, e, tid); got != "canceled" {
		t.Fatalf("task status = %q, want canceled (cancel must beat the late result)", got)
	}
	if !taskOutputNull(t, e, tid) {
		t.Fatal("late output should have been discarded, but task.output is set")
	}
}

// TestAdvanceIdempotency: advancing the same run twice must not create duplicate
// task rows. The runs row is locked FOR UPDATE per advance and the insert is
// ON CONFLICT DO NOTHING, so repeated advances converge. (Sequential is a
// faithful test: the second advance sees the step already queued.)
func TestAdvanceIdempotency(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	def := `{"steps":[{"key":"a","type":"test.echo"},{"key":"b","type":"test.echo","needs":["a"]}]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(ctx, s.wsID, s.wfID, nil)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	adv := &advanceRunWorker{e: e}
	for i := 0; i < 2; i++ {
		if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
			t.Fatalf("advance %d: %v", i, err)
		}
	}

	if n := countTaskAttempts(t, e, runID, "a"); n != 1 {
		t.Fatalf("step a has %d task rows, want exactly 1 (no duplicates)", n)
	}
	if hasTask(t, e, runID, "b") {
		t.Fatal("step b must not be scheduled while a is still pending")
	}
}

// TestContextScoping: y has no needs, sitting beside x. Even when x has already
// succeeded, y's frozen context must not contain steps.x — the expression
// context is scoped to a step's transitive needs, never leaking a sibling by
// completion order.
func TestContextScoping(t *testing.T) {
	ctx := context.Background()
	rec := newRecorder()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{rec: rec}}))
	def := `{"steps":[{"key":"x","type":"test.echo"},{"key":"y","type":"test.echo"}]}`
	s := seedWorkflow(t, e, def)
	runID, err := e.StartRun(ctx, s.wsID, s.wfID, map[string]any{"seed": "v"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	adv := &advanceRunWorker{e: e}
	exe := &executeTaskWorker{e: e}
	if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// Run x to completion first, then y — so if scoping were broken, y would see x.
	xID := taskID(t, e, runID, "x", 1)
	yID := taskID(t, e, runID, "y", 1)
	if err := exe.Work(ctx, &river.Job[ExecuteTaskArgs]{Args: ExecuteTaskArgs{TaskID: xID}}); err != nil {
		t.Fatalf("execute x: %v", err)
	}
	if got := taskStatusByID(t, e, xID); got != "succeeded" {
		t.Fatalf("x status = %q, want succeeded", got)
	}
	if err := exe.Work(ctx, &river.Job[ExecuteTaskArgs]{Args: ExecuteTaskArgs{TaskID: yID}}); err != nil {
		t.Fatalf("execute y: %v", err)
	}

	yctx := rec.contexts["y"]
	stepsMap, ok := yctx["steps"].(map[string]any)
	if !ok {
		t.Fatalf("y context has no steps map: %#v", yctx)
	}
	if _, leaked := stepsMap["x"]; leaked {
		t.Fatalf("y saw steps.x despite no dependency: %#v", stepsMap)
	}
	if len(stepsMap) != 0 {
		t.Fatalf("y (no needs) should see an empty steps map, got %#v", stepsMap)
	}
}

// TestReaperFailsOrphanNoRetries: a task stuck 'running' past the 20-minute
// threshold with no retries is failed by the sweep, then advance fails the run.
func TestReaperFailsOrphanNoRetries(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"s","type":"test.echo"}]}`)
	runID := insertRun(t, e, s, "running")
	tid := insertTask(t, e, runID, s.wsID, "s", 1, "running", 30*60) // started 30m ago

	if err := e.reapOrphans(ctx); err != nil {
		t.Fatalf("reapOrphans: %v", err)
	}
	if got := taskStatusByID(t, e, tid); got != "failed" {
		t.Fatalf("orphan task status = %q, want failed", got)
	}
	if n := countTaskAttempts(t, e, runID, "s"); n != 1 {
		t.Fatalf("no retries: want 1 attempt, got %d", n)
	}
	// The reaper enqueues an advance; drive it and confirm the run fails.
	adv := &advanceRunWorker{e: e}
	if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if got := runStatus(t, e, runID); got != "failed" {
		t.Fatalf("run status = %q, want failed", got)
	}
}

// TestReaperRetriesOrphan: a stuck task on a step with retries left is failed
// and a fresh queued attempt is created — exactly the transient failure retries
// exist for.
func TestReaperRetriesOrphan(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"s","type":"test.echo","retries":1}]}`)
	runID := insertRun(t, e, s, "running")
	tid := insertTask(t, e, runID, s.wsID, "s", 1, "running", 30*60)

	if err := e.reapOrphans(ctx); err != nil {
		t.Fatalf("reapOrphans: %v", err)
	}
	if got := taskStatusByID(t, e, tid); got != "failed" {
		t.Fatalf("attempt 1 status = %q, want failed", got)
	}
	if got := taskStatusByID(t, e, taskID(t, e, runID, "s", 2)); got != "queued" {
		t.Fatalf("attempt 2 status = %q, want queued", got)
	}
	if n := countTaskAttempts(t, e, runID, "s"); n != 2 {
		t.Fatalf("want 2 attempts after reap+retry, got %d", n)
	}
}

// TestReaperGuardedNoOp: if a task's status has already changed away from
// 'running' before reapTask commits, the guarded update is a no-op — the task is
// left untouched and no retry attempt is created.
func TestReaperGuardedNoOp(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	s := seedWorkflow(t, e, `{"steps":[{"key":"s","type":"test.echo","retries":1}]}`)
	runID := insertRun(t, e, s, "running")
	tid := insertTask(t, e, runID, s.wsID, "s", 1, "running", 30*60)

	// Simulate the task finishing underneath the reaper between the SELECT and
	// the guarded UPDATE.
	if _, err := e.Pool.Exec(ctx,
		`update tasks set status='succeeded', finished_at=now() where id=$1`, tid); err != nil {
		t.Fatalf("pre-finish: %v", err)
	}

	o := orphan{
		id:            tid,
		runID:         runID,
		workspaceID:   s.wsID,
		stepKey:       "s",
		attempt:       1,
		definitionRaw: []byte(s.def),
	}
	if err := e.reapTask(ctx, o); err != nil {
		t.Fatalf("reapTask: %v", err)
	}
	if got := taskStatusByID(t, e, tid); got != "succeeded" {
		t.Fatalf("guarded reap must not touch a task that already finished: status = %q", got)
	}
	if n := countTaskAttempts(t, e, runID, "s"); n != 1 {
		t.Fatalf("guarded no-op must not create a retry attempt, got %d attempts", n)
	}
}
