package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// --- condition (If/Else, goja) ---
//
// A condition evaluates a boolean over prior step outputs and routes the run
// down its Then or Else path. The engine reads the decision back from the
// output to prune the untaken branch (see engine.computePlan); this executor
// only owns evaluation and the output shape. It reuses the same sandboxed goja
// runtime as transform/interpolation — same trust model, 2s budget.
//
// Output: {"result": <bool>, "branch": "then"|"else"}. `result` is the source
// of truth the engine derives the branch from; `branch` is the human/UI label.

type Condition struct{}

// conditionPrelude defines helpers available to the compiled expression. It is
// a set of function declarations placed before the `return`, so it must be run
// through runJS (which wraps the body in a function) — NOT EvalExpression,
// which wraps its argument as `return (<expr>)` and would reject a declaration.
const conditionPrelude = `function __contains(haystack, needle){
  if (haystack == null) return false;
  if (Array.isArray(haystack)) return haystack.indexOf(needle) !== -1;
  return String(haystack).indexOf(String(needle)) !== -1;
}`

func (e *Condition) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	mode, _ := in.Config["mode"].(string)
	if mode == "" {
		mode = "rules"
	}

	var expr string
	var err error
	switch mode {
	case "expression":
		raw, _ := in.Config["expression"].(string)
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return TaskOutput{}, fmt.Errorf("condition: expression is required in expression mode")
		}
		expr = "(" + raw + ")"
	case "rules":
		expr, err = compileRules(in.Config)
		if err != nil {
			return TaskOutput{}, fmt.Errorf("condition: %w", err)
		}
	default:
		return TaskOutput{}, fmt.Errorf("condition: unknown mode %q", mode)
	}

	// !!(...) coerces to a real bool regardless of what the expression yields.
	v, err := runJS(ctx, conditionPrelude+"\nreturn !!("+expr+")", in.Context, 2*time.Second)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("condition: %w", err)
	}
	result, _ := v.(bool)
	branch := "else"
	if result {
		branch = "then"
	}
	in.Log.Info("condition evaluated", "result", result, "branch", branch)
	return TaskOutput{Data: map[string]any{"result": result, "branch": branch}}, nil
}

// compileRules turns the visual rule builder config into one JS boolean
// expression. rules is a list of {operand, operator, value, kind}; combinator
// ("and"/"or", default and) joins them.
func compileRules(config map[string]any) (string, error) {
	raw, ok := config["rules"].([]any)
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("at least one rule is required in rules mode")
	}
	joiner := " && "
	if c, _ := config["combinator"].(string); strings.EqualFold(c, "or") {
		joiner = " || "
	}
	subs := make([]string, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return "", fmt.Errorf("rule %d is malformed", i+1)
		}
		sub, err := compileRule(m)
		if err != nil {
			return "", fmt.Errorf("rule %d: %w", i+1, err)
		}
		subs = append(subs, sub)
	}
	return "(" + strings.Join(subs, joiner) + ")", nil
}

func compileRule(m map[string]any) (string, error) {
	operand, _ := m["operand"].(string)
	operand = strings.TrimSpace(operand)
	if operand == "" {
		return "", fmt.Errorf("operand is required")
	}
	operator, _ := m["operator"].(string)

	switch operator {
	case "exists":
		return "(" + operand + " !== undefined && " + operand + " !== null)", nil
	case "truthy":
		return "(!!(" + operand + "))", nil
	}

	kind, _ := m["kind"].(string)
	rhs, err := emitRHS(m["value"], kind)
	if err != nil {
		return "", err
	}
	switch operator {
	case "==", "!=", ">", "<", ">=", "<=":
		return "(" + operand + " " + operator + " " + rhs + ")", nil
	case "contains":
		return "__contains(" + operand + ", " + rhs + ")", nil
	case "matches":
		return "(new RegExp(" + rhs + ").test(String(" + operand + ")))", nil
	case "":
		return "", fmt.Errorf("operator is required")
	default:
		return "", fmt.Errorf("unknown operator %q", operator)
	}
}

// emitRHS renders the rule's value as a JS literal. The kind makes the
// string-vs-number-vs-expression decision explicit so we never guess. Strings
// and regexes are JSON-encoded — that produces a correctly escaped JS string
// literal, so a value containing quotes/backslashes/newlines can't break (or
// inject into) the generated expression.
func emitRHS(value any, kind string) (string, error) {
	switch kind {
	case "", "string":
		s, ok := value.(string)
		if !ok && value != nil {
			s = fmt.Sprintf("%v", value)
		}
		b, _ := json.Marshal(s)
		return string(b), nil
	case "number":
		switch n := value.(type) {
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64), nil
		case int:
			return strconv.Itoa(n), nil
		case string:
			t := strings.TrimSpace(n)
			if _, err := strconv.ParseFloat(t, 64); err != nil {
				return "", fmt.Errorf("value %q is not a number", n)
			}
			return t, nil
		default:
			return "", fmt.Errorf("number value required")
		}
	case "boolean":
		switch b := value.(type) {
		case bool:
			return strconv.FormatBool(b), nil
		case string:
			switch strings.TrimSpace(strings.ToLower(b)) {
			case "true":
				return "true", nil
			case "false":
				return "false", nil
			}
		}
		return "", fmt.Errorf("boolean value must be true or false")
	case "expression":
		s, _ := value.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			return "", fmt.Errorf("expression value required")
		}
		return "(" + s + ")", nil
	default:
		return "", fmt.Errorf("unknown value kind %q", kind)
	}
}
