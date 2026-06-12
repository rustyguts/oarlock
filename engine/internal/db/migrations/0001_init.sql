-- control-plane-ish (lives with cell-0 for now; split is logical, not physical)
create extension if not exists citext;

create table cells (
  id text primary key,
  region text not null, api_url text not null,
  engine_version text, status text not null default 'active'
);

create table workspaces (
  id uuid primary key default gen_random_uuid(),
  slug text not null unique, name text not null,
  plan text not null default 'free',
  cell_id text not null default 'cell-0' references cells(id),
  settings jsonb not null default '{}',
  created_at timestamptz not null default now()
);

create table users (
  id uuid primary key default gen_random_uuid(),
  email citext not null unique, name text,
  created_at timestamptz not null default now()
);

create table workspace_members (
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id uuid not null references users(id) on delete cascade,
  role text not null default 'editor',    -- owner|admin|editor|viewer
  created_at timestamptz not null default now(),
  primary key (workspace_id, user_id)
);

-- definitions
create table workflows (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null, slug text not null,
  is_enabled boolean not null default false,
  current_version_id uuid,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, slug)
);

create table workflow_versions (
  id uuid primary key default gen_random_uuid(),
  workflow_id uuid not null references workflows(id) on delete cascade,
  version integer not null,
  definition jsonb not null,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  unique (workflow_id, version)
);
alter table workflows add constraint fk_current_version
  foreign key (current_version_id) references workflow_versions(id);

create table triggers (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  workflow_id uuid not null references workflows(id) on delete cascade,
  type text not null,                     -- webhook|schedule|manual
  config jsonb not null default '{}',
  is_enabled boolean not null default true,
  created_at timestamptz not null default now()
);

-- credentials (envelope-encrypted)
create table connections (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null, provider text not null,
  encrypted_data bytea not null, key_id text not null,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  unique (workspace_id, name)
);

-- execution
create type run_status  as enum ('queued','running','suspended','succeeded','failed','canceled');
create type task_status as enum ('queued','running','suspended','succeeded','failed','skipped','canceled');

create table runs (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  workflow_id uuid not null references workflows(id) on delete cascade,
  workflow_version_id uuid not null references workflow_versions(id),
  trigger_id uuid references triggers(id),
  status run_status not null default 'queued',
  input jsonb, input_blob_ref text,
  error jsonb, idempotency_key text,
  created_at timestamptz not null default now(),
  started_at timestamptz, finished_at timestamptz,
  unique (workspace_id, idempotency_key)
);
create index on runs (workspace_id, created_at desc);
create index on runs (workspace_id, status) where status in ('queued','running','suspended');

create table tasks (
  id uuid primary key default gen_random_uuid(),
  run_id uuid not null references runs(id) on delete cascade,
  workspace_id uuid not null,             -- denormalized for RLS + quotas
  step_key text not null, attempt integer not null default 1,
  status task_status not null default 'queued',
  input jsonb, output jsonb, output_blob_ref text,
  error jsonb,
  queued_at timestamptz not null default now(),
  started_at timestamptz, finished_at timestamptz,
  unique (run_id, step_key, attempt)
);
create index on tasks (run_id);

create table suspensions (
  id uuid primary key default gen_random_uuid(),
  task_id uuid not null references tasks(id) on delete cascade,
  workspace_id uuid not null,
  kind text not null,                     -- delay|callback|approval|poll
  resume_token text unique, resume_at timestamptz,
  payload jsonb,
  created_at timestamptz not null default now()
);
create index on suspensions (resume_at) where resume_at is not null;

-- seed: cell-0 + dev workspace + dev user (fixed UUIDs so the dev API can assume them)
insert into cells (id, region, api_url) values ('cell-0', 'local', 'http://localhost:9000');
insert into workspaces (id, slug, name, plan)
  values ('00000000-0000-0000-0000-000000000001', 'dev', 'Dev Workspace', 'free');
insert into users (id, email, name)
  values ('00000000-0000-0000-0000-000000000002', 'dev@localhost', 'Dev User');
insert into workspace_members (workspace_id, user_id, role)
  values ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'owner');
