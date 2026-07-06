# Oarlock — Build TODO

Tracks progress against [design.md](design.md) §6 build order. Each step ends runnable.
Status: `[ ]` todo · `[~]` in progress · `[x]` done

## Phase 1 — Engine + single-player

**Exit criteria: replaced one of your own real automations and trust it.**

### 0. Quality gates
- [x] UI snapshot tests: Playwright visual regression in `web/tests` (mocked API, frozen clock, 8 baselines incl. dark mode; `npm run test:ui`, regenerate with `test:ui:update`)
- [x] Engine unit tests (definition/DAG validation)

### 1. Monorepo + dev stack
- [x] Repo layout: `/engine` (Go), `/web` (SvelteKit), `/deploy` (later)
- [x] Docker Compose: Postgres 18 + Valkey 9 + api + web services, healthchecks
- [x] `engine/Dockerfile` + `web/Dockerfile` multi-stage builds
- [x] Minimal Go api: `/` (version), `/healthz` (PG + Valkey checks), graceful shutdown
- [ ] CI (build + test on push)

### 2. Schema + data layer
- [x] Migration 0001 = full schema from design.md §5 (cells, workspaces, users, members, workflows, versions, triggers, connections, runs, tasks, suspensions) — task_logs deferred to step 11
- [x] Migration tooling (embedded SQL runner + schema_migrations; River via rivermigrate)
- [x] pgx (sqlc deferred — raw pgx queries for now)
- [ ] RLS policies on all `workspace_id` tables
- [x] Seed dev workspace (`cell-0`, dev user)

### 3. Definition format v0
- [x] Go structs for Definition / Step / `needs` edges (+ `ui` canvas positions)
- [x] Validator: unique keys, known types, needs resolution, cycle detection
- [ ] Published JSON Schema (step config specs served via /v1/step-types for now)
- [ ] YAML↔JSON conversion at the boundary
- [x] Validator unit tests (diamond, cycle, self-need, dup keys)

### 4. River wiring
- [x] River client + workers (in-process with API; dedicated worker binary later)
- [x] `control` / `tasks` queues
- [x] Graceful shutdown / drain

### 5. advance_run
- [x] Readiness computation (all `needs` satisfied), idempotent re-derive from rows
- [x] Transactional task insert + `execute_task` enqueue (job + state, one tx — hard rule 2)
- [ ] DB-backed unit tests for DAG math (validated manually: diamond + fan-out via API)

### 6. Executor framework
- [x] `Executor` interface (design §4.2)
- [x] Step-type registry + config specs (drives UI property panels)
- [x] Context assembly (`input` + `steps.<key>` outputs), log sink
- [x] Retry mapping: step-level `retries` (0–10) → new task attempt rows with exponential backoff (2s, 4s, 8s…)

### 7. Native steps ★ MILESTONE
- [x] `http.request` (method/url/body/headers, 1MB body cap, JSON auto-parse)
- [x] `transform` (goja, 5s limit)
- [x] `delay` (v0 in-process ≤5min; suspension + scheduled resume later)
- [x] ~~`log`~~ removed in migration 0007 — default task logging made it redundant
- [x] **★ multi-step diamond workflow runs end-to-end via API/UI**

### 8. Expressions
- [x] goja with per-eval time limits + context cancel
- [x] `{{ }}` interpolation (single-expression keeps native type)
- [x] Context scoped to a step's transitive `needs` (no sibling-output leakage; unit + DB tested)
- [ ] Frozen-context hardening + memory limits

### 9. Triggers
- [x] Webhook endpoint `POST /hooks/{ws-slug}/{path}` (optional HMAC-SHA256 over raw body, X-Idempotency-Key, body+query as run input; unknown → generic 404, disabled → 403)
- [x] Manual-run API (+ optional idempotency_key passthrough)
- [x] Cron (30s scheduler sweep, 90s lookback, per-occurrence idempotency key `cron:<trigger>:<unix>` → multi-replica-safe with zero locks)
- [x] Trigger CRUD API (`/v1/workflows/{id}/triggers`, `PATCH|DELETE /v1/triggers/{id}`) + editor Triggers panel; `is_enabled` on the workflow gates trigger firing (manual runs still allowed)
- [x] Migration 0008: partial unique index on webhook path per workspace

