package engine

// DB-backed harness for the engine's core invariants. Tests here drive the two
// real workers (advanceRunWorker, executeTaskWorker) directly by calling their
// Work methods (see drive) rather than starting a river.Client — direct,
// single-threaded invocation is deterministic and non-flaky, and every state
// transition still goes through the exact production code path (guarded status
// writes, transactional job+state inserts, retry/backoff task rows). River job
// rows enqueued by the workers accumulate inert in river_job and are ignored;
// Postgres rows remain the only source of truth.
//
// If Postgres is unreachable the whole DB suite t.Skips, so a plain offline
// `go test ./...` stays green. Point DATABASE_URL_TEST at another instance to
// override the localhost:5432 default.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/rustyguts/oarlock/engine/internal/db"
	"github.com/rustyguts/oarlock/engine/internal/steps"
)

const defaultTestDBURL = "postgres://oarlock:oarlock@localhost:5432/oarlock_test"

var (
	testPoolOnce sync.Once
	testPool     *pgxpool.Pool
	testPoolErr  error
)

// getTestPool lazily connects (once) to the test database, creating it if
// absent and running the app + River migrations. On any connectivity failure
// it marks the calling test skipped rather than failed.
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testPoolOnce.Do(func() {
		ctx := context.Background()
		dbURL := os.Getenv("DATABASE_URL_TEST")
		if dbURL == "" {
			dbURL = defaultTestDBURL
		}
		if testPoolErr = ensureTestDB(ctx, dbURL); testPoolErr != nil {
			return
		}
		testPool, testPoolErr = pgxpool.New(ctx, dbURL)
		if testPoolErr != nil {
			return
		}
		if testPoolErr = testPool.Ping(ctx); testPoolErr != nil {
			return
		}
		testPoolErr = db.Migrate(ctx, testPool)
	})
	if testPoolErr != nil {
		t.Skipf("test database unavailable (%v); skipping DB-backed engine tests", testPoolErr)
	}
	return testPool
}

// ensureTestDB connects to the maintenance database and creates the target test
// database if it does not exist yet. Identifiers can't be parameterized, but the
// name only ever comes from our own default/env, not user input.
func ensureTestDB(ctx context.Context, dbURL string) error {
	u, err := url.Parse(dbURL)
	if err != nil {
		return err
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return fmt.Errorf("no database name in %q", dbURL)
	}
	admin := *u
	admin.Path = "/oarlock" // the maintenance DB the compose stack always has
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx,
		`select exists(select 1 from pg_database where datname=$1)`, dbName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := conn.Exec(ctx, `create database "`+dbName+`"`); err != nil {
			if !strings.Contains(err.Error(), "already exists") { // lost a create race — fine
				return err
			}
		}
	}
	return nil
}

// newTestEngine builds an Engine against the shared pool with the given step
// registry, then truncates all run/task state so each test starts clean.
// Cache and Secrets are nil: notify becomes a no-op and no secrets are bound.
func newTestEngine(t *testing.T, reg *steps.Registry) *Engine {
	t.Helper()
	pool := getTestPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e, err := New(context.Background(), pool, reg, nil, nil, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	resetDB(t, pool)
	return e
}

// resetDB clears every table a test touches. Truncating workspaces CASCADEs
// through all workspace-scoped tables (workflows, versions, runs, tasks, …);
// task_logs and river_job have no FK to workspaces, so they're listed
// explicitly.
func resetDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`truncate workspaces, users, task_logs, river_job restart identity cascade`)
	if err != nil {
		t.Fatalf("reset db: %v", err)
	}
}

// --- seeding -------------------------------------------------------------

type seeded struct {
	wsID, userID, wfID, versionID uuid.UUID
	def                           string
}

