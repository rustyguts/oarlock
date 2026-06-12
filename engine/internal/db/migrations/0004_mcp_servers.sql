-- MCP servers: workspace-scoped external Model Context Protocol servers
-- (streamable HTTP). Auth header is encrypted with the same vault key as
-- connections; referenced by name from mcp.* step configs.
create table mcp_servers (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null,
  url text not null,
  auth_encrypted bytea, key_id text,
  is_enabled boolean not null default true,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, name)
);
