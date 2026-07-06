package steps

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestToFloat(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want float64
	}{
		{"float64", float64(2.5), 2.5},
		{"int", 5, 5},
		{"numeric string", "3.5", 3.5},
		{"integer string", "42", 42},
		{"unparseable string", "not a number", 0},
		{"empty string", "", 0},
		{"nil", nil, 0},
		{"unsupported type bool", true, 0},
		{"unsupported type int64", int64(9), 0}, // only int is handled, not int64
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toFloat(tc.in); got != tc.want {
				t.Fatalf("toFloat(%#v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunJSTimeLimit is the interruption guarantee: a runaway script must be
// killed by the interpreter's interrupt, returning an error rather than
// hanging the worker.
func TestRunJSTimeLimit(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	var (
		err error
	)
	go func() {
		_, err = runJS(ctx, "while(true){}", map[string]any{}, 100*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runJS did not honor its time limit (hung)")
	}
	if err == nil {
		t.Fatal("expected an interrupt error from the runaway script")
	}
	if !strings.Contains(err.Error(), "time limit exceeded") {
		t.Fatalf("expected time-limit interrupt, got %v", err)
	}
}

func TestRunJSReturnsValue(t *testing.T) {
	ctx := context.Background()
	v, err := runJS(ctx, "return steps.a.count + 1", map[string]any{
		"steps": map[string]any{"a": map[string]any{"count": float64(4)}},
	}, time.Second)
	if err != nil {
		t.Fatalf("runJS: %v", err)
	}
	// goja exports an integral JS number as int64.
	switch n := v.(type) {
	case int64:
		if n != 5 {
			t.Fatalf("got %d, want 5", n)
		}
	case float64:
		if n != 5 {
			t.Fatalf("got %v, want 5", n)
		}
	default:
		t.Fatalf("unexpected result type %T", v)
	}
}

// TestRunJSContextCancel proves a canceled task context interrupts the script.
func TestRunJSContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	if _, err := runJS(ctx, "while(true){}", map[string]any{}, time.Minute); err == nil {
		t.Fatal("expected interrupt from canceled context")
	}
}
