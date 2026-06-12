package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxLogLine  = 8 << 10  // 8KB per line, truncated
	maxTaskLogs = 1 << 20  // ~1MB per task, then dropped
)

// taskLogHandler is the default log sink for every task: lines go to the
// task_logs table (capped) and tee to the process logger. Executors receive
// it via TaskInput.Log, and the engine writes lifecycle lines through it, so
// every task gets a log trail with no explicit log step.
type taskLogHandler struct {
	pool    *pgxpool.Pool
	tee     slog.Handler
	notify  func()
	redact  *redactor // nil-safe; secrets never reach the log table or stdout
	ws      uuid.UUID
	run     uuid.UUID
	task    uuid.UUID
	stepKey string
	attrs   []slog.Attr
	written *atomic.Int64
	capped  *atomic.Bool
}

// taskLogger builds the per-task slog.Logger. notify fires after each insert
// so SSE subscribers refresh.
func (e *Engine) taskLogger(t taskRef) *slog.Logger {
	h := &taskLogHandler{
		pool:    e.Pool,
		tee:     e.Log.With("run_id", t.runID, "step", t.stepKey, "attempt", t.attempt).Handler(),
		notify:  func() { e.notify(context.Background(), t.runID) },
		redact:  t.redact,
		ws:      t.workspaceID,
		run:     t.runID,
		task:    t.id,
		stepKey: t.stepKey,
		written: &atomic.Int64{},
		capped:  &atomic.Bool{},
	}
	return slog.New(h)
}

func (h *taskLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *taskLogHandler) Handle(ctx context.Context, r slog.Record) error {
	msg := h.redact.String(r.Message)
	if len(msg) > maxLogLine {
		msg = msg[:maxLogLine] + "…(truncated)"
	}

	fields := map[string]any{}
	for _, a := range h.attrs {
		fields[a.Key] = a.Value.Resolve().Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.Resolve().Any()
		return true
	})
	var fieldsJSON []byte
	if len(fields) > 0 {
		fieldsJSON, _ = json.Marshal(fields)
		fieldsJSON = h.redact.JSON(fieldsJSON)
		if len(fieldsJSON) > maxLogLine {
			fieldsJSON, _ = json.Marshal(map[string]string{"truncated": "fields exceeded 8KB"})
		}
	}

	// Tee a redacted record to the process log — the original record may
	// contain secret material and must not reach stdout either.
	tee := slog.NewRecord(r.Time, r.Level, msg, r.PC)
	if len(fieldsJSON) > 0 {
		tee.AddAttrs(slog.String("fields", string(fieldsJSON)))
	}
	_ = h.tee.Handle(ctx, tee)

	total := h.written.Add(int64(len(msg) + len(fieldsJSON)))
	if total > maxTaskLogs {
		if h.capped.CompareAndSwap(false, true) {
			h.insert(slog.LevelWarn, "log cap reached (~1MB); further lines dropped", nil)
		}
		return nil
	}

	h.insert(r.Level, msg, fieldsJSON)
	return nil
}

// insert is best-effort with its own timeout: losing a log line must never
// fail a task, and a canceled task context must not lose the final lines.
func (h *taskLogHandler) insert(level slog.Level, msg string, fields []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := h.pool.Exec(ctx, `
		insert into task_logs (workspace_id, run_id, task_id, step_key, level, message, fields)
		values ($1, $2, $3, $4, $5, $6, $7)`,
		h.ws, h.run, h.task, h.stepKey, int(level), msg, fields)
	if err == nil && h.notify != nil {
		h.notify()
	}
}

func (h *taskLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.tee = h.tee.WithAttrs(attrs)
	c.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &c
}

func (h *taskLogHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.tee = h.tee.WithGroup(name)
	return &c
}
