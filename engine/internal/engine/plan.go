package engine

import "github.com/rustyguts/oarlock/engine/internal/definition"

// runPlan is the derived state of a run: which steps are ready to enqueue,
// which should be marked skipped (untaken branches), and whether the run is
// terminal. It is a pure function of the definition + persisted rows, so a
// replay converges (advanceRunWorker.Work is idempotent).
type runPlan struct {
	ready        []string
	skip         []string
	allSucceeded bool
	anyFailed    bool
}

type planEdge struct {
	other string // the predecessor (for in-edges) or successor (for out-edges)
	label string // branch label: "then"/"else", or "" for a plain dependency
}

// computePlan derives the plan from the definition, the latest-attempt status
// per step (stepStatus, only steps that have a task row), and the decided
// branch of each succeeded condition (branchChoice: condKey -> "then"/"else").
//
// Branching works by pruning edges and propagating "skipped" through the dead
// part of the graph:
//
//   - A condition decides a branch; edges labeled with the other branch are
//     PRUNED. A skipped condition (discovered here, via the fixpoint) prunes
//     BOTH of its branches.
//   - A step is "branch-dead" when it has ≥1 branch-labeled in-edge and ALL of
//     them are pruned — its branch wasn't taken. (This is why a branch step may
//     still carry plain prep dependencies and route correctly: an extra
//     unlabeled in-edge does not keep it alive.)
//   - A step that no live path can reach is also dead, once all its in-edges are
//     settled (so we never skip a step a still-running sibling could feed).
//
// A pruned edge counts as a SATISFIED dependency, so a join after an if/else
// fires when its taken side completes (the untaken side is skipped, which also
// satisfies). The whole thing is a fixpoint so a deep dead branch — or a nested
// condition that only becomes skipped during this pass — collapses in one call.
func computePlan(def *definition.Definition, stepStatus, branchChoice map[string]string) runPlan {
	for _, st := range stepStatus {
		if st == "failed" || st == "canceled" {
			return runPlan{anyFailed: true}
		}
	}

	inEdges := map[string][]planEdge{}
	outEdges := map[string][]planEdge{}
	isCondition := map[string]bool{}
	for _, s := range def.Steps {
		isCondition[s.Key] = s.Type == definition.ConditionType
		for _, n := range s.Needs {
			label := s.Branches[n]
			inEdges[s.Key] = append(inEdges[s.Key], planEdge{other: n, label: label})
			outEdges[n] = append(outEdges[n], planEdge{other: s.Key, label: label})
		}
	}

	// Working copies: the fixpoint adds "skipped" to status and the both-pruned
	// sentinel ("") to choice as conditions are discovered skipped.
	status := make(map[string]string, len(stepStatus))
	for k, v := range stepStatus {
		status[k] = v
	}
	choice := make(map[string]string, len(branchChoice))
	for k, v := range branchChoice {
		choice[k] = v
	}

	edgePruned := func(pred, label string) bool {
		c, decided := choice[pred]
		if !decided || label == "" {
			return false // non-condition / undecided condition / plain edge
		}
		if c == "" {
			return true // skipped condition prunes both branches
		}
		return label != c
	}
	branchDead := func(key string) bool {
		labeled := false
		for _, e := range inEdges[key] {
			if e.label == "" {
				continue
			}
			labeled = true
			if !edgePruned(e.other, e.label) {
				return false
			}
		}
		return labeled
	}
	markSkipped := func(key string) {
		status[key] = "skipped"
		if isCondition[key] {
			choice[key] = "" // sentinel: a skipped condition prunes both branches
		}
	}

	terminal := func(st string) bool {
		switch st {
		case "succeeded", "skipped", "failed", "canceled":
			return true
		}
		return false
	}

	// Forward reachability from live seeds over non-pruned edges; skipped steps
	// are dead sinks (we never traverse out of them).
	reachableSet := func() map[string]bool {
		reachable := map[string]bool{}
		var queue []string
		for _, s := range def.Steps {
			st, has := status[s.Key]
			switch {
			case has && st == "skipped":
				// dead — don't seed
			case has:
				queue = append(queue, s.Key) // running/suspended/succeeded/queued: live
			case len(inEdges[s.Key]) == 0:
				queue = append(queue, s.Key) // a root that hasn't started yet
			}
		}
		for len(queue) > 0 {
			cur := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if reachable[cur] {
				continue
			}
			reachable[cur] = true
			for _, e := range outEdges[cur] {
				if edgePruned(cur, e.label) {
					continue
				}
				if status[e.other] == "skipped" {
					continue
				}
				queue = append(queue, e.other)
			}
		}
		return reachable
	}

	for iter := 0; iter <= len(def.Steps); iter++ {
		changed := false
		// (1) Branch-not-taken steps.
		for _, s := range def.Steps {
			if _, has := status[s.Key]; has {
				continue
			}
			if branchDead(s.Key) {
				markSkipped(s.Key)
				changed = true
			}
		}
		// (2) Unreachable steps whose in-edges have all settled.
		reachable := reachableSet()
		for _, s := range def.Steps {
			if _, has := status[s.Key]; has || reachable[s.Key] {
				continue
			}
			settled := true
			for _, e := range inEdges[s.Key] {
				if edgePruned(e.other, e.label) {
					continue
				}
				if !terminal(status[e.other]) {
					settled = false
					break
				}
			}
			if settled {
				markSkipped(s.Key)
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	plan := runPlan{allSucceeded: true}
	for _, s := range def.Steps {
		st, has := status[s.Key]
		if has {
			switch st {
			case "succeeded":
			case "skipped":
				if _, original := stepStatus[s.Key]; !original {
					plan.skip = append(plan.skip, s.Key) // newly skipped this pass
				}
			default:
				plan.allSucceeded = false // queued/running/suspended
			}
			continue
		}
		plan.allSucceeded = false
		depsOK := true
		for _, e := range inEdges[s.Key] {
			if edgePruned(e.other, e.label) {
				continue // a pruned dependency is satisfied
			}
			if ps := status[e.other]; ps != "succeeded" && ps != "skipped" {
				depsOK = false
				break
			}
		}
		if depsOK {
			plan.ready = append(plan.ready, s.Key)
		}
	}
	return plan
}