### 10. REST API v0
- [x] Workflows CRUD + immutable definition versions
- [x] Workflow PATCH (rename, enable/disable); version history endpoints (`GET .../versions`, `.../versions/{n}`); rollback = re-save old definition
- [x] Runs: start / list / detail (with tasks)
- [x] Runs: cancel (guards beat in-flight results) / retry-from-failed (fresh attempts for failed steps)
- [x] Runs: SSE event stream `/v1/runs/{id}/events` (Valkey pub/sub ping → Postgres refetch; 250ms ping coalescing; poll-only fallback when Valkey down)
- [x] Idempotency-key on run start; keyset pagination on runs (`?limit&before`) and logs (`?limit&before_id`)
- [x] Hardening: CORS origin allowlist (`OARLOCK_ALLOWED_ORIGINS`), 1MB body cap, ReadHeader/Idle timeouts, 5xx message sanitization, sha256-hashed session tokens + `POST /v1/logout` + expired-session cleanup
- [x] Secret rotation (`PUT /v1/secrets/{id}`); stateless MCP connection test (`POST /v1/mcp-servers/test`); secret-reference match is word-boundary (no `foo`/`foobar` false positive)
- [ ] Single dev token auth (superseded: session auto-login + workspace API tokens for programmatic access)

### 10b. Engine durability (design §4.1)
- [x] Per-task `JobTimeout` override (15m; fixes the 60s River default that killed long delays / AI calls)
- [x] Orphaned-task reaper: fails tasks stuck `running` >20min and honors step retries (worker crash / deploy restart no longer hangs a run)
- [x] Per-step `timeout` field (0–600s) wrapping the executor context
- [x] Suspensions: `delay` >5min and `wait.callback` free the worker slot (suspensions row + scheduled `resume_task` job / `POST /resume/{token}` callback); cancel-while-suspended is a clean no-op

### 11. Logs
- [x] task_logs table (partitioned by ts, default partition, BRIN index) + capped writer (8KB/line, ~1MB/task)
- [x] Default log capture: per-task slog sink teed to stdout; engine lifecycle lines (started/succeeded/failed) mean every task logs with no explicit log step
- [x] Logs API: GET /v1/runs/{id}/logs (newest first) + /logs.txt download (chronological)
- [x] UI: Logs tab in editor run panel (live, 2s poll) + run detail page; download button
- [ ] Weekly partition maintenance job + retention DROP PARTITION
- [ ] Pagination (currently last 1000 lines)

