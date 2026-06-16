-- Compute targets: workspace-scoped "where/how to run a container" profiles,
-- referenced by name from container.* step configs (config.compute_target is a
-- flat top-level string == this name, so reference protection can scan it).
-- A target picks the backend (local docker vs k8s Jobs), placement (namespace,
-- gVisor RuntimeClass), resource ceilings the executor clamps to, an optional
-- image allowlist (prefix match; empty = any), and an optional private-registry
-- credential (a secrets.name of type 'registry').
create table compute_targets (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null,
  backend text not null,                       -- 'docker' | 'k8s'
  namespace text,                              -- k8s only
  runtime_class text,                          -- e.g. 'gvisor' for isolation
  cpu_limit text not null default '1',         -- docker NanoCPUs / k8s limits.cpu
  memory_mb_limit integer not null default 1024,
  timeout_sec_limit integer not null default 600,
  image_allowlist text[] not null default '{}', -- empty => any image
  registry_secret_name text,                   -- secrets.name (type 'registry')
  is_enabled boolean not null default true,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name)
);
