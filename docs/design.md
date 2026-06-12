# Workflow Platform — Design Document v1.0

*Consolidated from market research + architecture discussion, June 2026. This is the build plan. Principle throughout: keep early objectives simple, defer hard problems, but lay foundations now that the hard features can stand on later.*

---

## 1. What this is and why it can win

A self-hostable + cloud workflow automation platform. The market (n8n, Activepieces, Windmill, Kestra, Temporal, Prefect) gives away features and charges for two things: managed hosting and enterprise governance. The wedge:

- **Flat, predictable pricing** — unlimited executions, free egress (R2), no surprise bills, no halted workflows. The incumbents' loudest complaints are metering shock (Temporal actions, n8n execution caps).
- **SSO + basic RBAC included on paid plans** — incumbents gate these at $150–667/mo.
- **MCP-native** — every workflow is a tool an AI agent can call; the MCP ecosystem is the integration long-tail a solo builder can't hand-write.
- **BYO AI keys** — customers' own Anthropic/OpenAI keys; zero AI COGS, better privacy story.

Cost structure: fixed-then-stepped (rented nodes, free egress) → bill flat tiers gated on **concurrent task slots**, never per-execution. Profitable at ~2–3 customers per node; every additional tenant on a node is margin.

**Tiers (target):** Free = self-host · Hobby ~$9 · Pro ~$29–49 (SSO, more concurrency) · Business ~$99–149 (audit logs, dedicated workers) · Enterprise = dedicated cell, custom.

---

## 2. Stack (decided)

| Layer | Choice | Note |
|---|---|---|
| Core engine | **Go** | Goroutines make blocked I/O nearly free; matches daily tooling |
| Queue | **River** (Postgres-backed) | Transactional enqueue = job + state in one tx; no Redis dual-write |
| Database | **PostgreSQL** | Single source of truth; River lives in it |
| Cache / pub-sub | **Valkey** | UI live-updates + caching only; never load-bearing for correctness |
| Frontend | **SvelteKit** + Tailwind + shadcn-svelte + **@xyflow/svelte** (canvas) + CodeMirror 6 | Svelte Flow is the biggest time-saver in the project |
| Object storage | **R2** (or DO Spaces) | Free egress; coalesce writes (Class A ops cost $4.50/M) |
| Prod (initial) | **DOKS + managed Postgres** | Managed PG = cheap insurance for the only fatal-mistake zone |
| Prod (later) | Hetzner k3s | Migrate compute when infra > ~$300/mo; workers are stateless so it's a weekend |
| Expressions/JS steps | **goja** (embedded JS) | In-process, sandboxed, familiar to n8n/Zapier refugees |
| User Python/containers | deferred | Subprocess/WASM then gVisor k8s Jobs — Phase 3, demand-driven |

---

## 3. Glossary (use these names everywhere)

- **Workspace** — the tenant; every tenant-owned row carries `workspace_id`.
- **Workflow** — a named automation; points at its current Version.
- **Workflow Version** — immutable snapshot of a Definition; runs pin to one.
- **Definition** — the declarative JSON document (canonical artifact). YAML and the canvas are views of it.
- **Step** — a node in the definition (design-time).
- **Task** — one execution attempt of a step in a run (run-time; ≈ one River job; retry = new task, attempt+1).
- **Run** — one execution of a workflow version end-to-end.
- **Trigger** — webhook | schedule | manual (later: event/MCP).
- **Secret** — encrypted workspace value (type `generic` or `api_key`), referenced by name, never inlined; values are redacted from run records and logs. Managed in the Configuration UI. *(Renamed from "Connection" — they were always credentials/keys.)*
- **Suspension** — persisted "waiting" record that frees the worker during long waits.
- **Cell** — an isolated deployment unit (API + workers + Postgres). Day one there is exactly one: `cell-0`.

---

## 4. Architecture

### 4.1 Engine: event-driven state machine (the non-negotiable foundation)

No process owns a run. Truth lives in Postgres rows; workers are stateless muscle. Two job types only:

1. **`advance_run`** — load definition + task states, compute ready steps (all `needs` satisfied), insert tasks + enqueue `execute_task` jobs, all in one transaction. Idempotent (re-derives from rows).
2. **`execute_task`** — run the step, persist output/status, enqueue `advance_run`.

This shape gives parallelism, fan-out/fan-in, retries (River attempts), and crash recovery for free — and it's the architecture you cannot retrofit, so it ships in week one.

