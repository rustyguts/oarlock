-- The `log` step type is removed: every task logs by default since the
-- task_logs sink landed, so an explicit log step is redundant. Strip log
-- steps from stored definitions and drop dangling `needs` references so
-- existing workflows keep validating and running.
with rewritten as (
  select v.id,
    jsonb_set(v.definition, '{steps}', coalesce((
      select jsonb_agg(
        case
          when s ? 'needs' then jsonb_set(s, '{needs}', coalesce((
            select jsonb_agg(n)
            from jsonb_array_elements(s->'needs') n
            where not exists (
              select 1 from jsonb_array_elements(v.definition->'steps') ls
              where ls->>'type' = 'log' and ls->>'key' = n #>> '{}'
            )), '[]'::jsonb))
          else s
        end)
      from jsonb_array_elements(v.definition->'steps') s
      where s->>'type' <> 'log'), '[]'::jsonb)) as def
  from workflow_versions v
  where exists (
    select 1 from jsonb_array_elements(v.definition->'steps') s
    where s->>'type' = 'log'
  )
)
update workflow_versions v
set definition = r.def
from rewritten r
where v.id = r.id;
