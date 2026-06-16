package engine

import (
	"sort"
	"testing"

	"github.com/rustyguts/oarlock/engine/internal/definition"
)

// cond/step are tiny builders for branch-aware definitions.
func cond(key string, needs ...string) definition.Step {
	return definition.Step{Key: key, Type: definition.ConditionType, Needs: needs}
}
func step(key string, needs ...string) definition.Step {
	return definition.Step{Key: key, Type: "transform", Needs: needs}
}
func branch(s definition.Step, on, label string) definition.Step {
	if s.Branches == nil {
		s.Branches = map[string]string{}
	}
	s.Branches[on] = label
	return s
}

func eq(t *testing.T, label string, got, want []string) {
	t.Helper()
	g, w := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	if len(g) != len(w) {
		t.Errorf("%s: got %v, want %v", label, got, want)
		return
	}
	for i := range g {
		if g[i] != w[i] {
			t.Errorf("%s: got %v, want %v", label, got, want)
			return
		}
	}
}

func TestComputePlanBackwardCompat(t *testing.T) {
	// A plain diamond with no branches behaves exactly as the old loop did.
	def := &definition.Definition{Steps: []definition.Step{
		step("a"), step("b", "a"), step("c", "a"), step("d", "b", "c"),
	}}
	p := computePlan(def, map[string]string{"a": "succeeded"}, nil)
	eq(t, "ready", p.ready, []string{"b", "c"})
	eq(t, "skip", p.skip, nil)
	if p.allSucceeded || p.anyFailed {
		t.Fatalf("unexpected terminal: %+v", p)
	}
}

func TestComputePlanIfElseThen(t *testing.T) {
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
	}}
	p := computePlan(def, map[string]string{"c": "succeeded"}, map[string]string{"c": "then"})
	eq(t, "ready", p.ready, []string{"t"})
	eq(t, "skip", p.skip, []string{"e"})
}

func TestComputePlanIfElseElse(t *testing.T) {
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
	}}
	p := computePlan(def, map[string]string{"c": "succeeded"}, map[string]string{"c": "else"})
	eq(t, "ready", p.ready, []string{"e"})
	eq(t, "skip", p.skip, []string{"t"})
}

func TestComputePlanInBranchChainSkips(t *testing.T) {
	// The whole dead branch (a -> b) collapses, not just the direct target.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("a", "c"), "c", "then"),
		step("b", "a"),
		branch(step("e", "c"), "c", "else"),
	}}
	p := computePlan(def, map[string]string{"c": "succeeded"}, map[string]string{"c": "else"})
	eq(t, "ready", p.ready, []string{"e"})
	eq(t, "skip", p.skip, []string{"a", "b"})
}

func TestComputePlanJoinAfterIfElseFires(t *testing.T) {
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
		step("m", "t", "e"),
	}}
	// then taken, t done, e skipped → the join sees both deps satisfied.
	p := computePlan(def,
		map[string]string{"c": "succeeded", "t": "succeeded", "e": "skipped"},
		map[string]string{"c": "then"})
	eq(t, "ready", p.ready, []string{"m"})
	eq(t, "skip", p.skip, nil)
}

func TestComputePlanJoinWaitsForRunningSibling(t *testing.T) {
	// While the taken branch is still running, the join is neither ready nor
	// skipped (the all-settled gate / reachability keeps it alive).
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
		step("m", "t", "e"),
	}}
	p := computePlan(def,
		map[string]string{"c": "succeeded", "t": "running", "e": "skipped"},
		map[string]string{"c": "then"})
	eq(t, "ready", p.ready, nil)
	eq(t, "skip", p.skip, nil)
	if p.allSucceeded {
		t.Fatal("must not be all-succeeded while t runs")
	}
}

