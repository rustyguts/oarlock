# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

oarlock is a self-hostable workflow automation platform: a drag-and-drop canvas builder over an event-driven Go engine. `docs/design.md` is the binding build plan — its glossary names (Workspace, Workflow, Version, Definition, Step, Task, Run, Secret, Suspension, Cell) and eight "hard rules" govern naming and architecture decisions. `docs/todo.md` tracks progress against the design's build order; update it when completing or rescoping work.

## Comments

Terse and concise. Explain *why*, not *what* — skip fluff, don't restate the code.

## Commands

```sh
make up                                   # full stack: Postgres 18, Valkey 9, API :9000, web :3000
docker compose up -d --build api          # rebuild after engine/ changes
docker compose up -d --build web          # rebuild after web/ changes

cd engine && go build ./... && go vet ./... && go test ./...
cd engine && go test ./internal/definition/ -run TestValidateCycle   # single test

cd web && bun run build                   # vite build (does NOT typecheck)
cd web && bunx svelte-check --tsconfig ./tsconfig.json   # typecheck — NOT a declared dep; bunx fetches it on demand (no check script)
make screenshots-check                    # Playwright visual gate vs committed Linux baselines, in the pinned image (the real gate; any host)
make screenshots                          # regenerate those Linux baselines after an intentional UI change (any host; see Visual regression suite)
cd web && bun run test:ui                 # self-contained host run (builds + previews on :4173, mocked API) — fast iteration ONLY; host fonts won't match the -linux baselines
```

Migrations are embedded SQL in `engine/internal/db/migrations/`, applied automatically at API startup in filename order (tracked in `schema_migrations`); River's own tables migrate via `rivermigrate`. There is no down-migration mechanism — dump first (`docker compose exec -T postgres pg_dump -U oarlock oarlock > backups/...`).

## Engine architecture (engine/)

The non-negotiable core (design §4.1): **no process owns a run; truth lives in Postgres rows**. Two River job types only, on separate queues (`control`, MaxWorkers 10, for advancement; `tasks`, MaxWorkers 50, for execution — so slow executors never starve state transitions):

- `advance_run` (`internal/engine/advance.go`): loads the definition + latest task attempt per step, computes ready steps (all `needs` succeeded/skipped), inserts task rows and enqueues `execute_task` jobs **in one transaction**, marks the run succeeded/failed when terminal. Idempotent — re-derives everything from rows, so duplicate advances converge.
- `execute_task` (`internal/engine/execute.go`): loads the task, builds the frozen expression context (`input`, `steps.<key>` outputs, `secrets.*`), interpolates `{{ }}` config via goja, runs the executor, persists the result, and enqueues the next `advance_run` in the same transaction as the status write.

