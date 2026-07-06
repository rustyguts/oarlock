package steps

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureHandler is a minimal slog.Handler that records every emitted record so
// tests can assert what console.* wrote.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.records {
		out = append(out, r.Message)
	}
	return out
}

// TestRunCodeJSReturnsValue: the script's return value is exported as the task
// output, matching runJS semantics.
func TestRunCodeJSReturnsValue(t *testing.T) {
	v, err := runCodeJS(context.Background(), "return steps.a.count * 2", map[string]any{
		"steps": map[string]any{"a": map[string]any{"count": float64(21)}},
	}, nil, time.Second)
	if err != nil {
		t.Fatalf("runCodeJS: %v", err)
	}
	switch n := v.(type) {
	case int64:
		if n != 42 {
			t.Fatalf("got %d, want 42", n)
		}
	case float64:
		if n != 42 {
			t.Fatalf("got %v, want 42", n)
		}
	default:
		t.Fatalf("unexpected result type %T", v)
	}
}

// TestRunCodeJSConsoleReachesLog: each console.* method writes one line to the
// bound logger at the matching level; objects are stringified as JSON and
// multiple args joined by spaces.
func TestRunCodeJSConsoleReachesLog(t *testing.T) {
	h := &captureHandler{}
	log := slog.New(h)
	script := `
		console.log("hello", {a: 1});
		console.info("i");
		console.warn("w");
		console.error("e");
		return 1
	`
	if _, err := runCodeJS(context.Background(), script, map[string]any{}, log, time.Second); err != nil {
		t.Fatalf("runCodeJS: %v", err)
	}

	msgs := h.messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 log lines, got %d: %v", len(msgs), msgs)
	}
	if msgs[0] != `hello {"a":1}` {
		t.Fatalf("console.log line = %q, want %q", msgs[0], `hello {"a":1}`)
	}

	// Levels must map: log/info→Info, warn→Warn, error→Error.
	wantLevel := map[string]slog.Level{"hello {\"a\":1}": slog.LevelInfo, "i": slog.LevelInfo, "w": slog.LevelWarn, "e": slog.LevelError}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if want, ok := wantLevel[r.Message]; ok && r.Level != want {
			t.Fatalf("line %q at level %v, want %v", r.Message, r.Level, want)
		}
	}
}

// TestRunCodeJSTimeLimit: a runaway script is killed by the interpreter's
// interrupt, returning an error rather than hanging the worker.
func TestRunCodeJSTimeLimit(t *testing.T) {
	done := make(chan struct{})
	var err error
	go func() {
		_, err = runCodeJS(context.Background(), "while(true){}", map[string]any{}, nil, 100*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCodeJS did not honor its time limit (hung)")
	}
	if err == nil || !strings.Contains(err.Error(), "time limit exceeded") {
		t.Fatalf("expected time-limit interrupt, got %v", err)
	}
}

// TestRunCodeJSContextCancel: a canceled task context interrupts the script.
func TestRunCodeJSContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	if _, err := runCodeJS(ctx, "while(true){}", map[string]any{}, nil, time.Minute); err == nil {
		t.Fatal("expected interrupt from canceled context")
	}
}

// TestCodeJSExecuteRequiresScript: an empty script is a config error.
func TestCodeJSExecuteRequiresScript(t *testing.T) {
	e := &CodeJS{}
	if _, err := e.Execute(context.Background(), TaskInput{Config: map[string]any{}}); err == nil {
		t.Fatal("expected error for missing script")
	}
}
