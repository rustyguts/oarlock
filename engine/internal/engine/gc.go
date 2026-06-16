package engine

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// gcInterval is how often expired artifacts are swept. Retention is set per
// artifact (artifacts.expires_at) by the artifact store; this job removes the
// object bytes and the row once expired, keeping object storage bounded.
const gcInterval = 30 * time.Minute

// gcBatch caps how many artifacts a single sweep deletes, so a large backlog
// doesn't hold the control queue.
const gcBatch = 500

type GcArtifactsArgs struct{}

func (GcArtifactsArgs) Kind() string { return "gc_artifacts" }
func (GcArtifactsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueControl, MaxAttempts: 1}
}

type gcArtifactsWorker struct {
	river.WorkerDefaults[GcArtifactsArgs]
	e *Engine
}

func (w *gcArtifactsWorker) Work(ctx context.Context, job *river.Job[GcArtifactsArgs]) error {
	if w.e.Artifacts == nil {
		return nil
	}
	rows, err := w.e.Pool.Query(ctx, `
		select id, key from artifacts
		where expires_at is not null and expires_at < now()
		limit $1`, gcBatch)
	if err != nil {
		return err
	}
	type doomed struct {
		id  uuid.UUID
		key string
	}
	var items []doomed
	for rows.Next() {
		var d doomed
		if err := rows.Scan(&d.id, &d.key); err != nil {
			rows.Close()
			return err
		}
		items = append(items, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	deleted := 0
	for _, d := range items {
		// Delete the object first; only drop the row if it's gone, so a failed
		// object delete is retried next sweep (no orphaned bytes).
		if err := w.e.Artifacts.Delete(ctx, d.key); err != nil {
			w.e.Log.Warn("gc: object delete failed", "key", d.key, "error", err)
			continue
		}
		if _, err := w.e.Pool.Exec(ctx, `delete from artifacts where id = $1`, d.id); err != nil {
			w.e.Log.Warn("gc: row delete failed", "id", d.id, "error", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		w.e.Log.Info("gc: expired artifacts removed", "count", deleted)
	}
	return nil
}