Hard rule 2 in practice: **a job insert and its corresponding state write always share a transaction** (`river.Client.InsertTx`). Status transitions are guarded (`where status = 'queued'` / `in ('queued','running')`; `finishTask`'s guard also accepts `'suspended'` so a resume can finalize) so a concurrent cancel beats an in-flight executor's late result. Retries are new task rows (`attempt+1`, exponential backoff `2^attempt` seconds), never River-level job retries (`MaxAttempts: 1`): a step's `retries` is the count of *extra* attempts, so total attempts = `retries+1` (0 = one shot). `execute_task` returns nil from `Work` even when the executor errors — the failure is persisted as task `status='failed'` and retried as a new row; only infrastructure errors (DB, secret load) surface as a hard River failure. `advance_run` fails the whole run the moment any step's latest attempt is failed/canceled — it stops enqueuing new ready steps but does **not** interrupt already-running siblings (v0 has no task interruption; their late results are dropped by the status guard).

**Suspension & async steps**: a step frees its worker slot for long waits by returning the `*steps.Suspended` sentinel error (additive — synchronous executors never return it). `suspendTask` (`execute.go`) writes `status='suspended'` + a `suspensions` row + (for poll/delay) a scheduled `resume_task` job, all in one tx. The `resume_task` worker (`resume.go`) re-invokes the executor's optional `Resumable.Resume` (poll → re-suspend with backoff; terminal → normal `finishTask`); `prepareTask` is the shared load/context/redactor prelude (the redactor is **rebuilt every invocation**, never carried across a suspension — hard rule 6). Callback resumes hit `POST /v1/resume/{token}` (unauthenticated capability route, mounted outside `WithAuth` in `main.go`). `reconcile_suspensions` (periodic) re-enqueues overdue lost resumes; `gc_artifacts` (periodic) reclaims expired artifacts. `delay` is now suspension-backed (no parked goroutine). Job types total five: `advance_run`, `execute_task`, `resume_task`, `reconcile_suspensions`, `gc_artifacts`.

**Container steps** (`container.run`, `internal/container` + `internal/steps/container.go`): `Execute` submits the container and suspends (poll); `Resume` polls, then on terminal uploads outputs, records artifacts, meters container-seconds. The `ContainerRuntime` interface has two backends selected by `OARLOCK_CONTAINER_RUNTIME`: **Docker** (`docker.go`, raw Engine API over the unix socket — no SDK; stages files via the archive/`docker cp` API so it works from inside a container; host-root-equivalent, **dev-only**) and **Kubernetes** (`k8s.go`, client-go Jobs; the `oarlock-runner` emissary — an init container stages inputs + copies the static runner binary into a shared `emptyDir`, the user container runs the runner-wrapped command, uploads outputs + a `.oarlock-result.json` manifest to object storage; gVisor via the compute target's RuntimeClass). Files move through the **managed artifact store** (`internal/artifact`, S3-compatible via minio-go: SeaweedFS dev / R2 prod; keys `ws/{workspace_id}/…`), surfaced to downstream steps as `steps.<key>.artifacts[]`. `env`/`input_artifacts` interpolation reuses the engine's `{{ }}` machinery (so `{{secrets.X}}` and `{{steps.x.artifacts[0].id}}` resolve before the executor runs). Compute targets are a referenceable resource (`internal/api/compute_targets.go`, reference-protected via `referencingWorkflows("container.%","compute_target",…)`); registry creds are a `registry`-typed secret. The engine, the artifact store, and the runtime are nil-guarded — never assign a typed-nil `*S3Store`/runtime into `steps.Services`/`engine.New` (use a nil interface).

Step executors implement the one `Executor` interface (`internal/steps`); execution strategy is a property of the step type, invisible to the engine. Native types ship in `steps.Default` (`steps.go`): `ai.prompt` (BYOK LLM), `mcp.tool`, `http.request`, `transform` (JS via goja), `delay`, and `container.run` (registered only when `svc.Container`+`svc.Artifacts` are wired). The registry's `TypeInfo.ConfigSpec` drives the UI property panels — adding a step type that reuses an existing `ConfigKey.Kind` (`string`/`text`/`number`/`select`/`api_key`/`mcp_server`/`mcp_tool`/`compute_target`) is purely additive, but a **new** kind is not: it must also be added to the frontend union (`src/lib/api.ts`) and the Inspector's if/else render chain, or the field renders no input. Executors needing workspace resources get them via `steps.Services` (resolver interfaces implemented by `internal/vault`), threaded in at `steps.Default(svc)`. `ai.prompt` has no provider field — the provider is read off the chosen `api_key` secret (`anthropic`→`/v1/messages`, `max_tokens`; `openai`/`openrouter`→chat/completions, `max_completion_tokens`).

**Secrets and redaction** (hard rule 6): secrets are AES-256-GCM encrypted (`internal/vault`, master key from `OARLOCK_MASTER_KEY`, dev fallback). At task execution the engine decrypts all workspace secrets, binds them as `secrets.*` context, and builds a `redactor` whose values are scrubbed from **everything persisted or emitted**: task input/output/error JSON, every `task_logs` row, and the stdout tee in the task log handler (`internal/engine/dblog.go`). Only secret values **≥ 4 chars** are redacted (shorter ones would shred unrelated text), replaced longest-first and with the JSON-escaped form also registered, so a substring secret leaves no fragment — preserve both behaviors in redaction-adjacent code. Any new code path that persists or logs task-derived data must go through the taskRef's redactor.

**Logging**: every task automatically gets a DB-backed log trail (lifecycle lines + whatever executors write to `TaskInput.Log`) via a per-task slog handler that writes capped lines (8KB/line, ~1MB/task) to the partitioned `task_logs` table and tees redacted records to the process log.

**Live updates**: workers publish fire-and-forget pings to Valkey channel `run:{id}` after each state change; the SSE endpoint (`/v1/runs/{id}/events`) refetches the run snapshot from Postgres per ping. Valkey is never load-bearing for correctness — Postgres remains truth.

**Tenancy**: every API request resolves a workspace from the session (cookie auth with first-run auto-login as the migration-seeded owner — `internal/api/auth.go`); all queries filter by `workspace_id` via `s.workspace(r)`. Never join across workspaces (hard rule 7). CORS is credentialed (echoed origin, not wildcard).

**Reference protection**: secrets and MCP servers are referenced from step configs *by name*. Deleting (or renaming) one that a workflow's current version references must 409 with the referencing workflow names — see `referencingWorkflows` (jsonb step-config scan) and `workflowsMentioning` (definition text scan for `secrets.<name>`) in `internal/api/resources.go`. Note the asymmetry: secrets use **both** scans, but MCP servers use **only** `referencingWorkflows` (`mcp.%`/`server`) — an MCP name appearing solely in interpolated definition text is not protected. New referenceable resources need the same treatment (and ideally both scans).

## Web architecture (web/)

SvelteKit (Svelte 5 runes) + Tailwind 4 + shadcn-svelte + `@xyflow/svelte`. The browser calls the Go API directly (`PUBLIC_API_URL`, default `http://localhost:9000`) with `credentials: 'include'`; there are no SvelteKit server routes.

The **JSON definition is the only canonical workflow artifact** (hard rule 4): `src/lib/flow.ts` converts definition⇄canvas (node id = step key; edge source→target = "target needs source"; canvas positions persist in each step's `ui` field — the engine ignores them). Saving the canvas creates a new immutable version. The run detail page renders the **pinned** version a run executed, not the current one. The editor's logic lives entirely in `src/lib/components/Editor.svelte` (the `[id]` route is just a keyed `SvelteFlowProvider` shell); `nodes`/`edges` are `$state.raw` (xyflow perf), so every change must reassign the whole array (`nodes = nodes.map(...)`), never mutate in place. Renaming a step key must rewrite connected edges' `source`/`target` **and** their `source->target` id, since `needs` is derived from `edge.source` on save.

Live run state arrives via `watchRun` (EventSource wrapper in `src/lib/api.ts`); the server closes the stream after the terminal snapshot. No polling for run *state*; the timer polls that do exist are the runs *list* (4s), the *logs* panel (2s while active), and the dashboard *stats* (10s).

The Inspector renders step config forms from the API's `config_spec`; kinds `api_key`/`mcp_server`/`mcp_tool` are dynamic selects (api_key lists only api_key-typed secrets; mcp_tool fetches the chosen server's live tool list).

**Design language (Brendan's house style)**: every component shares the *same* background color — never a lighter card on a darker page (or vice versa). Separation comes from **borders**, not background contrast. This is encoded in the theme tokens (`--card`, `--popover`, `--sidebar` are all `var(--background)` in both modes); don't introduce surfaces with their own background tones. Subtle `bg-muted` fills are fine for *insets* (icon tiles, code/log blocks), not for panels. Primary pages (workflow list, run history) use the full page width (`w-full px-6`), not centered max-width columns. Brand font is Geist (+ Geist Mono), loaded via Fontsource. Brand-colored **text/icons on the background use `text-primary-strong`**, not `text-primary` (raw #ffcc00 fails on white). Light mode darkens both gold tokens for legibility — `--primary-strong` for text/icons, and `--primary` itself to a slightly deeper, less-neon gold for filled `bg-primary` controls; dark mode is #ffcc00 throughout. `bg-primary` with `text-primary-foreground` (filled buttons) works in both.

shadcn-svelte components live in `src/lib/components/ui/` and are project-owned — editing them is fine and sometimes necessary (e.g. `sidebar-menu-button.svelte` omits `data-active` when false because its styles use presence-based variants). Icons: `@lucide/svelte`, imported per-icon from `@lucide/svelte/icons/<name>`. Brand gold is `#ffcc00` in dark mode; light mode uses a slightly deeper same-hue gold (theme vars in `src/app.css`). Active nav items use the gold for icon + text.

## Frontend screenshot loop (required)

Whenever you change the frontend, **verify by looking at it, not just by building it**:

1. Build + rebuild the web container.
2. Drive a real browser (Playwright chromium is installed; scratch scripts in `/tmp` work) against http://localhost:3000 — visit the changed screens in **light and dark mode** (`button[aria-label="Toggle theme"]`).
3. Screenshot and **actually read the pixels** against what was asked: spacing, contrast, active states, phantom backgrounds, overflow.
4. Iterate until right. Visual bugs compile clean — the sidebar `data-active="false"` bug only showed up in screenshots, twice.
5. Then regenerate baselines with `make screenshots` and verify with `make screenshots-check` (must pass). Both run inside the pinned Playwright Docker image, so they work on any host (Linux/macOS) and produce the committed `-linux` baselines — never run a bare `bun run test:ui:update`, whose host-rendered output drifts from the container (see below).

## Visual regression suite (web/tests/)

Fully deterministic and self-contained: `test:ui`'s `webServer` runs `bun run build && bun run preview` on **:4173** against the network-mocked API (`tests/mock-api.ts`) — it needs no backend and does **not** use the docker web (:3000, which is for manual inspection only). Clock frozen, locale/timezone pinned, animations disabled, diff budget 50px.

Baselines are **Linux-only** — the snapshot path hard-codes a `-linux` suffix (not `{platform}`) so every host compares against the one committed set (`web/tests/__snapshots__/*-linux.png`). To keep renders byte-stable across host OSes, both generation and verification run inside the **pinned Playwright image** (`mcr.microsoft.com/playwright:v<version>-noble`, matching `@playwright/test` in `web/package.json`), which installs bun on the fly and bind-mounts the repo:

- `make screenshots` — regenerate baselines (`test:ui:update`); run after any intentional UI change and commit the diff.
- `make screenshots-check` — verify against committed baselines (`test:ui`); this is the gate CI runs.

Both work identically on Linux **and macOS** because the host browser/fonts are never used. Do **not** run a bare `bun run test:ui:update` on the host — host fonts/emoji differ from the container and silently corrupt the committed baselines. (No `make`? Run the equivalent `docker run …` from the README "Visual regression" section.) When adding API surface that pages consume, extend the mock fixtures or the affected tests will silently render empty states. An unexpected snapshot failure means the UI changed when it shouldn't have; an intentional change means regenerating baselines and committing the diff.

## Gotchas

- Host ports 8080 and 5173 are occupied by other projects on this machine — that's why the API is :9000. Web is :3000.
- `vite build` succeeding does not mean the TS is sound; always run svelte-check — but it's not a declared dep (no `check` script); `bunx svelte-check` fetches it on demand.
- Postgres 18's Docker image keeps PGDATA under `/var/lib/postgresql/<major>`, so the volume mounts the parent dir (not `.../data`).
- A local MCP test server for e2e is **not in the repo** — create it by hand per session at `/tmp/oarlock-mcp-test/server.mjs` (port 7777, bearer `test-secret-123`) and add an MCP server pointing at `host.docker.internal:7777`; nothing wires it up automatically.
- Two different dev master keys exist: `docker-compose.yml` sets `OARLOCK_MASTER_KEY`, but `go run ./cmd/api` with no env falls back to a *different* hardcoded key (`vault.go`). Secrets encrypted under one won't decrypt under the other — keep the env var consistent across `make up` and local `go run`.
- Step types (palette): `ai.prompt`, `mcp.tool`, `http.request`, `transform`, `delay`, and `container.run` (only when a container backend is configured). The old `log` step was removed in migration `0007`.
- **SeaweedFS dev S3 requires an identity config** — with none, it rejects signed requests ("Signed request requires setting up SeaweedFS S3 authentication") even though bucket-create succeeds. `docker-compose.yml` mounts `dev/seaweedfs-s3.json` (`-s3.config`) granting the `oarlock`/`oarlockdev` keys; the "Failed to load IAM configuration … STS" log line is unrelated (static keys still work).
- **Two engines on one DB share River's `tasks` queue.** For the k8s backend test, the host `go run` engine and the compose `api` (docker backend) both poll the same queue, so a docker engine can grab a k8s task → "compute target uses backend k8s but this engine runs docker". `docker compose stop api` before running a host k8s engine. Local k8s e2e: `kind create cluster` with `KIND_EXPERIMENTAL_DOCKER_NETWORK=oarlock_default` (so pods reach SeaweedFS at its container IP via `OARLOCK_S3_POD_ENDPOINT`), `kind load docker-image oarlock-runner:dev`, run a host engine with `OARLOCK_CONTAINER_RUNTIME=k8s`.
- Definitions store interpolatable strings; a step referencing `{{input.x}}` run with empty input interpolates to empty string — by design. The expression context binds `steps.<key>` for **every succeeded step** in the run (not just declared `needs`); the dependency guarantee comes from execution *ordering*, not context filtering, so referencing a non-dependency step is possible but racy/undefined.
