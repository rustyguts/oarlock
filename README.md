# oarlock

Self-hosted workflow automation: a drag-and-drop canvas builder backed by an
event-driven Go engine on [River](https://riverqueue.com) (Postgres-backed
queue). Every workflow is also an MCP tool an AI agent can call.

See [docs/project.md](docs/project.md) for the design, current features, and
remaining work.

## Layout

- `engine/` — Go core: REST API + River workers (`advance_run` /
  `execute_task` / `resume_task`), step executors, secrets vault, embedded
  migrations. Run state lives in Postgres rows; workers are stateless.
- `web/` — SvelteKit UI: dashboard, workflow list, Svelte Flow canvas editor,
  run history/detail with live updates, secrets + MCP configuration.
- `docs/` — the project document.

## Quickstart

Requires Docker.

```sh
make up      # build + start Postgres 18, Valkey 9, API, and web UI
open http://localhost:3001        # web UI
curl localhost:9000/healthz       # API
make logs    # tail the API
make down    # stop everything
```

Ports: web UI **3001**, API **9000**, Postgres 5432 (`oarlock`/`oarlock`),
Valkey 6379.

Create a workflow in the UI, drag steps onto the canvas (HTTP Request,
Transform, Code, Delay, Wait for Callback, AI Prompt, MCP Tool), connect
them, then hit Run. String config fields take `{{ }}` expressions against
`input`, upstream `steps.<key>` outputs, and `secrets.<name>`, e.g.
`{{ steps.fetch.body.title }}`.

Workflows fire from webhooks (`POST /hooks/{workspace}/{path}`), cron
schedules, the UI, or an MCP client pointed at `/mcp` with a workspace API
token (created under **MCP Access**).

## Local development

```sh
# engine (needs the compose postgres + valkey running)
cd engine && go run ./cmd/api

# web (talks to the API on :9000)
cd web && bun install && bun run dev

# tests
cd engine && go test ./...        # DB-backed tests self-skip without Postgres
cd web && bun run check && bun run test:unit
cd web && bun run test:ui         # Playwright visual regression (no backend)
```

Set `OARLOCK_MASTER_KEY` (64 hex chars, `openssl rand -hex 32`) before
storing real secrets — without it a built-in dev key is used and the UI shows
a warning.
