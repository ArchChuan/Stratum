# Generated Report Findings Remediation Design

**Date:** 2026-07-20

## Scope

Repair every current, reproducible defect identified as AR-001 through AR-024 in
`docs/audits/service-governance-2026-07-20-generated-reports.md`. Historical findings that no longer reproduce are
excluded. Knowledge and interview reports under `tmp/` are excluded.

## Delivery Strategy

Use five compatibility-aware batches. Each batch must leave the repository in a testable state and receive focused
verification before the next batch begins. Unsafe behavior is removed rather than retained behind compatibility flags.
Compatibility is preserved only where a safe transition exists, such as retaining `/health` while adding distinct
liveness and readiness endpoints.

## Batch 1: Authorization, Credentials, And Transport

- Validate current tenant membership before rotating a refresh token or signing new tenant claims. Membership absence
  fails as unauthorized; dependency failure is surfaced without consuming the refresh token.
- Replace OAuth bearer-token redirects with a short-lived, single-use opaque exchange code. The browser exchanges the
  code through a same-origin POST. Access tokens remain in memory and refresh credentials remain HttpOnly cookies.
- Remove generic request body, response body, raw query, RAG question, and downstream MCP body logging. Access logs keep
  stable structured metadata only.
- Remote demo and production deployment paths require TLS and verified peer identity. SSH host keys and Kubernetes CA
  data are supplied through protected configuration, not bypassed by workflow commands.
- Production PostgreSQL uses certificate-verified TLS. Development may opt into plaintext explicitly.

## Batch 2: Data Correctness

- Tenant creation is an application use case with an explicit provisioning outcome. A tenant cannot become active or
  receive tenant credentials until schema provisioning succeeds; failures are compensated or persisted as failed.
- Tenant schema upgrades never delete unmapped knowledge chunks. Legacy data is validated or quarantined for explicit
  repair.
- Knowledge ingestion propagates parent and leaf PostgreSQL persistence errors, writes failed state with an independent
  bounded cleanup context, and never reports completion after partial persistence.
- Request-time Milvus compatibility checks never drop collections. Incompatible schemas return a stable error and are
  migrated through versioned collections and explicit reindexing.
- Malformed memory outbox entries move atomically into a tenant-local quarantine table. The original row is deleted only
  after quarantine persistence succeeds.
- Refresh-token revoke and replacement insert execute in one PostgreSQL transaction; Redis remains a reconstructable
  cache and its failures are observable.

## Batch 3: Runtime Governance

- `/livez` reports process liveness. `/readyz` evaluates completed bootstrap and mandatory dependencies with bounded
  checks. `/health` remains as a compatibility alias with documented semantics while probes migrate to the new paths.
- MCP health recovery atomically swaps the connected client and closes the displaced client outside the manager lock.
- Auth and agent quotas use an atomic Redis-backed limiter keyed by route and caller identity. Failure semantics and
  `Retry-After` are explicit. Development without Redis retains a deliberately scoped local fallback.
- Helm-managed Secret changes alter the Pod template checksum. Externally managed Secrets require an explicit rollout
  mechanism.

## Batch 4: API And Functional Contracts

- Synchronous Agent execution failures use stable non-2xx HTTP mapping while preserving the frozen `{"error":"..."}`
  error envelope. Tool approval remains `202 Accepted`.
- Tenant role access uses one shared context accessor; owner self-deletion is covered at router level.
- GitHub configuration gates only GitHub OAuth routes. JWT, refresh, guest, tenant, and admin routes register according to
  their own dependencies.

## Batch 5: Supply Chain And Quality Gates

- Upgrade vulnerable frontend dependencies deliberately. Apply compatible lockfile updates first; major Vite/Vitest
  changes receive focused configuration, test, and build verification.
- Pin the CI security scanner and fail on the accepted severity/confidence threshold. Enforce the repository coverage
  floor as a command failure rather than a warning.
- Fix the local secret scanner to exclude its output and untracked local secrets, then verify consecutive scans do not
  amplify previous findings. Because the current cron source exists only under ignored `tmp/`, establish a tracked source
  of truth before treating this repair as deployable.

## Error And Compatibility Rules

- Authorization ambiguity fails closed.
- Dependency outages return a dependency/service error, not authorization success or false business success.
- Destructive migration is never executed on request or startup paths.
- Existing public response envelopes remain stable. Intentional status-code changes receive contract tests.
- Optional dependencies may degrade only when the affected feature becomes explicitly unavailable.

## Test And Evidence Strategy

Every defect follows red-green TDD using an existing test in the same package as the style template. Focused unit and
integration tests run after each repair. Each batch completes the real paths required by `stratum-e2e-development`,
including API, PostgreSQL tenant schema, NATS outbox, Milvus, MCP, Helm rendering, and browser OAuth behavior where
applicable. Final verification runs the repository Go, frontend, architecture, migration, deployment safety, dependency,
and secret checks. Missing external infrastructure is reported as an unclosed verification risk rather than silently
treated as passing.