**Long waits:** a native step blocked on slow HTTP costs one parked goroutine (~KBs) — already solved by Go, just set step timeouts. Waits beyond the timeout horizon use **suspension**: task writes `status=suspended` + a suspensions row, the job completes (slot freed), resume via River scheduled job (`delay`, polling) or callback URL `/v1/resume/{token}` (webhooks, approvals). Delay step = Phase 1 (trivial). Callback/approval = Phase 2. Temporal-grade timers/signals/replay = never (not needed).

**Fairness:** one knob — per-workspace concurrent-task cap checked at dequeue. This is also what pricing tiers gate.

**Live UI:** workers publish to Valkey pub/sub → SvelteKit SSE. Fire-and-forget; PG remains truth.

### 4.2 Executor abstraction (one interface now, implementations later)

Execution strategy is a property of the *step type*, invisible to the engine:

```go
type Executor interface {
    Execute(ctx context.Context, in TaskInput) (TaskOutput, error) // + log sink
}
```

- Phase 1: **in-process** only (native steps + goja). Density = margin.
- Phase 2–3: subprocess/WASM sandbox for heavier code steps; **k8s Job + gVisor** for BYO containers — metered (container-minutes), because it's the only executor with real marginal cost. The executor boundary and the billing boundary are the same line.

Defining this interface in week one is a one-file decision; skipping it is a month-six refactor of every step type.

### 4.3 Definition format (code + UI from one artifact)

