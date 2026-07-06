// Package definition implements the canonical workflow definition format v0.
// The JSON document stored in workflow_versions.definition is the only
// canonical workflow artifact (design hard rule 4); the canvas and any future
// YAML/SDK views are projections of it.
package definition

import (
	"encoding/json"
	"fmt"
)

type Definition struct {
	Name  string `json:"name,omitempty"`
	Steps []Step `json:"steps"`
}

type Step struct {
	Key   string   `json:"key"`
	Type  string   `json:"type"`
	Needs []string `json:"needs,omitempty"`
	// If is a JS guard expression evaluated against {input, steps.*} (upstream
	// outputs only, no secrets); empty = always run. A falsy result skips the
	// step; not syntax-checked at save, so a runtime eval error fails the run.
	If      string         `json:"if,omitempty"`
	Config  map[string]any `json:"config,omitempty"`
	Retries int            `json:"retries,omitempty"` // extra attempts after a failure (0–10)
	Timeout int            `json:"timeout,omitempty"` // seconds; 0 = executor default (0–600)
	UI      *StepUI        `json:"ui,omitempty"`      // canvas position; engine ignores it
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

// TransitiveNeeds returns the set of step keys in key's transitive needs
// closure (its dependencies, their dependencies, and so on), excluding key
// itself. A step's expression context may reference only these upstream
// outputs, so parallel siblings never leak in by completion order. An unknown
// key yields an empty set; assumes an acyclic graph (guaranteed by Validate).
func (d *Definition) TransitiveNeeds(key string) map[string]bool {
	seen := map[string]bool{}
	var visit func(k string)
	visit = func(k string) {
		step := d.Step(k)
		if step == nil {
			return
		}
		for _, n := range step.Needs {
			if !seen[n] {
				seen[n] = true
				visit(n)
			}
		}
	}
	visit(key)
	return seen
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
		if s.Timeout < 0 || s.Timeout > 600 {
			return fmt.Errorf("step %q timeout must be 0–600 seconds", s.Key)
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
