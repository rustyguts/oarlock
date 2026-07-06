package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// interpolateConfig resolves {{ expr }} against the frozen run context. These
// tests pin the two shapes the engine depends on: a value that is exactly one
// expression keeps the expression's native Go type, while mixed text
// stringifies each result. Non-string config values pass straight through.

func TestInterpolateSingleExpressionKeepsNativeType(t *testing.T) {
	ctx := context.Background()
	runContext := map[string]any{
		"input": map[string]any{
			"n":    float64(42),
			"obj":  map[string]any{"a": float64(1)},
			"flag": true,
		},
		"steps": map[string]any{},
	}
	out, err := interpolateConfig(ctx, map[string]any{
		"n":    "{{ input.n }}",
		"obj":  "{{ input.obj }}",
		"flag": "{{ input.flag }}",
	}, runContext)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}

	// Number stays a native numeric (goja may export int64 or float64).
	switch v := out["n"].(type) {
	case float64:
		if v != 42 {
			t.Fatalf("n = %v, want 42", v)
		}
	case int64:
		if v != 42 {
			t.Fatalf("n = %v, want 42", v)
		}
	default:
		t.Fatalf("n not native numeric, got %T", out["n"])
	}

	obj, ok := out["obj"].(map[string]any)
	if !ok {
		t.Fatalf("obj not a native map, got %T", out["obj"])
	}
	if !reflect.DeepEqual(obj, map[string]any{"a": float64(1)}) {
		t.Fatalf("obj = %#v, want {a:1}", obj)
	}

	if b, ok := out["flag"].(bool); !ok || b != true {
		t.Fatalf("flag = %#v, want native bool true", out["flag"])
	}
}

func TestInterpolateMixedTextStringifies(t *testing.T) {
	ctx := context.Background()
	runContext := map[string]any{
		"input": map[string]any{"n": float64(42), "name": "bob"},
		"steps": map[string]any{},
	}
	out, err := interpolateConfig(ctx, map[string]any{
		"num_in_text": "n={{ input.n }}!",
		"str_in_text": "hi {{ input.name }}",
		"two_exprs":   "{{ input.name }}={{ input.n }}",
	}, runContext)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if out["num_in_text"] != "n=42!" {
		t.Fatalf("num_in_text = %q, want %q", out["num_in_text"], "n=42!")
	}
	if out["str_in_text"] != "hi bob" {
		t.Fatalf("str_in_text = %q, want %q", out["str_in_text"], "hi bob")
	}
	if out["two_exprs"] != "bob=42" {
		t.Fatalf("two_exprs = %q, want %q", out["two_exprs"], "bob=42")
	}
}

func TestInterpolateNonStringPassthrough(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]any{
		"num":  7,
		"list": []any{float64(1), float64(2)},
		"flag": true,
	}
	out, err := interpolateConfig(ctx, cfg, map[string]any{})
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if out["num"] != 7 {
		t.Fatalf("num = %#v, want 7", out["num"])
	}
	if !reflect.DeepEqual(out["list"], []any{float64(1), float64(2)}) {
		t.Fatalf("list = %#v", out["list"])
	}
	if out["flag"] != true {
		t.Fatalf("flag = %#v, want true", out["flag"])
	}
}

func TestInterpolateStringWithoutExpressionPassthrough(t *testing.T) {
	ctx := context.Background()
	out, err := interpolateConfig(ctx, map[string]any{"plain": "no braces here"}, map[string]any{})
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if out["plain"] != "no braces here" {
		t.Fatalf("plain = %q", out["plain"])
	}
}

func TestInterpolateSecretResolution(t *testing.T) {
	ctx := context.Background()
	runContext := map[string]any{
		"input":   nil,
		"steps":   map[string]any{},
		"secrets": map[string]any{"tok": "abc123"},
	}
	out, err := interpolateConfig(ctx, map[string]any{"auth": "{{ secrets.tok }}"}, runContext)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if out["auth"] != "abc123" {
		t.Fatalf("auth = %q, want abc123", out["auth"])
	}
}

func TestInterpolateEvalErrorCarriesConfigKey(t *testing.T) {
	ctx := context.Background()
	runContext := map[string]any{"input": nil, "steps": map[string]any{}}

	// Single-expression path.
	if _, err := interpolateConfig(ctx, map[string]any{"bad": "{{ undefinedVar }}"}, runContext); err == nil {
		t.Fatal("expected error for undefined reference (single expr)")
	} else if !strings.Contains(err.Error(), `config "bad"`) {
		t.Fatalf("error should name the config key, got %v", err)
	}

	// Mixed-text path.
	if _, err := interpolateConfig(ctx, map[string]any{"bad2": "x {{ undefinedVar }} y"}, runContext); err == nil {
		t.Fatal("expected error for undefined reference (mixed text)")
	} else if !strings.Contains(err.Error(), `config "bad2"`) {
		t.Fatalf("error should name the config key, got %v", err)
	}
}

// TestInterpolateUnknownReference documents goja's behavior for a reference
// into missing step output: `steps.missing` is `undefined`, which goja exports
// as nil (not an error). A single expression yields a nil value; in mixed text
// it stringifies to the empty string. (A deeper access like `steps.missing.x`
// would instead raise a TypeError — covered by the eval-error test's pattern.)
func TestInterpolateUnknownReference(t *testing.T) {
	ctx := context.Background()
	runContext := map[string]any{"input": nil, "steps": map[string]any{}}
	out, err := interpolateConfig(ctx, map[string]any{
		"single": "{{ steps.missing }}",
		"mixed":  "[{{ steps.missing }}]",
	}, runContext)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if out["single"] != nil {
		t.Fatalf("single = %#v, want nil", out["single"])
	}
	if out["mixed"] != "[]" {
		t.Fatalf("mixed = %q, want %q", out["mixed"], "[]")
	}
}
