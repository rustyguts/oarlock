-- Artifacts: managed blob references for container step I/O. Bytes live in the
-- object store (SeaweedFS in dev, R2 in prod) under ws/{workspace_id}/... keys
-- (hard rule 7); this table is the metadata index. A container task can emit N
-- files, so this is a dedicated table rather than the single dead
-- tasks.output_blob_ref column (which is left unused). Downstream steps receive
-- structured refs ({id,name,size,content_type}) under steps.<key>.artifacts.
create table artifacts (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  run_id uuid references runs(id) on delete cascade,
  task_id uuid references tasks(id) on delete cascade,
  key text not null,                                  -- ws/{workspace_id}/...
  name text not null,                                 -- user-facing filename
  size bigint not null,
  content_type text not null default 'application/octet-stream',
  source text not null default 'output',              -- 'output' | 'upload'
  expires_at timestamptz,                             -- null = no expiry (GC, Phase D)
  created_at timestamptz not null default now()
);
create index on artifacts (workspace_id, run_id);
create index on artifacts (expires_at) where expires_at is not null;
create unique index on artifacts (workspace_id, key);
