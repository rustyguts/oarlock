# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

oarlock is a self-hostable workflow automation platform: a drag-and-drop canvas builder over an event-driven Go engine. `docs/project.md` is the binding project document — its glossary names (Workspace, Workflow, Version, Definition, Step, Task, Run, Secret, Suspension, Cell) and eight "hard rules" govern naming and architecture decisions. Its "Remaining work" section tracks what's left; update it when completing or rescoping work.

## Commands

```sh
make up                                   # full stack: Postgres 18, Valkey 9, API :9000, web :3001
docker compose up -d --build api          # rebuild after engine/ changes
docker compose up -d --build web          # rebuild after web/ changes

cd engine && go build ./... && go vet ./... && go test ./...
cd engine && go test ./internal/definition/ -run TestValidateCycle   # single test

cd web && bun install                     # install deps (uses bun.lock)
cd web && bun run build                   # vite build (does NOT typecheck)
cd web && bun run check                   # typecheck via svelte-check (vite won't catch TS errors)
cd web && bun run test:unit               # vitest (flow.ts round-trip etc.)
cd web && bun run test:ui                 # Playwright visual regression (no backend needed)
cd web && bun run test:ui:update          # regenerate snapshot baselines after intentional UI changes
cd web && bunx playwright test -g "sidebar"            # single snapshot test
```

Migrations are embedded SQL in `engine/internal/db/migrations/`, applied automatically at API startup in filename order (tracked in `schema_migrations`); River's own tables migrate via `rivermigrate`. There is no down-migration mechanism — dump first (`docker compose exec -T postgres pg_dump -U oarlock oarlock > backups/...`).

## Engine architecture (engine/)

The non-negotiable core: **no process owns a run; truth lives in Postgres rows**. River job types:

- `advance_run` (`internal/engine/advance.go`): loads the definition + latest task attempt per step, computes ready steps (all `needs` succeeded/skipped), inserts task rows and enqueues `execute_task` jobs **in one transaction**, marks the run succeeded/failed when terminal. Idempotent — re-derives everything from rows, so duplicate advances converge. A ready step with an `if` guard is resolved here: a falsy expression writes a terminal `skipped` row (no execute job), an eval error a `failed` row; whenever a guard row is inserted, one follow-up `advance_run` is enqueued in the same tx (nothing else would re-advance since those rows carry no job). Guard context is scoped to the step's transitive `needs` and does **not** bind secrets (conditions must not decrypt the vault on the control queue).
- `execute_task` (`internal/engine/execute.go`): loads the task, builds the frozen expression context (`input`, the outputs of the step's *transitive* `needs` only via `definition.TransitiveNeeds` — siblings never leak in, `secrets.*`), interpolates `{{ }}` config via goja, runs the executor (optionally under a per-step `timeout`), persists the result, and enqueues the next `advance_run` in the same transaction as the status write. A worker-level `Timeout()` caps a task attempt at 15m (River's default is 60s — do not remove this).
- `resume_task` (`internal/engine/resume.go`): revives a suspended task (delay >5min or `wait.callback`). Suspension frees the worker slot: the task goes `suspended` + a `suspensions` row is written; a timed delay schedules this job at `resume_at`, a callback waits for `POST /resume/{token}`. Resume flips `suspended`→`succeeded` and enqueues `advance_run`, all one tx. A `reaper` goroutine (`reaper.go`) fails tasks stuck `running` >20min (worker crash / deploy) and honors step retries; a `scheduler` goroutine (`scheduler.go`) fires cron triggers, deduped by a per-occurrence idempotency key so multiple replicas converge to one run.

Unauthenticated ingress (mounted outside the session-auth wrapper, inside `MaxBody`+CORS): `POST /hooks/{ws-slug}/{path}` (webhook triggers, optional HMAC-SHA256 over the raw body), `POST /resume/{token}` (callback suspensions), `/mcp` (workspace MCP server, `oak_` bearer token → workspace). All resolve the workspace from the URL/token, never a session.