### 12. Web shell
- [x] Admin dashboard home (`/`): real workspace stats from GET /v1/stats — stat cards, 14-day stacked run chart, task-outcome donut, top workflows w/ failure bars, recent activity feed, resource links; auto-refreshes
- [x] `/web` SvelteKit scaffold (Svelte 5 + Tailwind 4 + adapter-node)
- [x] shadcn-svelte components, custom theme (primary #ffcc00), Geist + Geist Mono brand fonts, light/dark via mode-watcher, sidebar nav with lucide icons (Workflows / MCP Servers / Configuration)
- [x] Workflow list (create/delete) with per-workflow run counts + failure %
- [x] Run history page: stats cards (total runs, failure rate, avg duration, active), status icons, relative times, error summaries
- [x] Run detail: execution rendered on the read-only visual builder canvas (pinned version), node click → per-attempt outputs/errors
- [x] Live run/task updates via SSE ← Valkey pub/sub (EventSource; polling removed)
- [x] Cancel + retry-from-failed buttons (editor run panel + run detail)
- [ ] Live *log* streaming (task_logs table doesn't exist yet — step 11)

### 13. Canvas ★ shipped early (vertical slice)
- [x] Svelte Flow (@xyflow/svelte) editing the definition: drag-and-drop palette, connect `needs` edges, rename keys, delete steps
- [x] Property panels generated from step-type config specs
- [x] Save = new immutable version; node statuses live-update during runs
- [x] Unsaved-changes navigation guard; reference-aware step rename (rewrites `steps.<key>` in other configs); run-with-input dialog; version history panel + restore; inline workflow rename; per-row enable toggle
- [x] Inspector: per-step `timeout` + `if` (run condition) fields; `skipped`/`suspended` node + badge styling; code.js / wait.callback palette entries
- [ ] YAML view (CodeMirror) of the same document
- [x] Browser e2e verified (Playwright: drop → configure → save → run → succeeded)

### 0b. Tests + CI (added)
- [x] GitHub Actions (`.github/workflows/ci.yml`): engine (build/vet/gofmt/test with Postgres service) + web (svelte-check/build/vitest)
- [x] DB-backed engine tests: diamond DAG, failure, retry, cancel-vs-late-result, advance idempotency, context scoping, reaper, if-guards, suspensions, triggers scheduler, MCP server end-to-end (real SDK client)
- [x] Unit: interpolation, redaction, code.js console, HMAC, cron occurrence, token hashing; web: `flow.ts` definition⇄canvas round-trip (vitest)

## Phase 2 — Multi-tenant cloud alpha

**Exit criteria: 10–20 external workspaces, weekly-active runs, zero isolation incidents.**

- [~] 14. Auth + tenancy — pulled forward: sessions table (migration 0003), first-run auto-login as seeded workspace owner, /v1/me, all API routes resolve workspace from session membership (no hardcoded tenant), credentialed CORS. Still todo: signup/login UI, multi-user, WorkspaceDB handle, RLS on, per-workspace concurrency cap, JWT cell claims
- [~] 15. Secrets vault: typed secrets (`generic` | `api_key`) AES-256-GCM under OARLOCK_MASTER_KEY; api_key type = BYOK (anthropic/openai/openrouter); `{{secrets.<name>}}` usable in any step config/script; values redacted from task input/output/errors, task_logs, log downloads, and stdout; masked list API; delete blocked while referenced (ai.* api_key refs + secrets.<name> text refs); Configuration UI. Todo: true envelope (per-row data keys), `http.request` secret auth
- [~] 16. AI steps, BYO keys: `ai.prompt` (provider dispatch, api_key secret select in UI). Todo: `ai.classify` structured output
- [x] 16b. AI token-usage capture (input/output tokens persisted in `ai.prompt` output for future metering)
- [ ] 17. Integrations v1 (exactly five): Slack, email, Google Sheets, GitHub, webhook-out
- [x] 18. MCP server per workspace: `list_workflows` / `run_workflow` / `get_run_status` (streamable HTTP at `/mcp`, `oak_` bearer tokens in `workspace_api_tokens` (migration 0009), token management + MCP-access UI; end-to-end tested) — *todo: record launch demo*
- [x] 19. Control flow: `code.js` (hardened goja, console→task log, 30s limit) + `if/branch` (skip propagation). *foreach still deferred (design §7)*
- [ ] 20. Production: DOKS + managed PG, Helm/ArgoCD, OTel→Grafana, GlitchTip, restore-drilled backups, Stripe stub
- [ ] Alpha polish: signup, docs w/ 5 recipes, ~10 templates, usage metering, rate limits

## Phase 3 — First paying customers

**Exit criteria: 5–10 paying workspaces, MRR > infra floor, repeatable onboarding.**

- [ ] OIDC SSO on paid plans; RBAC enforced; scoped API tokens
- [ ] Append-only `audit_events`; retention tiers + export; status/security pages
- [ ] Stripe live; soft-warn-then-throttle fair use (never silent halt)
- [ ] Tier-2 isolation (dedicated queue/workers) as Business upsell
- [ ] Demand-driven: subprocess/WASM sandbox, first dedicated cell
- [x] MCP client (pulled forward from Phase 3): workspace `mcp_servers` (encrypted auth, enable/disable), `mcp.tool` step, live tools discovery, delete/rename blocked while referenced by a workflow's current version, /mcp management UI + dynamic step config selects
- [ ] Platform operator console (control-plane surface, separate from tenant UI/API) — approach documented in [platform-administration.md](platform-administration.md)

## Known gaps / deferred (tracked, not blocking)

Security (before any non-localhost exposure):
- RLS still off (hard rule 3 wants it on); WorkspaceDB handle + `SET LOCAL app.workspace_id` not wired.
- No SSRF guard on `http.request` / MCP / webhook-out URLs (fine for self-host, blocking for multi-tenant cloud).
- Per-workspace concurrency cap (design's single fairness knob + pricing lever) not implemented.
- Signup/login UI + multi-user still absent (auto-login bootstrap remains).

Product/UX:
- `foreach` iteration; `ai.classify` structured output; the five v1 integrations.
- Canvas undo/redo; YAML/CodeMirror definition view; definition import/export.
- Log retention job (weekly partition DROP); `http.request` secret-based auth; true envelope encryption (per-row data keys).
