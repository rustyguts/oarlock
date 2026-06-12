# Platform Administration — recommended approach (not yet built)

*How the platform owner/operator is represented, and where platform
administration should live as oarlock grows from single-user self-host to a
multi-customer cloud. This documents a decision so we don't drift into the
wrong shape; nothing here is scheduled work.*

## The principle

**A platform operator is not a workspace role.** The workspace role ladder
(`owner > admin > editor > viewer` in `workspace_members`) is tenant-scoped
and must stay that way. Platform administration is a *control-plane* concern —
the same control plane that design.md §4.5 already assigns ownership of users,
sessions, the workspace directory, billing, and the cell registry. The tenant
app and API should never gain cross-workspace powers, because hard rule 7
(*no cross-workspace joins, ever*) is enforced structurally, not by
discipline: code paths that could read two tenants' data simply don't exist
in the tenant surface.

## Now: single-user self-host

Nothing to build. The self-host operator administrates the *host*, not the
app: compose/env config, migrations, `psql`, backups. The seeded workspace
owner is the only in-app identity. Resist adding an in-app "superadmin" —
a single-user deployment doesn't need it, and it would create the exact
cross-tenant code paths the architecture forbids.

## Later: multiple customers (cloud)

Platform administration becomes the **control-plane application** — and per
§4.5 that component must exist anyway once there is a workspace directory,
billing, and a cell registry to manage. Recommendation:

- **Separate surface, not the tenant UI/API.** Separate deployment (or at
  minimum a separate listener + router in the same binary — see below),
  separate hostname on internal ingress (VPN / IP-allowlist), separate
  session store.
- **Separate identity namespace.** Operators are rows in a
  `platform_operators` table (or an external IdP group via OIDC + MFA),
  *never* rows in `workspace_members`. An operator credential is useless
  against the tenant API; a tenant credential is useless against the admin
  API.
- **Separate audit trail** for every operator action, from day one of the
  console existing.

### Is sharing one UI/API between platform admins and workspace owners unsafe?

It is widely considered an anti-pattern, for reasons that compound:

1. **The tenant API is the most-attacked surface.** If admin powers live
   behind the same routes, one missing workspace check (IDOR-class bug) plus
   one admin session equals a cross-tenant breach. Separation turns this
   class of bug from "incident" into "impossible by construction."
2. **Session and CSRF blast radius.** An operator browsing the tenant UI with
   an elevated cookie is a confused-deputy machine. Separate origins and
   separate cookies remove the overlap.
3. **Exposure control.** The tenant app must be public; the admin plane can
   be VPN-only with MFA. You can't IP-allowlist half an app.
4. **Compliance.** SOC 2 / enterprise security reviews expect a segregated
   administrative plane with its own authn and audit. Retrofitting this is
   far more expensive than starting with it.

### Cost-conscious middle ground (early cloud)

A fully separate service on day one of cloud is not required. Acceptable
interim shape: the **same Go binary** serving the operator API on a *second
listener/port* with its own router and authn middleware, exposed only on
internal ingress, plus a small separate admin frontend. This keeps ops cheap
while preserving every security boundary above. Graduate to a standalone
control-plane service when the first dedicated cell ships (it must exist by
then — it's the thing that provisions cells from directory rows).

### Customer support access (impersonation)

Never reuse tenant sessions. When support needs to see a workspace, the
control plane mints a **time-boxed, scoped JWT** carrying `workspace_id`, the
acting operator's identity, and a `support` claim — exactly the §4.5 routing
token mechanism. The access is audited and visible to the customer (banner in
the workspace + audit event). This falls out of the JWT/cell design for free.

## What we keep true today so this stays cheap

- Workspace roles carry zero platform semantics (true today).
- Every tenant API route resolves its workspace from the session — no
  ambient cross-tenant access (true since the sessions work).
- `cells` table + JWT claims stay dormant but present (§4.5) — the future
  control plane is their issuer.
- `audit_events` lands in Phase 3 as planned; the operator console requires
  it before shipping.