func TestComputePlanNestedConditionSkipsInOnePass(t *testing.T) {
	// B1: outer chooses else; the nested condition `inner` (on outer's then
	// branch) and its grandchildren g1/g2 must all skip in a single computePlan
	// call via the internal fixpoint — no row exists for `inner` yet.
	def := &definition.Definition{Steps: []definition.Step{
		cond("outer"),
		branch(cond("inner", "outer"), "outer", "then"),
		branch(step("other", "outer"), "outer", "else"),
		branch(step("g1", "inner"), "inner", "then"),
		branch(step("g2", "inner"), "inner", "else"),
	}}
	p := computePlan(def, map[string]string{"outer": "succeeded"}, map[string]string{"outer": "else"})
	eq(t, "ready", p.ready, []string{"other"})
	eq(t, "skip", p.skip, []string{"inner", "g1", "g2"})
}

func TestComputePlanCrossEdgeDoesNotReviveDeadBranch(t *testing.T) {
	// B2: an independent live edge (x -> e) must NOT keep a not-taken branch
	// target alive. e is branch-dead because its only labeled in-edge is pruned.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		step("x"),
		branch(step("e", "c", "x"), "c", "else"),
	}}
	p := computePlan(def,
		map[string]string{"c": "succeeded", "x": "succeeded"},
		map[string]string{"c": "then"})
	eq(t, "skip", p.skip, []string{"e"})
	eq(t, "ready", p.ready, nil)
	if !p.allSucceeded {
		t.Fatalf("c+x succeeded, e skipped → run should be all-succeeded, got %+v", p)
	}
}

func TestComputePlanBranchStepKeepsPrepDep(t *testing.T) {
	// A branch target may also have a plain prep dependency. On the taken path
	// it waits for the prep; on the untaken path it skips regardless of the prep.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		step("prep"),
		branch(step("t", "c", "prep"), "c", "then"),
	}}
	taken := computePlan(def,
		map[string]string{"c": "succeeded", "prep": "succeeded"},
		map[string]string{"c": "then"})
	eq(t, "ready(taken)", taken.ready, []string{"t"})
	eq(t, "skip(taken)", taken.skip, nil)

	dead := computePlan(def,
		map[string]string{"c": "succeeded", "prep": "succeeded"},
		map[string]string{"c": "else"})
	eq(t, "ready(dead)", dead.ready, nil)
	eq(t, "skip(dead)", dead.skip, []string{"t"})
}

func TestComputePlanUndecidedConditionPrunesNothing(t *testing.T) {
	// Before the condition runs, it's just a ready root; nothing is pruned.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
	}}
	p := computePlan(def, nil, nil)
	eq(t, "ready", p.ready, []string{"c"})
	eq(t, "skip", p.skip, nil)
}

func TestComputePlanOneSidedCondition(t *testing.T) {
	// A condition with only a Then consumer is valid: else just skips it.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
	}}
	p := computePlan(def, map[string]string{"c": "succeeded"}, map[string]string{"c": "else"})
	eq(t, "skip", p.skip, []string{"t"})
	if !p.allSucceeded {
		t.Fatalf("c succeeded, t skipped → all-succeeded, got %+v", p)
	}
}

func TestComputePlanAnyFailed(t *testing.T) {
	def := &definition.Definition{Steps: []definition.Step{cond("c"), step("t", "c")}}
	p := computePlan(def, map[string]string{"c": "failed"}, nil)
	if !p.anyFailed {
		t.Fatal("expected anyFailed")
	}
}

func TestComputePlanUnreadableBranchRunsBothSides(t *testing.T) {
	// Graceful degradation: a succeeded condition with no decoded branch
	// (branchChoice absent) prunes nothing — both sides run, no deadlock.
	def := &definition.Definition{Steps: []definition.Step{
		cond("c"),
		branch(step("t", "c"), "c", "then"),
		branch(step("e", "c"), "c", "else"),
	}}
	p := computePlan(def, map[string]string{"c": "succeeded"}, nil)
	eq(t, "ready", p.ready, []string{"t", "e"})
	eq(t, "skip", p.skip, nil)
}
