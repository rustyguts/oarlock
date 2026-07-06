package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// --- code.js ---
//
// Like transform, but hardened for real scripts: a 30s time limit and a
// console object (log/info/warn/error) whose output is written to the task log
// at the matching level. It uses its own goja runtime (runCodeJS) rather than
// the shared runJS so the console binding and longer limit stay local here; the
// interrupt/watchdog mechanics (ctx-cancel + time-limit interrupt) are identical.

type CodeJS struct{}

const codeJSLimit = 30 * time.Second

func (e *CodeJS) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	script, _ := in.Config["script"].(string)
	if script == "" {
		return TaskOutput{}, fmt.Errorf("code.js: script is required")
	}
	v, err := runCodeJS(ctx, script, in.Context, in.Log, codeJSLimit)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("code.js: %w", err)
	}
	return TaskOutput{Data: v}, nil
}

// runCodeJS executes a script with the run context bound as globals (`input`,
// `steps`) plus a `console` object that writes through log. The script body may
// use `return`; it is wrapped in a function. Mirrors runJS's interrupt and
// watchdog behavior exactly.
func runCodeJS(ctx context.Context, script string, runContext map[string]any, log *slog.Logger, limit time.Duration) (any, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	for k, v := range runContext {
		if err := vm.Set(k, v); err != nil {
			return nil, err
		}
	}
	if err := vm.Set("console", newConsole(vm, log)); err != nil {
		return nil, err
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

// newConsole builds a console object whose log/info/warn/error each stringify
// their arguments (objects/arrays as JSON, joined by spaces) and write one line
// to log at the matching slog level. A nil log makes them no-ops.
func newConsole(vm *goja.Runtime, log *slog.Logger) *goja.Object {
	obj := vm.NewObject()
	set := func(name string, level slog.Level) {
		_ = obj.Set(name, func(call goja.FunctionCall) goja.Value {
			if log != nil {
				parts := make([]string, len(call.Arguments))
				for i, a := range call.Arguments {
					parts[i] = consoleArg(a)
				}
				log.Log(context.Background(), level, strings.Join(parts, " "))
			}
			return goja.Undefined()
		})
	}
	set("log", slog.LevelInfo)
	set("info", slog.LevelInfo)
	set("warn", slog.LevelWarn)
	set("error", slog.LevelError)
	return obj
}

// consoleArg stringifies a single console argument: objects and arrays render
// as compact JSON, everything else via goja's own String().
func consoleArg(v goja.Value) string {
	if v == nil {
		return "undefined"
	}
	switch v.Export().(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(v.Export()); err == nil {
			return string(b)
		}
	}
	return v.String()
}
