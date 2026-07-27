# Tenant Readiness And Cleanup Guard Design

## Context

On 2026-07-28 a remote E2E fixture treated the tenant ID returned by `POST /auth/guest` as an isolated tenant. The endpoint actually joins the shared default tenant. Its cleanup dropped that tenant schema and deleted the public tenant row. PostgreSQL remained reachable, so `/readyz` incorrectly continued returning 200.

The runtime evidence is authoritative: the deployment provisioned tenant `248dfa9a-8dcc-411b-be7a-e7007b9bebba`, five guest requests succeeded, then tenant tables disappeared and guest onboarding failed while readiness remained green. Obsidian's verified data-lifecycle guidance agrees that destructive cleanup must cover ownership and test-fixture boundaries, but repository behavior defines this fix.

## Goals

1. Return 503 from `/readyz` when the active default tenant is missing or its required tenant schema is absent.
2. Give real platform-assistant E2E tests one canonical guest cleanup operation that only deletes the generated user and verifies that its tenant is the default tenant.
3. Reject any reusable E2E tenant-schema cleanup request targeting the default tenant.
4. Keep readiness read-only. It must expose damage, never recreate or mutate tenant state.

## Non-Goals

- Recovering data deleted during the incident. No pre-incident dump, snapshot, or WAL archive exists.
- Replacing full tenant-schema compatibility checks with readiness.
- Adding automatic destructive repair or broad cleanup.
- Changing guest onboarding from the shared default tenant in this change.

## Backend Design

Add `postgres.CheckDefaultTenantReadiness(ctx, pool) error`. One schema-qualified query finds the active default tenant and verifies that its `tenant_<uuid>` schema contains the seeded `agents` table. No row means the default tenant is missing; a false table predicate means the schema is missing. Database errors propagate with operation context.

`cmd/server.withPostgresReadiness` will accept a bounded readiness function rather than only a `Ping` interface. `Run` supplies a closure that first pings PostgreSQL and then calls the tenant invariant check. The existing router remains responsible for converting any component error to the frozen `{"status":"not_ready"}` 503 response.

## E2E Cleanup Design

Extend `web/e2e/support/real-platform-assistant.ts` with two explicit operations:

- `cleanupPlatformAssistantSession(session)` queries the session tenant's `is_default` flag, rejects missing or non-default tenancy, and deletes only `public.users.id = session.userId`. Foreign-key cascades remove that guest's membership and refresh tokens; it never drops a schema or deletes a tenant.
- `requireDisposableTenant(tenantID)` rejects `is_default=true` before any future tenant-level cleanup. It is a guard primitive, not permission to delete by itself.

The helper tests use a narrow exported decision function so default, non-default, missing, and malformed results can be tested without a live database. Real browser specs call guest cleanup in `finally` blocks.

## Error And Security Behavior

- Readiness errors contain only invariant names in server-side results; the public endpoint keeps its generic body.
- SQL identifiers remain derived only from validated UUIDs.
- Cleanup is fail-closed on query errors, missing tenants, unexpected tenant type, or zero/multiple deleted users.
- No credential, token, cookie, provider key, or raw tenant settings are logged.

## Verification

- Unit tests prove missing default tenant and missing schema fail readiness while a healthy default tenant passes.
- Runtime composition tests prove PostgreSQL ping and tenant invariant failures both propagate.
- Vitest proves default tenants cannot be classified as disposable and guest cleanup requires the default-tenant contract.
- Existing platform-assistant browser E2E continues to pass against its isolated database.
- Remote verification temporarily removes no data: observe `/readyz`, query the restored invariant, and run guest creation/cleanup through the canonical helper.

## Incident Boundary

The original tenant UUID, default row, two non-guest memberships, and empty tenant schema were reconstructed. Historical tenant resources and encrypted provider settings are unrecoverable from current storage. This fix prevents recurrence and exposes future invariant loss; it does not claim data recovery.
