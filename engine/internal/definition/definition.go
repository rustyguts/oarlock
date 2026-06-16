// Package definition implements the canonical workflow definition format v0.
// The JSON document stored in workflow_versions.definition is the only
// canonical workflow artifact (design hard rule 4); the canvas and any future
// YAML/SDK views are projections of it.
package definition

import (
	"encoding/json"
	"fmt"
)

// ConditionType is the built-in branching step. The definition and engine
// layers identify conditions by this type name to decide skip propagation; the
// executor (package steps) owns the {result, branch} output shape. Kept here so
// the validator and advance_run don't depend on the steps registry.
const ConditionType = "condition"

type Definition struct {
	Name  string `json:"name,omitempty"`
	Steps []Step `json:"steps"`
}

type Step struct {
	Key   string   `json:"key"`
	Type  string   `json:"type"`
	Needs []string `json:"needs,omitempty"`
	// Branches maps a condition predecessor key (which must be in Needs) to the
	// branch label ("then"/"else") this step is wired to. Absent on every
	// non-branching definition, so it changes nothing for existing workflows.
	// Derived from the canvas edge's sourceHandle.
	Branches map[string]string `json:"branches,omitempty"`
	Config   map[string]any    `json:"config,omitempty"`
	Retries  int               `json:"retries,omitempty"` // extra attempts after a failure (0–10)
	UI       *StepUI           `json:"ui,omitempty"`      // canvas position; engine ignores it
}

type StepUI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func Parse(raw []byte) (*Definition, error) {
	var d Definition
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("invalid definition JSON: %w", err)
	}
	return &d, nil
}

func (d *Definition) Step(key string) *Step {
	for i := range d.Steps {
		if d.Steps[i].Key == key {
			return &d.Steps[i]
		}
	}
	return nil
}

// Validate checks structural invariants: unique non-empty keys, known step
// types, needs referencing existing steps, and an acyclic graph.
func (d *Definition) Validate(knownTypes func(string) bool) error {
	keys := make(map[string]bool, len(d.Steps))
	for _, s := range d.Steps {
		if s.Key == "" {
			return fmt.Errorf("step with empty key")
		}
		if keys[s.Key] {
			return fmt.Errorf("duplicate step key %q", s.Key)
		}
		keys[s.Key] = true
		if s.Type == "" {
			return fmt.Errorf("step %q has no type", s.Key)
		}
		if knownTypes != nil && !knownTypes(s.Type) {
			return fmt.Errorf("step %q has unknown type %q", s.Key, s.Type)
		}
		if s.Retries < 0 || s.Retries > 10 {
			return fmt.Errorf("step %q retries must be 0–10", s.Key)
		}
	}
	for _, s := range d.Steps {
		for _, n := range s.Needs {
			if !keys[n] {
				return fmt.Errorf("step %q needs unknown step %q", s.Key, n)
			}
			if n == s.Key {
				return fmt.Errorf("step %q needs itself", s.Key)
			}
		}
		// Branch wiring: each label must hang off a needed condition predecessor.
		// Acyclicity is unaffected — branches ride on existing needs, which
		// checkAcyclic already walks.
		if len(s.Branches) > 0 {
			needed := make(map[string]bool, len(s.Needs))
			for _, n := range s.Needs {
				needed[n] = true
			}
			for pred, label := range s.Branches {
				if !needed[pred] {
					return fmt.Errorf("step %q branches on %q which is not in its needs", s.Key, pred)
				}
				if p := d.Step(pred); p == nil || p.Type != ConditionType {
					return fmt.Errorf("step %q branches on %q which is not a %s step", s.Key, pred, ConditionType)
				}
				if label != "then" && label != "else" {
					return fmt.Errorf("step %q has invalid branch label %q for %q (want then|else)", s.Key, label, pred)
				}
			}
		}
	}
	return d.checkAcyclic()
}

func (d *Definition) checkAcyclic() error {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[string]int, len(d.Steps))
	var visit func(key string) error
	visit = func(key string) error {
		switch color[key] {
		case gray:
			return fmt.Errorf("cycle detected involving step %q", key)
		case black:
			return nil
		}
		color[key] = gray
		for _, n := range d.Step(key).Needs {
			if err := visit(n); err != nil {
				return err
			}
		}
		color[key] = black
		return nil
	}
	for _, s := range d.Steps {
		if err := visit(s.Key); err != nil {
			return err
		}
	}
	return nil
}