// seedWorkflow mirrors migration 0001's seed shape (workspace + owner + a
// workflow whose current version holds def) using fresh random UUIDs.
func seedWorkflow(t *testing.T, e *Engine, def string) seeded {
	t.Helper()
	ctx := context.Background()
	s := seeded{wsID: uuid.New(), userID: uuid.New(), wfID: uuid.New(), versionID: uuid.New(), def: def}
	exec := func(sql string, args ...any) {
		if _, err := e.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n  sql: %s", err, sql)
		}
	}
	exec(`insert into workspaces (id, slug, name) values ($1,$2,$3)`, s.wsID, s.wsID.String(), "test ws")
	exec(`insert into users (id, email, name) values ($1,$2,$3)`, s.userID, s.userID.String()+"@test", "tester")
	exec(`insert into workspace_members (workspace_id, user_id, role) values ($1,$2,'owner')`, s.wsID, s.userID)
	exec(`insert into workflows (id, workspace_id, name, slug) values ($1,$2,$3,$4)`, s.wfID, s.wsID, "wf", s.wfID.String())
	exec(`insert into workflow_versions (id, workflow_id, version, definition) values ($1,$2,1,$3)`, s.versionID, s.wfID, def)
	exec(`update workflows set current_version_id=$1 where id=$2`, s.versionID, s.wfID)
	return s
}

// insertRun creates a run row directly (bypassing StartRun) with the given
// status — used by the cancel and reaper tests that need a specific starting
// state.
func insertRun(t *testing.T, e *Engine, s seeded, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := e.Pool.QueryRow(context.Background(),
		`insert into runs (workspace_id, workflow_id, workflow_version_id, status, input)
		 values ($1,$2,$3,$4::run_status,'{}') returning id`,
		s.wsID, s.wfID, s.versionID, status).Scan(&id)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	return id
}

// insertTask inserts a task row directly. startedSecondsAgo backdates
// started_at (0 = now) so reaper tests can simulate a task stuck 'running'.
func insertTask(t *testing.T, e *Engine, runID, wsID uuid.UUID, step string, attempt int, status string, startedSecondsAgo int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	var err error
	if startedSecondsAgo > 0 {
		err = e.Pool.QueryRow(ctx,
			`insert into tasks (run_id, workspace_id, step_key, attempt, status, started_at)
			 values ($1,$2,$3,$4,$5::task_status, now() - make_interval(secs => $6)) returning id`,
			runID, wsID, step, attempt, status, startedSecondsAgo).Scan(&id)
	} else {
		err = e.Pool.QueryRow(ctx,
			`insert into tasks (run_id, workspace_id, step_key, attempt, status, started_at)
			 values ($1,$2,$3,$4,$5::task_status, now()) returning id`,
			runID, wsID, step, attempt, status).Scan(&id)
	}
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return id
}

// --- driving + querying --------------------------------------------------

// drive runs the event loop synchronously: advance the run, execute every
// task the advance left queued, and repeat until nothing is queued (the run
// has reached a terminal state or is waiting on nothing). Retry attempts land
// as new queued rows and are picked up on the next iteration; their River
// backoff schedule is irrelevant to direct invocation.
func drive(t *testing.T, e *Engine, runID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	adv := &advanceRunWorker{e: e}
	exe := &executeTaskWorker{e: e}
	for i := 0; i < 200; i++ {
		if err := adv.Work(ctx, &river.Job[AdvanceRunArgs]{Args: AdvanceRunArgs{RunID: runID}}); err != nil {
			t.Fatalf("advance: %v", err)
		}
		ids := queuedTaskIDs(t, e, runID)
		if len(ids) == 0 {
			return
		}
		for _, id := range ids {
			if err := exe.Work(ctx, &river.Job[ExecuteTaskArgs]{Args: ExecuteTaskArgs{TaskID: id}}); err != nil {
				t.Fatalf("execute %s: %v", id, err)
			}
		}
	}
	t.Fatalf("drive did not converge for run %s", runID)
}

