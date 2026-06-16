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
open http://localhost:3000        # web UI
curl localhost:9000/healthz       # API
make logs    # tail the API
make down    # stop everything
```

Ports: web UI **3000**, API **9000**, Postgres 5432 (`oarlock`/`oarlock`), Valkey 6379.

Create a workflow in the UI, drag steps onto the canvas (AI Prompt, HTTP
Request, Transform, Delay, MCP Tool, Run Container), connect them, then hit Run.
String config fields support `{{ }}` expressions against `input` and
`steps.<key>` outputs, e.g. `{{ steps.fetch.body.title }}`.

## Container steps

The **Run Container** step (`container.run`) runs any Docker image with files
staged in and out — e.g. `ffprobe` a video, then `ffmpeg` to transcode it. It
runs locally via Docker and at scale via Kubernetes Jobs (the engine is oblivious
to which — same `Executor` interface). Long containers don't pin a worker: the
task **suspends** (worker slot freed) and resumes on a poll/callback.

Files flow between steps through a **managed artifact store** (S3-compatible:
SeaweedFS in dev, Cloudflare R2 in prod). Upload a source via `POST /v1/artifacts`,
reference it from a step's `input_artifacts`, and download outputs from
`GET /v1/runs/{id}/artifacts`. A **compute target** (Configuration page) picks the
backend, resource ceilings, and an optional image allowlist; private registries
use a `registry`-typed secret.

`make up` enables this out of the box (local Docker backend + SeaweedFS). It is
opt-in via env on the `api` service:

```
OARLOCK_CONTAINER_RUNTIME=docker|k8s     # unset = container.run disabled
OARLOCK_S3_ENDPOINT / _ACCESS_KEY / _SECRET_KEY / _BUCKET / _REGION
OARLOCK_ARTIFACT_TTL_DAYS=0              # 0 = keep output artifacts forever
# k8s only:
OARLOCK_RUNNER_IMAGE   OARLOCK_K8S_NAMESPACE   OARLOCK_S3_POD_ENDPOINT
```

The local Docker backend mounts the host docker socket (host-root-equivalent);
it is **dev-only**. Multi-tenant production uses the Kubernetes backend (gVisor
RuntimeClass via the compute target).

## Local development

```sh
# engine (needs compose postgres + valkey running)
cd engine && go run ./cmd/api

# web (talks to the API on :9000)
cd web && bun install && bun run dev

# tests
make test
```

## Visual regression (screenshots)

The web UI has a Playwright snapshot suite (`web/tests/`). It's fully mocked and
needs no backend. **Baselines are Linux-only** (`web/tests/__snapshots__/*-linux.png`)
and are rendered inside a pinned Playwright Docker image so they're byte-identical
on every host — **including macOS**, which never uses its own browser/fonts.

```sh
make screenshots          # regenerate baselines after an intentional UI change, then commit the diff
make screenshots-check    # verify the UI against committed baselines (what CI runs)
```

Both targets just wrap a `docker run` against `mcr.microsoft.com/playwright:v<ver>-noble`
(the tag matches `@playwright/test` in `web/package.json`). **Never** regenerate with a
bare `bun run test:ui:update` on your host — host fonts/emoji drift from the container and
will corrupt the committed images.

No `make` (or want to see what it does)? Run the equivalent from the repo root — works the
same on macOS and Linux:

```sh
docker run --rm \
  -e HOST_UID=$(id -u) -e HOST_GID=$(id -g) \
  -v "$PWD":/repo -v /repo/web/node_modules \
  -w /repo/web mcr.microsoft.com/playwright:v1.61.0-noble \
  bash -lc 'apt-get update -qq >/dev/null && apt-get install -y -qq unzip >/dev/null \
    && curl -fsSL https://bun.sh/install | bash >/dev/null && export PATH=$HOME/.bun/bin:$PATH \
    && bun install --frozen-lockfile \
    && { bun run test:ui:update; ec=$?; chown -R $HOST_UID:$HOST_GID tests/__snapshots__ test-results 2>/dev/null || true; exit $ec; }'
```

The anonymous `-v /repo/web/node_modules` volume keeps the container's Linux `node_modules`
from clobbering your host's (important on macOS); swap `test:ui:update` for `test:ui` to
verify instead of regenerate.
