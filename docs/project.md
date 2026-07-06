# oarlock

*The project document: design, current features, and remaining work in one
place. This consolidates the former `design.md`, `todo.md`, and
`platform-administration.md`. It is the binding plan — its glossary names and
eight hard rules govern naming and architecture decisions.*

## 1. What this is and why it can win

A self-hostable (later also cloud) workflow automation platform: a
drag-and-drop canvas builder over an event-driven Go engine. The market (n8n,
Activepieces, Windmill, Kestra, Temporal, Prefect) gives features away and
charges for managed hosting and enterprise governance. The wedge:

- **Flat, predictable pricing** — unlimited executions, no metering shock, no
  halted workflows. Bill flat tiers gated on **concurrent task slots**, never
  per-execution.
- **SSO + basic RBAC included on paid plans** — incumbents gate these at
  $150–667/mo.
- **MCP-native** — every workflow is a tool an AI agent can call; the MCP
  ecosystem is the integration long-tail a solo builder can't hand-write.
- **BYO AI keys** — customers' own Anthropic/OpenAI/OpenRouter keys; zero AI
  COGS, better privacy story.

Target tiers: Free = self-host · Hobby ~$9 · Pro ~$29–49 (SSO, more
concurrency) · Business ~$99–149 (audit logs, dedicated workers) · Enterprise
= dedicated cell.

### Stack (decided)

| Layer | Choice | Note |
|---|---|---|
| Core engine | **Go** | Goroutines make blocked I/O nearly free |
| Queue | **River** (Postgres-backed) | Transactional enqueue: job + state in one tx |
| Database | **PostgreSQL** | Single source of truth; River lives in it |
| Cache / pub-sub | **Valkey** | Live UI updates only; never load-bearing for correctness |
| Frontend | **SvelteKit** + Tailwind 4 + shadcn-svelte + **@xyflow/svelte** | Svelte Flow is the biggest time-saver in the project |
| Expressions / JS steps | **goja** (embedded JS) | In-process, sandboxed |
| Object storage | R2 or DO Spaces (later) | Free egress |
| Production (later) | DOKS + managed Postgres, then Hetzner k3s at scale | Workers are stateless, so migration is cheap |

## 2. Glossary (use these names everywhere)

- **Workspace** — the tenant; every tenant-owned row carries `workspace_id`.
- **Workflow** — a named automation; points at its current Version.
- **Version** — immutable snapshot of a Definition; runs pin to one.
- **Definition** — the declarative JSON document (canonical artifact). The
  canvas (and any future YAML/SDK) are views of it.
- **Step** — a node in the definition (design-time).
- **Task** — one execution attempt of a step in a run (run-time; ≈ one River
  job; retry = new task row, attempt+1).
- **Run** — one execution of a workflow version end-to-end.
- **Trigger** — webhook | schedule | manual.
- **Secret** — encrypted workspace value (type `generic` or `api_key`),
  referenced by name, never inlined; values are redacted from run records and
  logs.
- **Suspension** — persisted "waiting" record that frees the worker during
  long waits.
- **Cell** — an isolated deployment unit (API + workers + Postgres). Day one
  there is exactly one: `cell-0`.

## 3. Architecture

### Engine: event-driven state machine (the non-negotiable foundation)

No process owns a run. Truth lives in Postgres rows; workers are stateless
muscle. Job types:

1. **`advance_run`** — load the definition + latest task attempt per step,
   compute ready steps (all `needs` succeeded/skipped), insert task rows +
   enqueue `execute_task` jobs in one transaction, mark the run terminal when
   done. Idempotent: it re-derives everything from rows, so duplicate
   advances converge. `if` guards resolve here (falsy → terminal `skipped`
   row, eval error → `failed` row, neither with an execute job; a follow-up
   advance is enqueued in the same tx). Guard context never binds secrets.
