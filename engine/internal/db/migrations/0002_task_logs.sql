-- logs: partitioned, capped, disposable (design §5). Weekly partitions +
-- retention DROP PARTITION come with the maintenance job; a DEFAULT
-- partition keeps writes flowing until then.
create table task_logs (
  id bigint generated always as identity,
  workspace_id uuid not null,
  run_id uuid not null,
  task_id uuid not null,
  step_key text not null default '',
  ts timestamptz not null default now(),
  level smallint not null default 0,       -- slog levels: 0=info 4=warn 8=error
  message text not null,
  fields jsonb
) partition by range (ts);

create table task_logs_default partition of task_logs default;

create index on task_logs using brin (ts);
create index on task_logs (run_id, id desc);
