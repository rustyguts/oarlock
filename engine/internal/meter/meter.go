// Package meter records billable usage events. Only the container executor
// meters — it is the only executor with real marginal cost (hard rule 8: the
// executor boundary is the billing boundary).
package meter

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rustyguts/oarlock/engine/internal/steps"
)

type DBMeter struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *DBMeter { return &DBMeter{pool: pool} }

func (m *DBMeter) RecordContainerSeconds(ctx context.Context, in steps.TaskInput, computeTarget, image string, seconds float64) error {
	_, err := m.pool.Exec(ctx, `
		insert into usage_events (workspace_id, run_id, task_id, kind, quantity, image, compute_target)
		values ($1, $2, $3, 'container_seconds', $4, $5, $6)`,
		in.WorkspaceID, in.RunID, in.TaskID, seconds, image, computeTarget)
	return err
}