2. **`execute_task`** — build the frozen expression context (`input`, outputs
   of the step's *transitive* `needs` only, `secrets.*`), interpolate
   `{{ }}` config via goja, run the executor (optionally under a per-step
   `timeout`), persist the result, and enqueue the next `advance_run` in the
   same transaction as the status write.
3. **`resume_task`** — revive a suspended task when a timed delay comes due.

**Long waits:** a short wait parks a goroutine (~free in Go). A `delay` over
5 minutes or a `wait.callback` step **suspends** instead: the task goes
`suspended` + a `suspensions` row is written and the worker slot frees. A
timed delay schedules `resume_task` at `resume_at`; a callback waits for
`POST /resume/{token}` (the unguessable token is the credential; only its
sha256 is stored). Cancel-while-suspended is a clean no-op.

**Recovery:** a `reaper` goroutine fails tasks stuck `running` >20min (worker
crash / deploy) and honors step retries. A worker-level timeout caps a task
attempt at 15m. Retries are new task rows with exponential backoff, never
River-level job retries (`MaxAttempts: 1`).

**Fairness (planned):** one knob — a per-workspace concurrent-task cap
checked at dequeue. This is also what pricing tiers gate.

**Live UI:** workers publish fire-and-forget pings to Valkey channel
`run:{id}`; the SSE endpoint refetches the run snapshot from Postgres per
ping. Valkey down = slower polling, never wrong data.

### Executor abstraction

Execution strategy is a property of the *step type*, invisible to the engine:

```go
type Executor interface {
    Execute(ctx context.Context, in TaskInput) (TaskOutput, error)
}
```

In-process (native steps + goja) is the default forever. Subprocess/WASM
sandboxes and k8s-Job/gVisor containers land later behind the same interface
— the executor boundary and the billing boundary are the same line.

The registry's `TypeInfo.ConfigSpec` drives the UI: adding a step type to the
registry is all that's needed for it to appear in the palette and inspector.

### Definition format

Canonical: a versioned **JSON document** in `workflow_versions.definition`
(jsonb). The canvas edits the same document; canvas positions persist in each
step's `ui` field (the engine ignores them). `needs` edges define the DAG;
`if` guards skip; `{{ }}` goja expressions evaluate against a frozen context
scoped to a step's transitive needs — siblings never leak in by completion
order. Saving the canvas creates a new immutable version; rollback is
re-saving an old definition. Never make an imperative SDK canonical.

### Triggers

- **Webhook** — unauthenticated `POST /hooks/{ws-slug}/{path}` (path unique
  per workspace), optional HMAC-SHA256 over the raw body
  (`X-Oarlock-Signature`), optional `X-Idempotency-Key` dedup.
- **Schedule** — a scheduler goroutine sweeps cron triggers every 30s with a
  90s lookback; a per-occurrence idempotency key (`cron:<trigger>:<unix>`)
  makes multiple replicas converge to one run with zero locks.
- **Manual** — API/UI run, always allowed. The workflow `is_enabled` toggle
  gates triggers and programmatic (MCP) starts only.

### Secrets and redaction

Secrets are AES-256-GCM encrypted (`internal/vault`, master key from
`OARLOCK_MASTER_KEY`, dev fallback with a UI warning). At execution the
engine decrypts workspace secrets, binds them as `secrets.*`, and builds a
redactor that scrubs the values from **everything persisted or emitted**:
task input/output/error JSON, every `task_logs` row, and the stdout tee.
Secrets can be rotated in place while referenced; deleting (or renaming) a
secret or MCP server that a current workflow version references 409s with the
referencing workflow names.

### Logging

Every task gets a DB-backed log trail (engine lifecycle lines + whatever
executors write to `TaskInput.Log`) via a per-task slog handler writing
capped lines (8KB/line, ~1MB/task) to the partitioned `task_logs` table,
teeing redacted records to the process log.

### Tenancy and cells

Every API request resolves a workspace from the session (password login,
sha256-hashed session tokens; see §7) or an `oak_` bearer token. All queries
filter by `workspace_id`. Programmatic access (`/mcp` and the REST API)
authenticates via `oak_` workspace API tokens (sha256-hashed) — the token
*is* the workspace credential, scoped to member tier.

Cell-ready foundations laid now (cheap to keep, brutal to retrofit): a
`cells` table + `cell_id` on workspaces (one row, `cell-0`); routing-by-token
(webhooks/MCP already carry the workspace in URL/token); the data line —
control plane owns users/sessions/directory/billing, cells own everything
with a `workspace_id`; UUIDs everywhere so tenants are movable. **No second
cell until someone pays for it.**

### Packaging and deployment

One image (`ghcr.io/rustyguts/oarlock`, published by CI on main): the web UI
builds to a static SPA and is embedded in the Go binary, which serves it
same-origin next to the API. `OARLOCK_MODE` selects the role — `all` (UI +
API + workers, the single-container shape), `api` (UI + API, inserts jobs
only), `worker` (workers + reaper + scheduler, `/healthz` only) — so the same
image runs as one container or as an HA split with independently scaled API
and worker tiers. Schema + River migrations run under a Postgres advisory
lock, making concurrent replica boot safe. The Helm chart
(`deploy/chart/oarlock`) installs either shape (`mode: simple | scalable`)
with an optional bundled single-node Postgres/Valkey for simple installs.
Postgres is the only hard dependency; without Valkey, live updates degrade to
polling.

### Data model

The embedded migrations in `engine/internal/db/migrations/` are the source of
truth. Core tables: `workspaces`, `users`, `workspace_members`, `workflows`,
`workflow_versions`, `triggers`, `secrets`, `mcp_servers`,
`workspace_api_tokens`, `sessions`, `runs`, `tasks`, `suspensions`, and the
ts-partitioned `task_logs`. Log policy: 8KB/line truncate, ~1MB/task cap;
weekly partitions + retention by `DROP PARTITION` still to come (retention is
a clean pricing lever).

## 4. Hard rules (the foundation contract)

1. Engine state lives in rows; no process owns a run.
2. Job insert and state write share a transaction, always.
3. Every tenant row has `workspace_id`; every query goes through WorkspaceDB;
   RLS stays on. *(Scoping is enforced today; the WorkspaceDB handle + RLS
   backstop are still to be wired — see Remaining work.)*
4. The JSON definition is the only canonical workflow artifact.
5. One executor interface; in-process is the default forever.
6. Secrets only via the vault; encrypted at rest; never in definitions or
   logs (the engine redacts secret values from task records and log lines).
7. No cross-workspace joins, ever.
8. No second cell, no new executor, no sixth integration until demand (or
   revenue) forces it.

## 5. What's built today

**Engine.** advance/execute/resume workers with transactional job+state
writes and guarded status transitions (a concurrent cancel beats an in-flight
result); idempotent advancement; retries as new attempt rows with exponential
backoff; per-step `timeout` (0–600s) and `retries` (0–10); 15m worker-level
task ceiling; orphan reaper; suspensions for long delays and callbacks;
cancel + retry-from-failed; run idempotency keys scoped by workflow.

**Steps.** `http.request` (1MB body cap, JSON auto-parse), `transform` (goja,
5s), `code.js` (hardened goja, 30s, `console.*` → task log), `delay` (>5min
suspends, up to 30 days), `wait.callback` (suspend until external POST),
`ai.prompt` (BYOK Anthropic/OpenAI/OpenRouter, token usage captured in
output), `mcp.tool` (call a tool on a workspace MCP server).

**Expressions.** `{{ }}` interpolation with per-eval time limits and context
cancel; a single-expression value keeps its native type; context scoped to
transitive needs (unit + DB tested); `if` guards evaluated at advance without
secrets.

**Triggers.** Webhook (HMAC, idempotency, unique path per workspace, generic
404 on unknown), cron scheduler (multi-replica-safe via idempotency keys),
trigger CRUD API + editor panel, `is_enabled` gating.

**Secrets.** Typed secrets (`generic` | `api_key`) encrypted with
AES-256-GCM; `{{secrets.<name>}}` usable in any config/script; redaction from
all persisted/emitted output; masked list API; in-place rotation; delete
blocked while referenced (jsonb config scan + word-boundary text scan).

**MCP.** Workspace MCP **server** at `/mcp` (streamable HTTP, `oak_` bearer
tokens, tools: `list_workflows` / `run_workflow` / `get_run_status`, tested
end-to-end with the official SDK client) and MCP **client** (workspace
`mcp_servers` with encrypted auth, live tool discovery, stateless connection
test, delete/rename protection).

**API.** Workflows/versions/triggers/secrets/MCP/tokens CRUD; runs
start/list/detail/cancel/retry; SSE run events (Valkey ping → Postgres
refetch, 250ms coalescing, poll fallback); logs API with keyset pagination +
plaintext download; `/v1/stats` dashboard aggregates; hardening — credentialed
CORS origin allowlist, 1MB body cap, ReadHeader/Idle timeouts, sanitized 5xx,
sha256-hashed session tokens, logout + expired-session cleanup.

**Web.** Dashboard (stat cards, 14-day chart, task donut, top workflows,
recent activity); workflow list with enable toggles and run stats; canvas
editor (drag-and-drop palette, inspector generated from config specs with
dynamic api_key/mcp_server/mcp_tool selects, reference-aware step rename,
per-step timeout/retries/if fields, unsaved-changes guard, run-with-input,
version history + restore, triggers panel); run detail on the **pinned**
version with per-attempt outputs/errors, suspension cards with resume URLs,
live SSE updates, log tail with load-older paging; Configuration (secrets +
dev-key banner), MCP Servers, API Access (tokens + rotate) pages; auth gate
(setup / login / forced password change), admin Users page, and a user menu
(change password / sign out).

**Packaging.** All-in-one Docker image with embedded UI and
`OARLOCK_MODE=all|api|worker`; Docker Compose stack; Helm chart with simple
(all-in-one) and scalable (api + worker) modes, optional bundled
Postgres/Valkey, ingress/TLS, existing-secret support, and a chart check
script run in CI; GHCR publishing workflow (multi-arch amd64/arm64).

**Auth.** Password login (argon2id), first-run setup that claims the admin,
admin-created users with forced first-login password change and last-admin
guards, sessions with the auto-login bootstrap removed, and `oak_` bearer
tokens authenticating the full `/v1` API at member tier with the admin
surface excluded and in-place rotation (project.md §7).

**Tests + CI.** DB-backed engine tests (diamond DAG, failure, retry,
cancel-vs-late-result, advance idempotency, context scoping, reaper,
if-guards, suspensions, scheduler, MCP end-to-end); unit tests
(interpolation, redaction, HMAC, cron, tokens, `flow.ts` round-trip);
Playwright visual regression (fully mocked API, frozen clock, darwin
baselines, local-only); GitHub Actions (Go build/vet/gofmt/test with a
Postgres service, `-p 1`; web svelte-check/build/vitest).

## 6. Remaining work

### Security (before any non-localhost, multi-tenant exposure)

- RLS is still off (hard rule 3 wants it on); no WorkspaceDB handle +
  `SET LOCAL app.workspace_id`.
- No SSRF guard on `http.request` / MCP URLs (fine for self-host, blocking
  for cloud).
- Per-workspace concurrency cap (the fairness knob and pricing lever) not
  implemented.
- Built-in auth shipped (§7): password login, admin-created users, full-API
  bearer tokens. SSRF guard on `http.request`/MCP URLs is still the remaining
  blocker before a network-exposed (non-tailnet) deployment.

### Phase 2 — multi-tenant cloud alpha (exit: 10–20 external workspaces, zero isolation incidents)

- `ai.classify` (structured output).
- Integrations v1 — exactly five: Slack, email, Google Sheets, GitHub,
  webhook-out. Resist the catalog; MCP is the long-tail.
- Production: DOKS + managed PG via Helm/ArgoCD, OTel → Grafana, GlitchTip,
  restore-drilled backups, Stripe stub + plan-limit middleware.
- Alpha polish: signup, docs with 5 recipes, ~10 templates, usage metering,
  rate limits.
- True envelope encryption (per-row data keys); `http.request` secret-based
  auth.
- Record the Claude-runs-your-workflow MCP demo (the launch hook).

### Phase 3 — first paying customers (exit: 5–10 paying workspaces, MRR > infra floor)

- OIDC SSO on paid plans (SAML only when a deal demands it); RBAC enforced;
  scoped API tokens.
- Append-only `audit_events`; retention tiers + export; status/security pages.
- Stripe live; fair use = soft-warn-then-throttle, **never silent halt**.
- Tier-2 isolation (dedicated queue/workers) as a Business upsell.
- Demand-driven only: subprocess/WASM code sandbox, first dedicated cell
  (ApplicationSet from a directory row).

### Product / engine backlog (tracked, not blocking)

- `foreach` iteration.
- YAML view (CodeMirror) of the definition; import/export; canvas undo/redo.
- Published JSON Schema for the definition (config specs are served via
  `/v1/step-types` for now); YAML↔JSON at the API boundary.
- Log retention: weekly partition maintenance + `DROP PARTITION`.
- goja frozen-context hardening + memory limits.

## 7. Built-in authentication (v1 — shipped)

Simple auth out of the box for self-hosters: first visit creates the admin,
everything requires login, admins manage users and API keys. No SMTP, no
external IdP, no new infrastructure — Postgres and the existing session
mechanism carry all of it. Implemented in `internal/api` (`auth.go`,
`users.go`, `password.go`, `ratelimit.go`; migration 0010) and the web
`AuthGate` / `session.svelte.ts` / `/users` / `/account` surfaces.

### Decisions

- **Identity**: email + password. Hashes are argon2id (`x/crypto/argon2`,
  OWASP parameters: 19 MiB memory, t=2, p=1, PHC-encoded) in a new nullable
  `users.password_hash` — NULL means the account cannot log in.
- **First run = setup**: while no user has a password, authenticated routes
  return 401 with `{"setup_required": true}` and the UI routes to `/setup`.
  `POST /v1/setup` **claims the migration-seeded owner account in place**
  (sets email, name, password) rather than inserting a new user — every
  `created_by` FK and the owner membership survive, and the guard
  (`where password_hash is null` on the seeded row) makes concurrent setup
  attempts race-safe. The first account is therefore the admin by
  construction.
- **Subsequent users are admin-created** (signup stays closed): admins add
  users from a Users page with an initial password and
  `must_change_password=true`; the user is forced through a password change
  on first login. No open registration, no invite-link plumbing, no email
  dependency. Invite links can layer on later.
- **Roles**: reuse the existing `workspace_members` ladder, enforced in v1 as
  two tiers — **admin** (`owner`|`admin`) and **member** (`editor`|`viewer`).
  Admin-only surface: user management and API tokens. Everything else is
  available to any authenticated member; per-route viewer enforcement is
  deferred until it's needed.
- **API keys**: the existing `oak_` workspace tokens authenticate the **whole
  `/v1` API** (plus `/mcp`, as today) via `Authorization: Bearer`. Token
  principals are member-tier and are **hard-excluded from the admin surface**
  (`/v1/users*`, `/v1/api-tokens*`, auth endpoints) — a leaked key can drive
  workflows but can never mint credentials or escalate.
  `POST /v1/api-tokens/{id}/rotate` swaps the hash in place (same id/name)
  and returns the new raw token exactly once, mirroring secret rotation.
- **Sessions**: the existing mechanism stays (sha256-hashed tokens, 30-day
  TTL, HttpOnly + SameSite=Lax cookie, logout, expiry pruning). Login mints a
  fresh token (no fixation); setting or changing a password deletes the
  user's other sessions. The cookie gains `Secure` automatically when the
  request arrived over TLS (`X-Forwarded-Proto`), overridable via
  `OARLOCK_SECURE_COOKIES=auto|always|never`. The **auto-login bootstrap is
  removed**.

### API surface

Unauthenticated: `POST /v1/setup` (first run only), `POST /v1/login`
(uniform "invalid credentials", per-IP+email rate limit — in-memory, so
per-replica in HA; acceptable v1). Authenticated (self): `POST /v1/logout`,
`POST /v1/password` (requires current password unless `must_change_password`),
`GET /v1/me` (gains `must_change_password`). Admin-only: `GET|POST
/v1/users`, `PATCH /v1/users/{id}` (name/role), `DELETE /v1/users/{id}`,
`POST /v1/users/{id}/reset-password` (temp password + forced change), token
CRUD + rotate. Guards: the last admin can't be deleted or demoted. Webhooks,
`/resume/{token}`, `/mcp`, `/healthz`, and the static UI assets keep their
current auth models.

`WithAuth` resolution order: `Bearer oak_…` → token principal (workspace,
member-tier); else session cookie → user principal; else 401 (with
`setup_required` while unconfigured). A `requireAdmin` wrapper protects the
admin routes; token principals always fail it.

### UI

`/setup` and `/login` pages (minimal centered card, house style). The root
layout fetches `/v1/me` and redirects: 401→`/login`, `setup_required`→
`/setup`, `must_change_password`→ change-password screen. Sidebar footer
becomes a user menu (change password, log out). New admin-only **Users**
page (create / role / reset password / delete). The MCP Access page becomes
**API Access**: tokens now cover the full API, with a Rotate action reusing
the shown-once dialog.

### Migration and compatibility

One migration: `users.password_hash text`, `users.must_change_password
boolean not null default false`. Existing installs wake up in setup mode
(the seeded user has no password); their data is untouched and setup adopts
it. DB-backed API tests replace the cookie-bootstrap flow with a
seed-user-and-login helper. The dev split (vite :3001 → API :9000) works
unchanged over credentialed CORS. Once this ships, the houston deployment
can graduate from tailnet-only — after the SSRF guard also lands (§6).

### Out of scope for v1

OIDC/SSO (Phase 3, paid), email flows, invite links, per-key token scopes,
2FA, audit log (Phase 3), multi-workspace membership.

### Status

Shipped (all steps): migration 0010, argon2id, setup/login/logout/password,
`WithAuth` rework + login rate limit (bootstrap removed), users CRUD with
last-admin guards, bearer tokens on `/v1` + rotate; web setup/login/
change-password gate, route guard (`session.svelte.ts`), user menu, Users
page, API Access rotate. Verified with DB-backed engine tests and a live
setup→login→token-run browser pass. Remaining follow-up: the houston
deployment can move off tailnet-only once the SSRF guard (§6) also lands.

## 8. Platform administration (decision, not scheduled work)

**A platform operator is not a workspace role.** The workspace ladder
(`owner > admin > editor > viewer`) is tenant-scoped and stays that way;
platform administration is a control-plane concern. The tenant app/API never
gains cross-workspace powers — hard rule 7 is enforced structurally, not by
discipline.

- **Now (single-user self-host):** nothing to build. The operator
  administrates the host (compose, env, `psql`, backups). No in-app
  superadmin, ever — it would create exactly the cross-tenant code paths the
  architecture forbids.
- **Later (cloud):** the control-plane application is the admin surface —
  separate deployment (or a second listener + router in the same binary on
  internal ingress), separate identity namespace (`platform_operators` or
  OIDC group, never `workspace_members`), separate audit trail. Sharing one
  UI/API between operators and tenants is an anti-pattern: one missing
  workspace check + one admin session = cross-tenant breach.
- **Support access:** never reuse tenant sessions; the control plane mints a
  time-boxed, scoped JWT carrying `workspace_id` + the operator's identity +
  a `support` claim, audited and visible to the customer.

What we keep true today so this stays cheap: workspace roles carry zero
platform semantics; every tenant route resolves its workspace from the
session; the `cells` table stays dormant but present; `audit_events` lands in
Phase 3 before any operator console ships.
