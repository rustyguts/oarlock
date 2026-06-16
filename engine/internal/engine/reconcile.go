package engine

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// The scheduled-at insert in suspendTask (one tx with the suspend write) is the
// primary resume mechanism. The reconciler is a belt-and-suspenders sweep: it
// re-enqueues scheduled resumes that are well past due (a lost River job, say),
// relying on the resume worker's status + row guards to make a double-resume a
// harmless no-op.
const (
	reconcileInterval = 60 * time.Second
	reconcileGrace    = "2 minutes" // how overdue before we re-enqueue
)

type ReconcileSuspensionsArgs struct{}

func (ReconcileSuspensionsArgs) Kind() string { return "reconcile_suspensions" }
func (ReconcileSuspensionsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueControl, MaxAttempts: 1}
}

type reconcileSuspensionsWorker struct {
	river.WorkerDefaults[ReconcileSuspensionsArgs]
	e *Engine
}

func (w *reconcileSuspensionsWorker) Work(ctx context.Context, job *river.Job[ReconcileSuspensionsArgs]) error {
	rows, err := w.e.Pool.Query(ctx, `
		select s.id, s.task_id
		from suspensions s
		join tasks t on t.id = s.task_id
		where t.status = 'suspended'
		  and s.resume_at is not null
		  and s.resume_at < now() - $1::interval`, reconcileGrace)
	if err != nil {
		return err
	}
	defer rows.Close()
	type due struct{ suspID, taskID uuid.UUID }
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.suspID, &d.taskID); err != nil {
			return err
		}
		dues = append(dues, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range dues {
		if _, err := w.e.Client.Insert(ctx,
			ResumeTaskArgs{TaskID: d.taskID, SuspensionID: d.suspID, Reason: "poll"}, nil); err != nil {
			w.e.Log.Warn("reconcile resume enqueue failed", "task_id", d.taskID, "error", err)
		}
	}
	if len(dues) > 0 {
		w.e.Log.Info("reconciled overdue suspensions", "count", len(dues))
	}
	return nil
}
