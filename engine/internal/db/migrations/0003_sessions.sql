-- sessions (Phase 2 step 14, pulled forward for local auto-login bootstrap)
create table sessions (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  token text not null unique,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  last_seen_at timestamptz
);

-- friendlier default-workspace naming for first-run bootstrap
update workspaces set name = 'Default Workspace', slug = 'default' where slug = 'dev';
