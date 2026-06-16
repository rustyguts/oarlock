-- Usage events: metering for the container executor — the only executor with
-- real marginal cost (design §4.2: the executor boundary is the billing
-- boundary). One row per finished container, quantity in seconds.
create table usage_events (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  run_id uuid,
  task_id uuid,
  kind text not null default 'container_seconds',
  quantity numeric not null,
  image text,
  compute_target text,
  occurred_at timestamptz not null default now()
);
create index on usage_events (workspace_id, occurred_at);
