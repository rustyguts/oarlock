package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// schedulerInterval is how often the cron sweep runs. lookbackWindow must
// exceed it so a due minute is never missed between ticks: each sweep looks
// back this far and fires the most recent occurrence in the window. The
// per-occurrence idempotency key makes an overlapping window harmless — the
// same occurrence firing on two consecutive sweeps (or two replicas) dedupes
// to a single run.
const (
	schedulerInterval = 30 * time.Second
	lookbackWindow    = 90 * time.Second
)

// runScheduler fires schedule triggers whose next-due occurrence has just
// passed. It owns no state: every tick re-derives what's due from the triggers
// table, and StartRunOpts' idempotency key (cron:<trigger>:<unix>) makes firing
// safe across replicas with zero locks — a lost race is a silent no-op. Runs
// until ctx is canceled.
func (e *Engine) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.sweepSchedules(ctx); err != nil {
				e.Log.Warn("scheduler sweep failed", "error", err)
			}
		}
	}
}

type scheduleTrigger struct {
	id          uuid.UUID
	workspaceID uuid.UUID
	workflowID  uuid.UUID
	cronExpr    string
}

func (e *Engine) sweepSchedules(ctx context.Context) error {
	// Only enabled schedule triggers on enabled, deployable workflows. A
	// disabled workflow (or one with no current version) never fires.
	rows, err := e.Pool.Query(ctx, `
		select t.id, t.workspace_id, t.workflow_id, t.config->>'cron'
		from triggers t
		join workflows w on w.id = t.workflow_id
		where t.type = 'schedule' and t.is_enabled
		  and w.is_enabled and w.current_version_id is not null`)
	if err != nil {
		return err
	}
	var triggers []scheduleTrigger
	for rows.Next() {
		var st scheduleTrigger
		var expr *string
		if err := rows.Scan(&st.id, &st.workspaceID, &st.workflowID, &expr); err != nil {
			rows.Close()
			return err
		}
		if expr != nil {
			st.cronExpr = *expr
		}
		triggers = append(triggers, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, st := range triggers {
		sched, err := cron.ParseStandard(st.cronExpr)
		if err != nil {
			// CRUD validation should prevent bad exprs existing; if one slips
			// through, warn once per sweep and keep going.
			e.Log.Warn("scheduler: bad cron expression", "trigger_id", st.id, "cron", st.cronExpr, "error", err)
			continue
		}
		occ, ok := latestOccurrence(sched, now, lookbackWindow)
		if !ok {
			continue // nothing due in the window
		}
		if err := e.fireSchedule(ctx, st, occ); err != nil {
			// One trigger's failure must not abort the sweep.
			e.Log.Warn("scheduler: fire failed", "trigger_id", st.id, "error", err)
		}
	}
	return nil
}

// latestOccurrence returns the most recent scheduled activation in the
// half-open window (now-lookback, now]. Returns ok=false when the schedule has
// no activation in that window. sched.Next(t) yields the first activation
// strictly after t, so we walk forward from the window start and keep the last
// activation that is still ≤ now.
func latestOccurrence(sched cron.Schedule, now time.Time, lookback time.Duration) (time.Time, bool) {
	t := now.Add(-lookback)
	var occ time.Time
	var found bool
	for {
		next := sched.Next(t)
		if next.IsZero() || next.After(now) {
			break
		}
		occ, found, t = next, true, next
	}
	return occ, found
}

// fireSchedule starts a run for one due occurrence. The idempotency key ties the
// run to (trigger, occurrence-instant) so repeat sweeps and concurrent replicas
// converge to exactly one run.
func (e *Engine) fireSchedule(ctx context.Context, st scheduleTrigger, occ time.Time) error {
	tid := st.id
	runID, created, err := e.StartRunOpts(ctx, st.workspaceID, st.workflowID,
		map[string]any{"scheduled_for": occ.UTC().Format(time.RFC3339)},
		RunOpts{TriggerID: &tid, IdempotencyKey: fmt.Sprintf("cron:%s:%d", st.id, occ.Unix())})
	if err != nil {
		return err
	}
	if created {
		e.Log.Info("scheduled run fired", "trigger_id", st.id, "workflow_id", st.workflowID,
			"run_id", runID, "scheduled_for", occ.UTC().Format(time.RFC3339))
	}
	return nil
}
