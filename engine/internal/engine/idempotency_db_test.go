package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

// TestIdempotencyKeyScopedByWorkflow guards the P1 fix: the same caller-supplied
// idempotency key on two different workflows in one workspace must start two
// distinct runs, not have the second silently return the first. Reusing the key
// on the *same* workflow still dedupes.
func TestIdempotencyKeyScopedByWorkflow(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t, testRegistry(map[string]steps.Executor{"test.echo": echoExec{}}))
	def := `{"steps":[{"key":"a","type":"test.echo"}]}`

	// Workflow A (its own workspace) and workflow B seeded into A's workspace.
	a := seedWorkflow(t, e, def)
	bWf, bVer := uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		if _, err := e.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed B: %v", err)
		}
	}
	exec(`insert into workflows (id, workspace_id, name, slug) values ($1,$2,$3,$4)`,
		bWf, a.wsID, "wf-b", bWf.String())
	exec(`insert into workflow_versions (id, workflow_id, version, definition) values ($1,$2,1,$3)`,
		bVer, bWf, def)
	exec(`update workflows set current_version_id=$1 where id=$2`, bVer, bWf)

	const key = "shared-key"
	runA, createdA, err := e.StartRunOpts(ctx, a.wsID, a.wfID, nil, RunOpts{IdempotencyKey: key})
	if err != nil || !createdA {
		t.Fatalf("start A: run=%s created=%v err=%v", runA, createdA, err)
	}
	runB, createdB, err := e.StartRunOpts(ctx, a.wsID, bWf, nil, RunOpts{IdempotencyKey: key})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	if !createdB {
		t.Fatal("workflow B reused the same key as A and did not fire — keys are not workflow-scoped")
	}
	if runA == runB {
		t.Fatalf("both workflows returned the same run %s", runA)
	}

	// Same workflow + same key → dedupe to the existing run.
	runA2, createdA2, err := e.StartRunOpts(ctx, a.wsID, a.wfID, nil, RunOpts{IdempotencyKey: key})
	if err != nil {
		t.Fatalf("replay A: %v", err)
	}
	if createdA2 || runA2 != runA {
		t.Fatalf("replay on same workflow should dedupe: run=%s created=%v (want %s, false)", runA2, createdA2, runA)
	}
}
