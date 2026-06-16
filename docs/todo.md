# Oarlock — Build TODO

Tracks progress against [design.md](design.md) §6 build order. Each step ends runnable.
Status: `[ ]` todo · `[~]` in progress · `[x]` done

## Phase 1 — Engine + single-player

**Exit criteria: replaced one of your own real automations and trust it.**

### 0. Quality gates
- [x] UI snapshot tests: Playwright visual regression in `web/tests` (mocked API, frozen clock, 8 baselines incl. dark mode; `bun run test:ui`, regenerate with `test:ui:update`)
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
- [x] `delay` (now suspension-backed: writes a suspensions row + scheduled resume, frees the worker slot, arbitrary durations)
- [x] ~~`log`~~ removed in migration 0007 — default task logging made it redundant
- [x] `container.run` — run any Docker image (Docker locally, k8s Jobs at scale); managed artifact store (SeaweedFS/R2) stages files in/out; compute targets + `registry` secrets + image allowlist + container-seconds metering
- [x] **★ multi-step diamond workflow runs end-to-end via API/UI**

### 6b. Suspension engine (built with container steps)
- [x] `Suspended` sentinel + `Resumable` interface (additive; synchronous steps untouched)
- [x] `suspendTask` (status=suspended + suspensions row + scheduled `resume_task`, one tx) + widened `finishTask` guard
- [x] `resume_task` worker (poll re-suspends with backoff; terminal finalizes), shared `prepareTask` prelude (redactor rebuilt per invocation)
- [x] `POST /v1/resume/{token}` callback (unauthenticated capability), `reconcile_suspensions` safety net, cancel kills the container/Job
- [x] `gc_artifacts` periodic sweep (retention via `artifacts.expires_at`)

### 8. Expressions
- [x] goja with per-eval time limits + context cancel
- [x] `{{ }}` interpolation (single-expression keeps native type)
- [ ] Frozen-context hardening + memory limits

### 9. Triggers
- [ ] Webhook endpoint `/hooks/{ws}/{path}` (HMAC, idempotency)
- [x] Manual-run API
- [ ] Cron (River periodic jobs)

### 10. REST API v0
- [x] Workflows CRUD + immutable definition versions
- [x] Runs: start / list / detail (with tasks)
- [x] Runs: cancel (guards beat in-flight results) / retry-from-failed (fresh attempts for failed steps)
- [x] Runs: SSE event stream `/v1/runs/{id}/events` (Valkey pub/sub ping → Postgres refetch)
- [ ] Single dev token auth (open + CORS-permissive for dev)

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
- [ ] YAML view (CodeMirror) of the same document
- [x] Browser e2e verified (Playwright: drop → configure → save → run → succeeded)

## Phase 2 — Multi-tenant cloud alpha

**Exit criteria: 10–20 external workspaces, weekly-active runs, zero isolation incidents.**

- [~] 14. Auth + tenancy — pulled forward: sessions table (migration 0003), first-run auto-login as seeded workspace owner, /v1/me, all API routes resolve workspace from session membership (no hardcoded tenant), credentialed CORS. Still todo: signup/login UI, multi-user, WorkspaceDB handle, RLS on, per-workspace concurrency cap, JWT cell claims
- [~] 15. Secrets vault: typed secrets (`generic` | `api_key`) AES-256-GCM under OARLOCK_MASTER_KEY; api_key type = BYOK (anthropic/openai/openrouter); `{{secrets.<name>}}` usable in any step config/script; values redacted from task input/output/errors, task_logs, log downloads, and stdout; masked list API; delete blocked while referenced (ai.* api_key refs + secrets.<name> text refs); Configuration UI. Todo: true envelope (per-row data keys), `http.request` secret auth
- [~] 16. AI steps, BYO keys: `ai.prompt` (provider dispatch, api_key secret select in UI). Todo: `ai.classify` structured output
- [ ] 17. Integrations v1 (exactly five): Slack, email, Google Sheets, GitHub, webhook-out
- [ ] 18. MCP server per workspace: `list_workflows` / `run_workflow` / `get_run_status` (+ record launch demo)
- [~] 19. Control flow: `if/branch` done — `condition` step (If/Then/Else), visual AND/OR rule builder + raw-JS expression escape hatch (reuses goja), two then/else output handles, branch-labeled edges. Engine prunes the untaken branch via idempotent skip-propagation in `advance_run` (`computePlan`: branch-dead + reachability + pruned-edge-satisfies-dep, internal fixpoint for nested conditions); untaken steps get `skipped` rows. Todo: `code.js` (hardened goja), `foreach`
- [ ] 20. Production: DOKS + managed PG, Helm/ArgoCD, OTel→Grafana, GlitchTip, restore-drilled backups, Stripe stub
- [ ] Alpha polish: signup, docs w/ 5 recipes, ~10 templates, usage metering, rate limits

## Phase 3 — First paying customers

**Exit criteria: 5–10 paying workspaces, MRR > infra floor, repeatable onboarding.**

- [ ] OIDC SSO on paid plans; RBAC enforced; scoped API tokens
- [ ] Append-only `audit_events`; retention tiers + export; status/security pages
- [ ] Stripe live; soft-warn-then-throttle fair use (never silent halt)
- [ ] Tier-2 isolation (dedicated queue/workers) as Business upsell
- [ ] Demand-driven: subprocess/WASM sandbox, first dedicated cell
- [x] BYO containers (pulled forward from Phase 3): `container.run` step — Docker locally + gVisor-capable k8s Jobs at scale, suspension-based async, managed artifact store, metered container-seconds (the executor/billing boundary). See §6b/§7.
- [x] MCP client (pulled forward from Phase 3): workspace `mcp_servers` (encrypted auth, enable/disable), `mcp.tool` step, live tools discovery, delete/rename blocked while referenced by a workflow's current version, /mcp management UI + dynamic step config selects
- [ ] Platform operator console (control-plane surface, separate from tenant UI/API) — approach documented in [platform-administration.md](platform-administration.md)