Canonical: versioned **JSON document** in `workflow_versions.definition` (jsonb). YAML accepted at API/CLI boundary; canvas edits the same document; future TS SDK *generates* it. Never make an imperative SDK canonical (that's Temporal replay-land). `needs` edges define the DAG; `if` guards skip; `foreach` step for iteration; `{{ }}` goja expressions against a frozen context. Publish the JSON Schema day one — it drives validation, editor autocomplete, and canvas property panels from one source.

### 4.4 Tenancy: three layers shared, isolation as a paid ladder

All tiers: (1) repository-layer scoping — every query through a `WorkspaceDB` handle, (2) Postgres RLS backstop via `SET LOCAL app.workspace_id`, (3) object keys prefixed `ws/{workspace_id}/`. Isolation ladder: shared pool + caps (default) → dedicated River queue + worker Deployment (Business; a Helm values change) → dedicated cell (Enterprise).

### 4.5 Cell-ready foundations (lay now, use later)

The hybrid/cell architecture (control plane + per-customer data planes) costs almost nothing to keep open and is brutal to retrofit. Three rules, effective immediately:

1. **Tenant directory:** a `cells` table + `cell_id` on workspaces. One row (`cell-0`) until an enterprise customer pays.
2. **Routing by JWT:** control plane mints short-lived tokens carrying `workspace_id` + cell claims; cell APIs validate locally. Webhooks/MCP already carry workspace in the URL → edge resolves cell from a cached directory.
3. **The data line:** control plane owns users, sessions, workspace directory, billing, cell registry, templates. Cells own everything with a `workspace_id`. No query ever joins across workspaces. UUIDs everywhere (already true) so tenants are movable.

Engine doesn't change at all under cells — River/suspensions/cron are cell-local by construction. **Hard rule: no second cell until someone pays for it.** Cells are cattle (ArgoCD ApplicationSet generates them from directory rows), never snowflakes.

---

## 5. Schema (first migration)

```sql
-- control-plane-ish (lives with cell-0 for now; split is logical, not physical)
create table cells (
  id text primary key,                    -- 'cell-0'
  region text not null, api_url text not null,
  engine_version text, status text not null default 'active'
);
create table workspaces (
  id uuid primary key default gen_random_uuid(),
  slug text not null unique, name text not null,
  plan text not null default 'free',
  cell_id text not null default 'cell-0' references cells(id),
  settings jsonb not null default '{}',
  created_at timestamptz not null default now()
);
create table users (
  id uuid primary key default gen_random_uuid(),
  email citext not null unique, name text,
  created_at timestamptz not null default now()
);
create table workspace_members (
  workspace_id uuid not null references workspaces(id) on delete cascade,
  user_id uuid not null references users(id) on delete cascade,
  role text not null default 'editor',    -- owner|admin|editor|viewer
  created_at timestamptz not null default now(),
  primary key (workspace_id, user_id)
);

-- definitions
create table workflows (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null, slug text not null,
  is_enabled boolean not null default false,
  current_version_id uuid,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (workspace_id, slug)
);
create table workflow_versions (
  id uuid primary key default gen_random_uuid(),
  workflow_id uuid not null references workflows(id) on delete cascade,
  version integer not null,
  definition jsonb not null,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  unique (workflow_id, version)
);
alter table workflows add constraint fk_current_version
  foreign key (current_version_id) references workflow_versions(id);

create table triggers (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  workflow_id uuid not null references workflows(id) on delete cascade,
  type text not null,                     -- webhook|schedule|manual
  config jsonb not null default '{}',
  is_enabled boolean not null default true,
  created_at timestamptz not null default now()
);

-- secrets (encrypted; renamed from `connections` in migration 0005/0006 —
-- now `secrets` with type generic|api_key and nullable provider)
create table connections (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  name text not null, provider text not null,
  encrypted_data bytea not null, key_id text not null,
  created_by uuid references users(id),
  created_at timestamptz not null default now(),
  unique (workspace_id, name)
);

-- execution
create type run_status  as enum ('queued','running','suspended','succeeded','failed','canceled');
create type task_status as enum ('queued','running','suspended','succeeded','failed','skipped','canceled');

create table runs (
  id uuid primary key default gen_random_uuid(),
  workspace_id uuid not null references workspaces(id) on delete cascade,
  workflow_id uuid not null references workflows(id) on delete cascade,
  workflow_version_id uuid not null references workflow_versions(id),
  trigger_id uuid references triggers(id),
  status run_status not null default 'queued',
  input jsonb, input_blob_ref text,       -- inline <32KB, else R2 key
  error jsonb, idempotency_key text,
  created_at timestamptz not null default now(),
  started_at timestamptz, finished_at timestamptz,
  unique (workspace_id, idempotency_key)
);
create index on runs (workspace_id, created_at desc);
create index on runs (workspace_id, status) where status in ('queued','running','suspended');

create table tasks (
  id uuid primary key default gen_random_uuid(),
  run_id uuid not null references runs(id) on delete cascade,
  workspace_id uuid not null,             -- denormalized for RLS + quotas
  step_key text not null, attempt integer not null default 1,
  status task_status not null default 'queued',
  input jsonb, output jsonb, output_blob_ref text,
  error jsonb,
  queued_at timestamptz not null default now(),
  started_at timestamptz, finished_at timestamptz,
  unique (run_id, step_key, attempt)
);
create index on tasks (run_id);

create table suspensions (
  id uuid primary key default gen_random_uuid(),
  task_id uuid not null references tasks(id) on delete cascade,
  workspace_id uuid not null,
  kind text not null,                     -- delay|callback|approval|poll
  resume_token text unique, resume_at timestamptz,
  payload jsonb,
  created_at timestamptz not null default now()
);
create index on suspensions (resume_at) where resume_at is not null;

-- logs: partitioned, capped, disposable
create table task_logs (
  id bigint generated always as identity,
  workspace_id uuid not null, run_id uuid not null, task_id uuid not null,
  ts timestamptz not null default now(),
  level smallint not null default 1, message text not null, fields jsonb
) partition by range (ts);
create index on task_logs using brin (ts);
create index on task_logs (run_id);
```

Log policy (engine-enforced): 8KB/line truncate, ~1MB/task cap, weekly partitions, retention by `DROP PARTITION` (7d free / 30d pro / 90d business — retention is a clean pricing lever). Sanity math: 1M runs/mo ≈ 40GB/mo raw — comfortably Postgres. Escape hatches in order: shorter retention → cold partitions to R2 as Parquet → Timescale. RLS policies on all `workspace_id` tables from migration one.

---

## 6. Build order — first 20 steps (each ends runnable)

**Phase 1 — Engine + single-player (steps 1–13). Exit: you've replaced one of your own real automations and trust it.**

1. Monorepo (`/engine` Go, `/web` SvelteKit, `/deploy`); Docker Compose (PG + Valkey); CI.
2. Migration = schema above; pgx + sqlc; seed dev workspace.
3. Definition format v0: structs + JSON Schema + validator + YAML↔JSON. Golden-file tests.
4. River wiring: worker binary, `control`/`tasks` queues, graceful shutdown.
5. `advance_run`: readiness computation + transactional task insert/enqueue. Unit-test the DAG math (diamond, fan-out, skip propagation).
6. Step executor framework: **the Executor interface**, step-type registry, context assembly, retry mapping.
7. Native steps: `http.request`, `transform` (goja), `delay` (suspension + scheduled resume). **★ Milestone: 3-step workflow runs end-to-end via CLI. Aim everything at reaching this fast.**
8. Expressions: goja with frozen context + time/memory limits; `{{ }}` interpolation.
9. Triggers: webhook endpoint (`/hooks/{ws}/{path}`, HMAC, idempotency) + manual API + cron (River periodic).
10. REST API v0: workflows/versions/triggers CRUD; runs list/detail/cancel/retry-from-failed; single dev token.
11. Logs: capped writer, partition maintenance job, paginated logs API.
12. Web shell: workflow list, run list, run detail (task timeline + live logs via SSE←Valkey).
13. Canvas: Svelte Flow editing the definition; property panels from step JSON Schemas; save = new version; YAML view (CodeMirror) of the same document.

**Phase 2 — Multi-tenant cloud alpha, free users (steps 14–20 + alpha polish). Exit: 10–20 external workspaces with weekly-active runs, zero isolation incidents.**

14. Real auth + tenancy: sessions, workspace membership, WorkspaceDB scoping + RLS on, per-workspace concurrency limiter, **cells table + JWT routing claims** (dormant, single cell).
15. Secrets vault (encryption + redaction); `http.request` secret-based auth.
16. AI steps, BYO keys: `ai.prompt`, `ai.classify` (Anthropic/OpenAI/OpenRouter api_key secrets, structured output).
17. Integrations v1 — exactly five: Slack, email, Google Sheets, GitHub, webhook-out. Resist the catalog; MCP is the long-tail.
18. **MCP server per workspace**: `list_workflows` / `run_workflow` / `get_run_status`. Record the Claude-triggers-your-workflow demo — it's the launch hook.
19. Control flow: `code.js` (hardened goja), `foreach`, `if/branch`.
20. Production: DOKS + managed PG via Helm/ArgoCD; OTel → Grafana stack; GlitchTip; restore-drill-verified backups; Stripe stub + plan-limit middleware.

Plus for alpha: signup, docs with 5 copy-paste recipes, ~10 templates, usage metering (you need the data before pricing confidently), rate limits.

**Phase 3 — First paying business customers. Exit: 5–10 paying workspaces, MRR > infra floor (~customers 2–3), repeatable onboarding.**

- SSO via **OIDC** on paid plans (SAML only when a deal demands it); RBAC enforced everywhere; scoped API tokens.
- Append-only `audit_events` table (UI later); retention tiers + export; status page; security page.
- Stripe live; fair-use = soft-warn-then-throttle, **never silent halt** (make n8n's hard-stop a comparison point).
- Tier-2 isolation (dedicated queue/workers) as Business upsell.
- Demand-driven only: subprocess/WASM code sandbox, `mcp.call_tool` client step, first dedicated cell (provisioned the week an enterprise customer signs — ApplicationSet from a directory row).

---

## 7. Deferred on purpose (and why it's safe)

| Deferred | Safe because |
|---|---|
| Second cell / multi-cell ops | `cells` table + JWT routing + data-line discipline keep it a provisioning task |
| Containerized execution | Executor interface exists from step 6 |
| Callback/approval suspensions | `suspensions` table + resume_token designed in from step 7 |
| Temporal-grade durability (signals/replay) | Suspension + River scheduling covers ~90% of real needs |
| Big integration catalog | MCP client step makes the ecosystem the catalog |
| Hetzner migration | Stateless workers + external PG/R2 = weekend move at >$300/mo |
| Log scale (Parquet/Timescale) | Partitioned PG + retention holds far past first revenue |
| SAML, SCIM, `ai.agent`, BYOC | All have a designed landing spot; build on demand |
| Platform operator console | Control-plane surface per §4.5, never the tenant UI/API — see [platform-administration.md](platform-administration.md) |

## 8. Hard rules (the foundation contract)

1. Engine state lives in rows; no process owns a run.
2. Job insert and state write share a transaction, always.
3. Every tenant row has `workspace_id`; every query goes through WorkspaceDB; RLS stays on.
4. The JSON definition is the only canonical workflow artifact.
5. One executor interface; in-process is the default forever.
6. Secrets only via the vault (`secrets` table); encrypted at rest; never in definitions or logs (the engine redacts secret values from task records and log lines).
7. No cross-workspace joins, ever.
8. No second cell, no new executor, no sixth integration until demand (or revenue) forces it.

*Step 7's milestone is where this stops being a document. Everything before it is plumbing; everything after is iteration.*
