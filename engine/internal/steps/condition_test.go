package steps

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func runCondition(t *testing.T, config, ctxData map[string]any) (bool, string) {
	t.Helper()
	in := TaskInput{
		Config:  config,
		Context: ctxData,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	out, err := (&Condition{}).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("condition execute: %v", err)
	}
	m, ok := out.Data.(map[string]any)
	if !ok {
		t.Fatalf("output not a map: %#v", out.Data)
	}
	return m["result"].(bool), m["branch"].(string)
}

func TestConditionExpressionMode(t *testing.T) {
	ctx := map[string]any{"steps": map[string]any{"fetch": map[string]any{"count": float64(3)}}}
	res, br := runCondition(t, map[string]any{"mode": "expression", "expression": "steps.fetch.count > 0"}, ctx)
	if !res || br != "then" {
		t.Fatalf("want then/true, got %v/%s", res, br)
	}
}

func TestConditionRulesNumberAndCombinator(t *testing.T) {
	ctx := map[string]any{"input": map[string]any{"n": float64(5)}}
	cfg := map[string]any{
		"mode":       "rules",
		"combinator": "and",
		"rules": []any{
			map[string]any{"operand": "input.n", "operator": ">", "value": float64(0), "kind": "number"},
			map[string]any{"operand": "input.n", "operator": "<", "value": "10", "kind": "number"},
		},
	}
	res, br := runCondition(t, cfg, ctx)
	if !res || br != "then" {
		t.Fatalf("want then, got %v/%s", res, br)
	}
}

func TestConditionRulesOr(t *testing.T) {
	ctx := map[string]any{"input": map[string]any{"n": float64(-1)}}
	cfg := map[string]any{
		"mode":       "rules",
		"combinator": "or",
		"rules": []any{
			map[string]any{"operand": "input.n", "operator": ">", "value": float64(100), "kind": "number"},
			map[string]any{"operand": "input.n", "operator": "<", "value": float64(0), "kind": "number"},
		},
	}
	if res, _ := runCondition(t, cfg, ctx); !res {
		t.Fatal("or should be true")
	}
}

// A string value carrying quotes/backslashes must not break the generated JS —
// emitRHS JSON-encodes it. This is the injection/escaping guard.
func TestConditionRulesStringEscaping(t *testing.T) {
	ctx := map[string]any{"input": map[string]any{"s": `he said "hi"\n`}}
	cfg := map[string]any{
		"mode": "rules",
		"rules": []any{
			map[string]any{"operand": "input.s", "operator": "==", "value": `he said "hi"\n`, "kind": "string"},
		},
	}
	if res, _ := runCondition(t, cfg, ctx); !res {
		t.Fatal("escaped string equality should hold")
	}
}

func TestConditionRulesContainsAndExists(t *testing.T) {
	ctx := map[string]any{"steps": map[string]any{"fetch": map[string]any{
		"tags": []any{"a", "b", "c"},
		"name": "hello world",
	}}}
	contains := map[string]any{"mode": "rules", "rules": []any{
		map[string]any{"operand": "steps.fetch.tags", "operator": "contains", "value": "b", "kind": "string"},
	}}
	if res, _ := runCondition(t, contains, ctx); !res {
		t.Fatal("array contains should be true")
	}
	substr := map[string]any{"mode": "rules", "rules": []any{
		map[string]any{"operand": "steps.fetch.name", "operator": "contains", "value": "world", "kind": "string"},
	}}
	if res, _ := runCondition(t, substr, ctx); !res {
		t.Fatal("string contains should be true")
	}
	exists := map[string]any{"mode": "rules", "rules": []any{
		map[string]any{"operand": "steps.fetch.missing", "operator": "exists"},
	}}
	if res, _ := runCondition(t, exists, ctx); res {
		t.Fatal("missing field should not exist")
	}
}

func TestConditionRulesMatches(t *testing.T) {
	ctx := map[string]any{"input": map[string]any{"email": "a@b.com"}}
	cfg := map[string]any{"mode": "rules", "rules": []any{
		map[string]any{"operand": "input.email", "operator": "matches", "value": `^[^@]+@[^@]+$`, "kind": "string"},
	}}
	if res, _ := runCondition(t, cfg, ctx); !res {
		t.Fatal("regex should match")
	}
}

func TestConditionEmptyRulesErrors(t *testing.T) {
	in := TaskInput{
		Config: map[string]any{"mode": "rules", "rules": []any{}},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if _, err := (&Condition{}).Execute(context.Background(), in); err == nil {
		t.Fatal("empty rules should error")
	}
}

func TestConditionFalseRoutesElse(t *testing.T) {
	ctx := map[string]any{"input": map[string]any{"n": float64(0)}}
	cfg := map[string]any{"mode": "rules", "rules": []any{
		map[string]any{"operand": "input.n", "operator": ">", "value": float64(0), "kind": "number"},
	}}
	res, br := runCondition(t, cfg, ctx)
	if res || br != "else" {
		t.Fatalf("want else/false, got %v/%s", res, br)
	}
}