Hard rule 2 in practice: **a job insert and its corresponding state write always share a transaction** (`river.Client.InsertTx`). Status transitions are guarded (`where status = 'queued'` / `in ('queued','running')`) so a concurrent cancel beats an in-flight executor's late result. Retries are new task rows (`attempt+1`, scheduled with exponential backoff), never River-level job retries (`MaxAttempts: 1`).

Step executors implement the one `Executor` interface (`internal/steps`); execution strategy is a property of the step type, invisible to the engine. The registry's `TypeInfo.ConfigSpec` drives the UI property panels — adding a step type there is all that's needed for it to appear in the palette and inspector. Executors needing workspace resources get them via `steps.Services` (resolver interfaces implemented by `internal/vault`).

**Secrets and redaction** (hard rule 6): secrets are AES-256-GCM encrypted (`internal/vault`, master key from `OARLOCK_MASTER_KEY`, dev fallback). At task execution the engine decrypts all workspace secrets, binds them as `secrets.*` context, and builds a `redactor` whose values are scrubbed from **everything persisted or emitted**: task input/output/error JSON, every `task_logs` row, and the stdout tee in the task log handler (`internal/engine/dblog.go`). Any new code path that persists or logs task-derived data must go through the taskRef's redactor.

**Logging**: every task automatically gets a DB-backed log trail (lifecycle lines + whatever executors write to `TaskInput.Log`) via a per-task slog handler that writes capped lines (8KB/line, ~1MB/task) to the partitioned `task_logs` table and tees redacted records to the process log.

**Live updates**: workers publish fire-and-forget pings to Valkey channel `run:{id}` after each state change; the SSE endpoint (`/v1/runs/{id}/events`) refetches the run snapshot from Postgres per ping. Valkey is never load-bearing for correctness — Postgres remains truth.

**Tenancy**: every API request resolves a workspace from the session (cookie auth with first-run auto-login as the migration-seeded owner — `internal/api/auth.go`; session tokens are stored sha256-hashed, raw token only in the cookie); all queries filter by `workspace_id` via `s.workspace(r)`. Never join across workspaces (hard rule 7). CORS is credentialed against an explicit origin allowlist (`OARLOCK_ALLOWED_ORIGINS`, default `http://localhost:3001`) — never wildcard. Programmatic access (the `/mcp` endpoint) authenticates via `workspace_api_tokens` (`oak_` tokens, sha256-hashed) instead of a session; the token *is* the workspace credential. Request bodies are capped at 1MB (`api.MaxBody`) and 5xx responses are sanitized to `{"error":"internal error"}`.

**Reference protection**: secrets and MCP servers are referenced from step configs *by name*. Deleting (or renaming) one that a workflow's current version references must 409 with the referencing workflow names — see `referencingWorkflows` (jsonb step-config scan) and `workflowsMentioning` (definition regex scan for `secrets.<name>`, word-boundary so `foo` isn't blocked by `foobar`) in `internal/api/resources.go`. New referenceable resources need the same treatment. Secrets can be rotated in place (`PUT /v1/secrets/{id}`) without unwiring references — the only way to change a value while it's referenced.

## Web architecture (web/)

SvelteKit (Svelte 5 runes) + Tailwind 4 + shadcn-svelte + `@xyflow/svelte`. The browser calls the Go API directly (`PUBLIC_API_URL`, default `http://localhost:9000`) with `credentials: 'include'`; there are no SvelteKit server routes.