func queuedTaskIDs(t *testing.T, e *Engine, runID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := e.Pool.Query(context.Background(),
		`select id from tasks where run_id=$1 and status='queued' order by queued_at, attempt`, runID)
	if err != nil {
		t.Fatalf("query queued: %v", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan queued: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func runStatus(t *testing.T, e *Engine, runID uuid.UUID) string {
	t.Helper()
	var status string
	if err := e.Pool.QueryRow(context.Background(),
		`select status::text from runs where id=$1`, runID).Scan(&status); err != nil {
		t.Fatalf("run status: %v", err)
	}
	return status
}

type taskRow struct {
	step    string
	attempt int
	status  string
}

func allTasks(t *testing.T, e *Engine, runID uuid.UUID) []taskRow {
	t.Helper()
	rows, err := e.Pool.Query(context.Background(),
		`select step_key, attempt, status::text from tasks where run_id=$1 order by step_key, attempt`, runID)
	if err != nil {
		t.Fatalf("all tasks: %v", err)
	}
	defer rows.Close()
	var out []taskRow
	for rows.Next() {
		var r taskRow
		if err := rows.Scan(&r.step, &r.attempt, &r.status); err != nil {
			t.Fatalf("scan task: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// taskID returns the id of a specific (step, attempt) task row.
func taskID(t *testing.T, e *Engine, runID uuid.UUID, step string, attempt int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.Pool.QueryRow(context.Background(),
		`select id from tasks where run_id=$1 and step_key=$2 and attempt=$3`, runID, step, attempt).Scan(&id); err != nil {
		t.Fatalf("task id for %s#%d: %v", step, attempt, err)
	}
	return id
}

func taskStatusByID(t *testing.T, e *Engine, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := e.Pool.QueryRow(context.Background(),
		`select status::text from tasks where id=$1`, id).Scan(&status); err != nil {
		t.Fatalf("task status: %v", err)
	}
	return status
}

// taskOutputNull reports whether a task's output column is SQL NULL.
func taskOutputNull(t *testing.T, e *Engine, id uuid.UUID) bool {
	t.Helper()
	var isNull bool
	if err := e.Pool.QueryRow(context.Background(),
		`select output is null from tasks where id=$1`, id).Scan(&isNull); err != nil {
		t.Fatalf("task output: %v", err)
	}
	return isNull
}

func countTaskAttempts(t *testing.T, e *Engine, runID uuid.UUID, step string) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(),
		`select count(*) from tasks where run_id=$1 and step_key=$2`, runID, step).Scan(&n); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	return n
}

func hasTask(t *testing.T, e *Engine, runID uuid.UUID, step string) bool {
	t.Helper()
	var exists bool
	if err := e.Pool.QueryRow(context.Background(),
		`select exists(select 1 from tasks where run_id=$1 and step_key=$2)`, runID, step).Scan(&exists); err != nil {
		t.Fatalf("has task: %v", err)
	}
	return exists
}

// --- fake executors ------------------------------------------------------

// recorder captures, in execution order, which step ran and the frozen
// TaskInput.Context it saw — the evidence for the DAG-ordering and
// context-scoping assertions.
type recorder struct {
	mu       sync.Mutex
	order    []string
	contexts map[string]map[string]any
}

func newRecorder() *recorder { return &recorder{contexts: map[string]map[string]any{}} }

func (r *recorder) note(step string, ctx map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, step)
	r.contexts[step] = ctx
}

func (r *recorder) orderOf(step string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.order {
		if s == step {
			return i
		}
	}
	return -1
}

// echoExec always succeeds, echoing its step key as output.
type echoExec struct{ rec *recorder }

func (e echoExec) Execute(_ context.Context, in steps.TaskInput) (steps.TaskOutput, error) {
	if e.rec != nil {
		e.rec.note(in.StepKey, in.Context)
	}
	return steps.TaskOutput{Data: map[string]any{"step": in.StepKey, "ok": true}}, nil
}

// failExec always fails.
type failExec struct{ rec *recorder }

func (e failExec) Execute(_ context.Context, in steps.TaskInput) (steps.TaskOutput, error) {
	if e.rec != nil {
		e.rec.note(in.StepKey, in.Context)
	}
	return steps.TaskOutput{}, fmt.Errorf("boom in %s", in.StepKey)
}

// flakyExec fails the first failUntil executions of each step key, then
// succeeds — used to exercise the retry ladder.
type flakyExec struct {
	mu        sync.Mutex
	calls     map[string]int
	failUntil int
}

func newFlaky(failUntil int) *flakyExec {
	return &flakyExec{calls: map[string]int{}, failUntil: failUntil}
}

func (e *flakyExec) Execute(_ context.Context, in steps.TaskInput) (steps.TaskOutput, error) {
	e.mu.Lock()
	e.calls[in.StepKey]++
	n := e.calls[in.StepKey]
	e.mu.Unlock()
	if n <= e.failUntil {
		return steps.TaskOutput{}, fmt.Errorf("flaky failure %d", n)
	}
	return steps.TaskOutput{Data: map[string]any{"succeeded_on_attempt": n}}, nil
}

func testRegistry(execs map[string]steps.Executor) *steps.Registry {
	r := steps.NewRegistry()
	for typ, ex := range execs {
		r.Register(steps.TypeInfo{Type: typ}, ex)
	}
	return r
}
