package definition

import "testing"

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