The **JSON definition is the only canonical workflow artifact** (hard rule 4): `src/lib/flow.ts` converts definition⇄canvas (node id = step key; edge source→target = "target needs source"; canvas positions persist in each step's `ui` field — the engine ignores them). Saving the canvas creates a new immutable version. The run detail page renders the **pinned** version a run executed, not the current one.

Live run state arrives via `watchRun` (EventSource wrapper in `src/lib/api.ts`); the server closes the stream after the terminal snapshot. No polling for run state — only the runs *list* and *logs* panels poll.

The Inspector renders step config forms from the API's `config_spec`; kinds `api_key`/`mcp_server`/`mcp_tool` are dynamic selects (api_key lists only api_key-typed secrets; mcp_tool fetches the chosen server's live tool list).

**Design language (Brendan's house style)**: every component shares the *same* background color — never a lighter card on a darker page (or vice versa). Separation comes from **borders**, not background contrast. This is encoded in the theme tokens (`--card`, `--popover`, `--sidebar` are all `var(--background)` in both modes); don't introduce surfaces with their own background tones. Subtle `bg-muted` fills are fine for *insets* (icon tiles, code/log blocks), not for panels. Primary pages (workflow list, run history) use the full page width (`w-full px-6`), not centered max-width columns. Brand font is Geist (+ Geist Mono), loaded via Fontsource. Brand-colored **text/icons on the background use `text-primary-strong`**, not `text-primary` — raw #ffcc00 is unreadable on white, so the token is a darkened same-hue gold in light mode and #ffcc00 in dark. `bg-primary` with `text-primary-foreground` (filled buttons) is fine in both modes.

shadcn-svelte components live in `src/lib/components/ui/` and are project-owned — editing them is fine and sometimes necessary (e.g. `sidebar-menu-button.svelte` omits `data-active` when false because its styles use presence-based variants). Unused subcomponents have been pruned from the vendored kits; re-add via `bunx shadcn add <component>` if needed. Icons: `@lucide/svelte`, imported per-icon from `@lucide/svelte/icons/<name>`. Brand primary is exactly `#ffcc00` (theme vars in `src/app.css`); active nav items use it for icon + text.

## Frontend screenshot loop (required)

Whenever you change the frontend, **verify by looking at it, not just by building it**:

1. Build + rebuild the web container.
2. Drive a real browser (Playwright chromium is installed; scratch scripts in `/tmp` work) against http://localhost:3001 — visit the changed screens in **light and dark mode** (`button[aria-label="Toggle theme"]`).
3. Screenshot and **actually read the pixels** against what was asked: spacing, contrast, active states, phantom backgrounds, overflow.
4. Iterate until right. Visual bugs compile clean — the sidebar `data-active="false"` bug only showed up in screenshots, twice.
5. Then regenerate baselines: `bun run test:ui:update && bun run test:ui` (must pass twice).

## Visual regression suite (web/tests/)

Fully deterministic: the API is mocked at the network layer (`tests/mock-api.ts`), the clock is frozen, locale/timezone pinned, animations disabled, diff budget 50px. Baselines are platform-suffixed (`-darwin`) and intentionally NOT run in CI. When adding API surface that pages consume, extend the mock fixtures or the affected tests will silently render empty states. An unexpected snapshot failure means the UI changed when it shouldn't have; an intentional change means regenerating baselines and committing the diff.

## Gotchas

- Host ports 8080, 3000, and 5173 are occupied by other projects on this machine — that's why the API is :9000 and web is :3001.
- `vite build` succeeding does not mean the TS is sound; always run svelte-check.
- Postgres 18's Docker image keeps PGDATA under `/var/lib/postgresql/<major>`, so the volume mounts the parent dir (not `.../data`).
- Engine DB-backed tests self-skip without Postgres; CI runs them with `go test -p 1` because the engine and api test packages share one `DATABASE_URL_TEST` database and truncate between tests.
- A local MCP test server for e2e lives at `/tmp/oarlock-mcp-test/server.mjs` (port 7777, bearer `test-secret-123`); the engine reaches it via `host.docker.internal`.
- Definitions store interpolatable strings; a step referencing `{{input.x}}` run with empty input interpolates to empty string — by design (expressions only see declared dependencies' outputs and provided input).
