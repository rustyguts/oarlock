package definition

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func known(string) bool { return true }

func def(steps ...Step) *Definition { return &Definition{Steps: steps} }

func TestValidateDiamond(t *testing.T) {
	d := def(
		Step{Key: "a", Type: "log"},
		Step{Key: "b", Type: "log", Needs: []string{"a"}},
		Step{Key: "c", Type: "log", Needs: []string{"a"}},
		Step{Key: "d", Type: "log", Needs: []string{"b", "c"}},
	)
	if err := d.Validate(known); err != nil {
		t.Fatalf("diamond should validate: %v", err)
	}
}

func TestValidateCycle(t *testing.T) {
	d := def(
		Step{Key: "a", Type: "log", Needs: []string{"c"}},
		Step{Key: "b", Type: "log", Needs: []string{"a"}},
		Step{Key: "c", Type: "log", Needs: []string{"b"}},
	)
	if err := d.Validate(known); err == nil {
		t.Fatal("cycle should fail validation")
	}
}

func TestValidateSelfNeed(t *testing.T) {
	d := def(Step{Key: "a", Type: "log", Needs: []string{"a"}})
	if err := d.Validate(known); err == nil {
		t.Fatal("self-need should fail validation")
	}
}

func TestValidateDuplicateKey(t *testing.T) {
	d := def(Step{Key: "a", Type: "log"}, Step{Key: "a", Type: "log"})
	if err := d.Validate(known); err == nil {
		t.Fatal("duplicate key should fail validation")
	}
}

func TestValidateUnknownNeed(t *testing.T) {
	d := def(Step{Key: "a", Type: "log", Needs: []string{"ghost"}})
	if err := d.Validate(known); err == nil {
		t.Fatal("unknown need should fail validation")
	}
}

func TestValidateUnknownType(t *testing.T) {
	d := def(Step{Key: "a", Type: "nope"})
	if err := d.Validate(func(string) bool { return false }); err == nil {
		t.Fatal("unknown type should fail validation")
	}
}

func TestValidateTimeout(t *testing.T) {
	if err := def(Step{Key: "a", Type: "log", Timeout: 600}).Validate(known); err != nil {
		t.Fatalf("timeout 600 should validate: %v", err)
	}
	if err := def(Step{Key: "a", Type: "log", Timeout: 601}).Validate(known); err == nil {
		t.Fatal("timeout 601 should fail validation")
	}
	if err := def(Step{Key: "a", Type: "log", Timeout: -1}).Validate(known); err == nil {
		t.Fatal("negative timeout should fail validation")
	}
}

// TestIfRoundTrips: the `if` guard survives Parse→marshal unchanged, and an
// empty guard is omitted from the JSON (omitempty). No validation rule governs
// `if` — a step carrying one still validates; a runtime eval error is the only
// failure mode.
func TestIfRoundTrips(t *testing.T) {
	raw := `{"steps":[{"key":"a","type":"log"},{"key":"b","type":"log","needs":["a"],"if":"steps.a.count > 5"}]}`
	d, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := d.Step("b").If; got != "steps.a.count > 5" {
		t.Fatalf("if = %q, want %q", got, "steps.a.count > 5")
	}
	if err := d.Validate(known); err != nil {
		t.Fatalf("a step with an if guard must still validate: %v", err)
	}

	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Re-parse the marshaled form: the guard must survive the round trip
	// unchanged (json escapes `>`, so compare structurally, not by substring).
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got := back.Step("b").If; got != "steps.a.count > 5" {
		t.Fatalf("round-tripped if = %q, want %q", got, "steps.a.count > 5")
	}
	// An empty guard is omitted entirely (omitempty).
	bare, err := json.Marshal(Step{Key: "a", Type: "log"})
	if err != nil {
		t.Fatalf("marshal bare: %v", err)
	}
	if strings.Contains(string(bare), `"if"`) {
		t.Fatalf("step with no guard should omit if: %s", bare)
	}
}

func TestTransitiveNeeds(t *testing.T) {
	// chain a→b→c→d (d needs c needs b needs a).
	chain := def(
		Step{Key: "a", Type: "log"},
		Step{Key: "b", Type: "log", Needs: []string{"a"}},
		Step{Key: "c", Type: "log", Needs: []string{"b"}},
		Step{Key: "d", Type: "log", Needs: []string{"c"}},
	)
	// diamond: d needs b and c, both need a.
	diamond := def(
		Step{Key: "a", Type: "log"},
		Step{Key: "b", Type: "log", Needs: []string{"a"}},
		Step{Key: "c", Type: "log", Needs: []string{"a"}},
		Step{Key: "d", Type: "log", Needs: []string{"b", "c"}},
	)
	// two disjoint branches sharing no ancestors.
	disjoint := def(
		Step{Key: "a", Type: "log"},
		Step{Key: "b", Type: "log", Needs: []string{"a"}},
		Step{Key: "x", Type: "log"},
		Step{Key: "y", Type: "log", Needs: []string{"x"}},
	)

	cases := []struct {
		name string
		d    *Definition
		key  string
		want []string
	}{
		{"chain leaf", chain, "d", []string{"a", "b", "c"}},
		{"chain middle", chain, "b", []string{"a"}},
		{"chain root", chain, "a", nil},
		{"diamond leaf", diamond, "d", []string{"a", "b", "c"}},
		{"disjoint isolates branch", disjoint, "y", []string{"x"}},
		{"missing key", chain, "ghost", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.d.TransitiveNeeds(tc.key)
			var keys []string
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if strings.Join(keys, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("TransitiveNeeds(%q) = %v, want %v", tc.key, keys, tc.want)
			}
		})
	}
}
