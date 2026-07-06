-- Webhook triggers are addressed by /hooks/{ws}/{path}; a path must be unique
-- within a workspace so a request resolves to exactly one trigger. Partial
-- index scoped to webhook rows only — schedule triggers carry no path.
create unique index triggers_webhook_path_unique
  on triggers (workspace_id, (config->>'path'))
  where type = 'webhook';
