package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dop251/goja"
)

// --- http.request ---

type HTTPRequest struct{}

func (e *HTTPRequest) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	url, _ := in.Config["url"].(string)
	if url == "" {
		return TaskOutput{}, fmt.Errorf("http.request: url is required")
	}
	method, _ := in.Config["method"].(string)
	if method == "" {
		method = "GET"
	}

	var body io.Reader
	if b, ok := in.Config["body"].(string); ok && b != "" {
		body = bytes.NewBufferString(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("http.request: %w", err)
	}
	req.Header.Set("User-Agent", "oarlock/0.1")
	if h, ok := in.Config["headers"].(string); ok && h != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(h), &headers); err != nil {
			return TaskOutput{}, fmt.Errorf("http.request: invalid headers JSON: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("http.request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB cap
	if err != nil {
		return TaskOutput{}, fmt.Errorf("http.request: read body: %w", err)
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		parsed = string(raw)
	}
	out := map[string]any{"status": resp.StatusCode, "body": parsed}
	if resp.StatusCode >= 400 {
		return TaskOutput{Data: out}, fmt.Errorf("http.request: %s %s returned %d", method, url, resp.StatusCode)
	}
	in.Log.Info("http request done", "method", method, "url", url, "status", resp.StatusCode)
	return TaskOutput{Data: out}, nil
}

// --- transform (goja) ---

type Transform struct{}

func (e *Transform) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	script, _ := in.Config["script"].(string)
	if script == "" {
		return TaskOutput{}, fmt.Errorf("transform: script is required")
	}
	v, err := runJS(ctx, script, in.Context, 5*time.Second)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("transform: %w", err)
	}
	return TaskOutput{Data: v}, nil
}

// --- delay ---
// Delay suspends instead of blocking: Execute writes a suspensions row with a
// scheduled resume and frees the worker slot; Resume returns once the schedule
// fires (hard rule 1 — no worker parked on a wait). Arbitrary durations are fine.

type Delay struct{}

func (e *Delay) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	seconds := toFloat(in.Config["seconds"])
	if seconds <= 0 {
		return TaskOutput{}, fmt.Errorf("delay: seconds must be > 0")
	}
	resumeAt := time.Now().Add(time.Duration(seconds * float64(time.Second)))
	return TaskOutput{}, SuspendNow("delay", resumeAt, map[string]any{"waited_seconds": seconds})
}

func (e *Delay) Resume(ctx context.Context, in TaskInput, s SuspensionState) (TaskOutput, error) {
	return TaskOutput{Data: map[string]any{"waited_seconds": s.Payload["waited_seconds"]}}, nil
}

// --- shared JS runtime helpers ---

// runJS executes a script with the run context bound as globals (`input`,
// `steps`). The script body may use `return`; it is wrapped in a function.
func runJS(ctx context.Context, script string, runContext map[string]any, limit time.Duration) (any, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	for k, v := range runContext {
		if err := vm.Set(k, v); err != nil {
			return nil, err
		}
	}

	timer := time.AfterFunc(limit, func() { vm.Interrupt("time limit exceeded") })
	defer timer.Stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt("canceled")
		case <-done:
		}
	}()

	v, err := vm.RunString("(function(){\n" + script + "\n})()")
	if err != nil {
		return nil, err
	}
	return v.Export(), nil
}

// EvalExpression evaluates a single JS expression (used for {{ }} interpolation).
func EvalExpression(ctx context.Context, expr string, runContext map[string]any) (any, error) {
	return runJS(ctx, "return ("+expr+")", runContext, 2*time.Second)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}
