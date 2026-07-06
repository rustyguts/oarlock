package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// enableWorkflow flips the seeded workflow to is_enabled=true (seedWorkflow
// leaves it at the schema default of false). The scheduler only fires triggers
// on enabled workflows.
func enableWorkflow(t *testing.T, e *Engine, wfID uuid.UUID) {
	t.Helper()
	if _, err := e.Pool.Exec(context.Background(),
		`update workflows set is_enabled = true where id = $1`, wfID); err != nil {
		t.Fatalf("enable workflow: %v", err)
	}
}

// insertScheduleTrigger inserts a schedule trigger with the given cron expr and
// enabled flag, returning its id.
func insertScheduleTrigger(t *testing.T, e *Engine, s seeded, cronExpr string, enabled bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := e.Pool.QueryRow(context.Background(), `
		insert into triggers (workspace_id, workflow_id, type, config, is_enabled)
		values ($1, $2, 'schedule', jsonb_build_object('cron', $3::text), $4)
		returning id`, s.wsID, s.wfID, cronExpr, enabled).Scan(&id)
	if err != nil {
		t.Fatalf("insert schedule trigger: %v", err)
	}
	return id
}

func countRuns(t *testing.T, e *Engine, wfID uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(),
		`select count(*) from runs where workflow_id = $1`, wfID).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

// TestSchedulerFiresOnce: a due schedule trigger fires exactly one run; a second
// sweep and a concurrent duplicate insert (same idempotency key) are no-ops.
func TestSchedulerFiresOnce(t *testing.T) {
	e := newTestEngine(t, testRegistry(nil))
	s := seedWorkflow(t, e, `{"steps":[]}`)
	enableWorkflow(t, e, s.wfID)
	tid := insertScheduleTrigger(t, e, s, "* * * * *", true)

	ctx := context.Background()
	if err := e.sweepSchedules(ctx); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if got := countRuns(t, e, s.wfID); got != 1 {
		t.Fatalf("after first sweep runs = %d, want 1", got)
	}

	// The run carries the trigger id, a cron: idempotency key, and the
	// scheduled_for input.
	var (
		gotTrigger uuid.UUID
		idemKey    string
		input      string
	)
	err := e.Pool.QueryRow(ctx, `
		select trigger_id, idempotency_key, input::text
		from runs where workflow_id = $1`, s.wfID).Scan(&gotTrigger, &idemKey, &input)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if gotTrigger != tid {
		t.Fatalf("run trigger_id = %s, want %s", gotTrigger, tid)
	}
	if !strings.HasPrefix(idemKey, "cron:"+tid.String()+":") {
		t.Fatalf("idempotency key = %q, want prefix cron:%s:", idemKey, tid)
	}
	if !strings.Contains(input, "scheduled_for") {
		t.Fatalf("run input = %q, want scheduled_for", input)
	}

	// A second sweep computes the same occurrence → same key → no new run.
	if err := e.sweepSchedules(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if got := countRuns(t, e, s.wfID); got != 1 {
		t.Fatalf("after second sweep runs = %d, want 1 (idempotent)", got)
	}

	// A concurrent duplicate insert with the same key is a silent no-op that
	// returns the existing run with created=false.
	existingID, created, err := e.StartRunOpts(ctx, s.wsID, s.wfID,
		map[string]any{"scheduled_for": "x"}, RunOpts{TriggerID: &tid, IdempotencyKey: idemKey})
	if err != nil {
		t.Fatalf("duplicate StartRunOpts: %v", err)
	}
	if created {
		t.Fatalf("duplicate insert reported created=true, want false")
	}
	if existingID == uuid.Nil {
		t.Fatalf("duplicate insert returned nil run id")
	}
	if got := countRuns(t, e, s.wfID); got != 1 {
		t.Fatalf("after duplicate insert runs = %d, want 1", got)
	}
}

// TestSchedulerSkipsDisabled: a disabled workflow and a disabled trigger never
// fire, and a bad cron expression is skipped without aborting the sweep.
func TestSchedulerSkipsDisabled(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(nil))

	// Disabled workflow (enabled trigger) → no fire.
	disabledWf := seedWorkflow(t, e, `{"steps":[]}`)
	insertScheduleTrigger(t, e, disabledWf, "* * * * *", true)
	if err := e.sweepSchedules(ctx); err != nil {
		t.Fatalf("sweep (disabled wf): %v", err)
	}
	if got := countRuns(t, e, disabledWf.wfID); got != 0 {
		t.Fatalf("disabled workflow fired %d runs, want 0", got)
	}

	// Enabled workflow but disabled trigger → no fire.
	e2 := newTestEngine(t, testRegistry(nil))
	s := seedWorkflow(t, e2, `{"steps":[]}`)
	enableWorkflow(t, e2, s.wfID)
	insertScheduleTrigger(t, e2, s, "* * * * *", false)
	// A bad cron expr on an enabled trigger must not abort the sweep.
	insertScheduleTrigger(t, e2, s, "not a cron expr", true)
	if err := e2.sweepSchedules(ctx); err != nil {
		t.Fatalf("sweep (disabled trigger): %v", err)
	}
	if got := countRuns(t, e2, s.wfID); got != 0 {
		t.Fatalf("disabled trigger fired %d runs, want 0", got)
	}
}
