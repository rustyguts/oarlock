-- Workspace API tokens: bearer credentials for the workspace MCP endpoint
-- (design §6 step 18). The token IS the workspace credential — one stable
-- /mcp URL, scoped by which token authenticates. Only the sha256 hash is
-- stored (a DB leak never exposes a usable token); prefix is the first 8 raw
-- chars kept for display in the tokens UI.
create table workspace_api_tokens (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null,
  token_hash text not null unique,
  prefix text not null,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  last_used_at timestamptz,
  unique (workspace_id, name)
);
