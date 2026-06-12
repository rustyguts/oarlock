package api

import (
	"net/http"

	"github.com/google/uuid"
)

// Workspace dashboard statistics — all real aggregates over runs/tasks/logs,
// scoped to the session workspace.

type statsTotals struct {
	Workflows     int     `json:"workflows"`
	Runs          int     `json:"runs"`
	Succeeded     int     `json:"succeeded"`
	Failed        int     `json:"failed"`
	Canceled      int     `json:"canceled"`
	Active        int     `json:"active"`
	Tasks         int     `json:"tasks"`
	LogLines      int     `json:"log_lines"`
	Secrets       int     `json:"secrets"`
	MCPServers    int     `json:"mcp_servers"`
	AvgDurationMS *int    `json:"avg_duration_ms"`
	SuccessRate   *float64 `json:"success_rate"` // succeeded / terminal
}

type statsDaily struct {
	Date      string `json:"date"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Canceled  int    `json:"canceled"`
}

type statsWorkflow struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Runs          int       `json:"runs"`
	Failed        int       `json:"failed"`
	AvgDurationMS *int      `json:"avg_duration_ms"`
	LastRunAt     *string   `json:"last_run_at"`
}

type statsRecentRun struct {
	ID           uuid.UUID `json:"id"`
	WorkflowID   uuid.UUID `json:"workflow_id"`
	WorkflowName string    `json:"workflow_name"`
	Status       string    `json:"status"`
	CreatedAt    string    `json:"created_at"`
	StartedAt    *string   `json:"started_at"`
	FinishedAt   *string   `json:"finished_at"`
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ws := s.workspace(r)

	var t statsTotals
	err := s.DB.QueryRow(ctx, `
		select
		  (select count(*) from workflows where workspace_id = $1),
		  (select count(*) from runs where workspace_id = $1),
		  (select count(*) from runs where workspace_id = $1 and status = 'succeeded'),
		  (select count(*) from runs where workspace_id = $1 and status = 'failed'),
		  (select count(*) from runs where workspace_id = $1 and status = 'canceled'),
		  (select count(*) from runs where workspace_id = $1 and status in ('queued','running','suspended')),
		  (select count(*) from tasks where workspace_id = $1),
		  (select count(*) from task_logs where workspace_id = $1),
		  (select count(*) from secrets where workspace_id = $1),
		  (select count(*) from mcp_servers where workspace_id = $1),
		  (select (extract(epoch from avg(finished_at - started_at)) * 1000)::int
		     from runs where workspace_id = $1 and started_at is not null and finished_at is not null)`,
		ws).Scan(&t.Workflows, &t.Runs, &t.Succeeded, &t.Failed, &t.Canceled, &t.Active,
		&t.Tasks, &t.LogLines, &t.Secrets, &t.MCPServers, &t.AvgDurationMS)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	if terminal := t.Succeeded + t.Failed + t.Canceled; terminal > 0 {
		rate := float64(t.Succeeded) / float64(terminal)
		t.SuccessRate = &rate
	}

	// Last 14 days, zero-filled.
	daily := []statsDaily{}
	rows, err := s.DB.Query(ctx, `
		select d::date::text,
		  count(r.id) filter (where r.status = 'succeeded'),
		  count(r.id) filter (where r.status = 'failed'),
		  count(r.id) filter (where r.status = 'canceled')
		from generate_series(current_date - interval '13 days', current_date, '1 day') d
		left join runs r on r.workspace_id = $1 and r.created_at::date = d::date
		group by d order by d`, ws)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var d statsDaily
		if err := rows.Scan(&d.Date, &d.Succeeded, &d.Failed, &d.Canceled); err != nil {
			rows.Close()
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		daily = append(daily, d)
	}
	rows.Close()

	topWorkflows := []statsWorkflow{}
	rows, err = s.DB.Query(ctx, `
		select w.id, w.name, count(r.id),
		  count(r.id) filter (where r.status = 'failed'),
		  (extract(epoch from avg(r.finished_at - r.started_at)) * 1000)::int,
		  max(r.created_at)::text
		from workflows w
		left join runs r on r.workflow_id = w.id
		where w.workspace_id = $1
		group by w.id, w.name
		order by count(r.id) desc, w.name
		limit 6`, ws)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var x statsWorkflow
		if err := rows.Scan(&x.ID, &x.Name, &x.Runs, &x.Failed, &x.AvgDurationMS, &x.LastRunAt); err != nil {
			rows.Close()
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		topWorkflows = append(topWorkflows, x)
	}
	rows.Close()

	recent := []statsRecentRun{}
	rows, err = s.DB.Query(ctx, `
		select r.id, r.workflow_id, w.name, r.status::text,
		  r.created_at::text, r.started_at::text, r.finished_at::text
		from runs r join workflows w on w.id = r.workflow_id
		where r.workspace_id = $1
		order by r.created_at desc
		limit 10`, ws)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var x statsRecentRun
		if err := rows.Scan(&x.ID, &x.WorkflowID, &x.WorkflowName, &x.Status,
			&x.CreatedAt, &x.StartedAt, &x.FinishedAt); err != nil {
			rows.Close()
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		recent = append(recent, x)
	}
	rows.Close()

	taskStatuses := map[string]int{}
	rows, err = s.DB.Query(ctx, `
		select status::text, count(*) from tasks
		where workspace_id = $1 group by status`, ws)
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		taskStatuses[status] = n
	}
	rows.Close()

	s.json(w, http.StatusOK, map[string]any{
		"totals":        t,
		"daily":         daily,
		"top_workflows": topWorkflows,
		"recent_runs":   recent,
		"task_statuses": taskStatuses,
	})
}
