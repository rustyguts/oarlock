# oarlock

Self-hosted workflow automation: a drag-and-drop workflow builder backed by an
event-driven Go engine on [River](https://riverqueue.com) (Postgres-backed
queue). See [docs/design.md](docs/design.md) for the design and
[docs/todo.md](docs/todo.md) for build progress.

## Layout

- `engine/` — Go core: REST API + River workers (`advance_run` / `execute_task`), step executors, migrations. Engine state lives in Postgres rows; workers are stateless.
- `web/` — SvelteKit UI: workflow list + Svelte Flow canvas editor (drag steps from the palette, wire `needs` edges, configure, save versions, run with live status).
- `docs/` — design doc + build todo
- `deploy/` — Helm / production manifests (later)

## Quickstart

Requires Docker.

```sh
make up      # build + start Postgres 18, Valkey 9, API, and web UI
open http://localhost:3001        # web UI
curl localhost:9000/healthz       # API
make logs    # tail the API
make down    # stop everything
```

Ports: web UI **3001**, API **9000**, Postgres 5432 (`oarlock`/`oarlock`), Valkey 6379.

Create a workflow in the UI, drag steps onto the canvas (HTTP Request,
Transform, Delay, Log), connect them, then hit Run. String config fields
support `{{ }}` expressions against `input` and `steps.<key>` outputs, e.g.
`{{ steps.fetch.body.title }}`.

## Local development

```sh
# engine (needs compose postgres + valkey running)
cd engine && go run ./cmd/api

# web (talks to the API on :9000)
cd web && bun install && bun run dev

# tests
make test
```
