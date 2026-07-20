# Generated Report Findings Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repair every current defect AR-001 through AR-024 without destructive migration or unsafe compatibility modes.

**Architecture:** Deliver five compatibility-aware batches behind existing domain ports and application services. Security
checks fail closed, data transitions are atomic or explicitly staged, runtime governance is shared across replicas, and
public HTTP envelopes remain stable. Each task starts with a reproducing test and ends with focused verification.

**Tech Stack:** Go 1.25, Gin, pgx v5, Redis v9, NATS JetStream, Milvus SDK, React 18, Vite, Vitest, Helm, GitHub Actions.

---

## File Map

- IAM session and OAuth: `api/http/handler/auth_*`, `internal/iam/application`, `internal/iam/infrastructure/persistence`.
- HTTP governance: `api/middleware`, `api/http/router.go`, `api/http/handler/agent_exec_handler.go`.
- Knowledge and vectors: `internal/knowledge/application`, `pkg/storage/postgres/tenant_schema.sql`,
  `pkg/storage/milvus`.
- Memory messaging: `internal/memory/infrastructure/pipeline`, tenant DDL baselines.
- MCP lifecycle: `internal/mcp/infrastructure`.
- Deployment and CI: `helm`, `k8s`, `.github/workflows`, `scripts/quality`.
- Frontend auth and dependencies: `web/src/modules/iam`, `web/package.json`, `web/package-lock.json`.

### Task 1: Fail-Closed Refresh And Atomic Rotation (AR-001, AR-017)

**Files:**

- Modify: `api/http/handler/auth_session_handler.go`
- Modify: `internal/iam/infrastructure/persistence/token_store.go`
- Test: `api/http/handler/auth_handler_test.go`
- Test: `internal/iam/infrastructure/persistence/token_store_test.go`

- [ ] **Step 1: Add failing refresh tests**

Add table cases proving missing membership and membership lookup failure return non-2xx and never call `Rotate`, while a
valid role rotates exactly once.

- [ ] **Step 2: Run focused tests and confirm RED**

Run: `go test ./api/http/handler -run 'TestRefresh.*Membership' -count=1`

Expected: current fallback-to-member behavior fails the new assertions.

- [ ] **Step 3: Validate membership before rotation**

Move `GetTenantRole` before `TokenStore.Rotate`. Require a non-empty role and map lookup failure to an authorization or
dependency error without consuming the old token.

- [ ] **Step 4: Add atomic rotation repository test**

Use pgxmock expectations for `Begin`, old-token `UPDATE ... RETURNING`, new-token `INSERT`, and `Commit`; inject insert
failure and require `Rollback`.

- [ ] **Step 5: Implement transactional rotation**

Execute revoke and replacement insert on one `pgx.Tx`. Update Redis only after commit and log/return cache errors according
to the documented best-effort policy.

- [ ] **Step 6: Verify GREEN**

Run: `go test ./api/http/handler ./internal/iam/infrastructure/persistence -run 'Refresh|Rotate' -count=1`

### Task 2: Replace OAuth Token Redirects (AR-003)

**Files:**

- Create: `internal/iam/domain/port/oauth_exchange_store.go`
- Create: `internal/iam/infrastructure/persistence/oauth_exchange_store.go`
- Modify: `api/http/handler/auth_handler.go`
- Modify: `api/http/handler/auth_oauth_handler.go`
- Modify: `api/http/router.go`
- Modify: `web/src/modules/iam/api/auth.api.ts`
- Modify: `web/src/modules/iam/pages/auth/CallbackPage.tsx`
- Modify: `web/src/modules/iam/pages/auth/OnboardingPage.tsx`
- Test: `api/http/handler/auth_handler_test.go`
- Test: `web/src/modules/iam/pages/auth/CallbackPage.test.tsx`

- [ ] **Step 1: Add redirect and exchange contract tests**

Assert callback redirects contain only `code`, never `access_token` or `onboarding_token`; assert a POST exchange consumes
the code once, rejects replay and expiry, and returns the existing access/onboarding response shape.

- [ ] **Step 2: Confirm RED**

Run: `go test ./api/http/handler -run 'OAuth.*Exchange|GitHubCallback' -count=1`

- [ ] **Step 3: Implement opaque exchange storage**

Define a minimal consumer-side port with `Create(ctx, payload, ttl)` and `Consume(ctx, code)`. Store only a SHA-256 code
hash and encrypted or server-side payload, deleting atomically on consume.

- [ ] **Step 4: Wire POST `/auth/oauth/exchange`**

The callback stores the pending result then redirects with `?code=`. The exchange endpoint returns access token or sets an
HttpOnly onboarding cookie/session; it never accepts bearer credentials in the URL.

- [ ] **Step 5: Update frontend callback flow**

Read `code`, immediately replace the URL, POST exchange through the shared Axios instance, keep access token in the auth
context, and remove onboarding token use from `sessionStorage`.

- [ ] **Step 6: Verify backend and frontend GREEN**

Run: `go test ./api/http/handler -run 'OAuth|GitHubCallback' -count=1`

Run: `npm --prefix web test -- CallbackPage.test.tsx --run`

### Task 3: Remove Sensitive Logging And Raw MCP Errors (AR-004, AR-012)

**Files:**

- Modify: `api/middleware/trace.go`
- Modify: `api/middleware/trace_test.go`
- Modify: `internal/knowledge/application/rag_service.go`
- Modify: `internal/knowledge/application/rag_service_test.go`
- Modify: `internal/mcp/infrastructure/client.go`
- Modify: `internal/mcp/infrastructure/client_test.go`

- [ ] **Step 1: Add sentinel-leak tests**

Inject a unique secret into request/response bodies, raw query, RAG question and MCP error body. Capture zap logs and
errors; assert the sentinel never appears and body/query fields are absent.

- [ ] **Step 2: Confirm RED**

Run: `go test ./api/middleware ./internal/knowledge/application ./internal/mcp/infrastructure -run 'Sensitive|RawBody|QuestionLog' -count=1`

- [ ] **Step 3: Remove global body/query capture**

Delete `bodyWriter`, request body reads and raw query logging. Keep method, path, status, latency, byte count, client and
trace identity fields.

- [ ] **Step 4: Replace content logs**

RAG logs record question length, mode, workspace and trace only. MCP non-200 errors contain status and stable server/transport
classification only.

- [ ] **Step 5: Verify GREEN**

Run the Step 2 command and require exit 0.

### Task 4: Make Tenant Creation And Legacy DDL Non-Destructive (AR-006, AR-007)

**Files:**

- Modify: `internal/iam/application/onboard_service.go`
- Modify: `internal/iam/domain/port/onboard_repo.go`
- Modify: `internal/iam/infrastructure/persistence/onboard_repo.go`
- Modify: `api/http/handler/auth_register_handler.go`
- Modify: `api/http/handler/auth_tenant_handler.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `internal/migration/sql/tenant_schema.sql`
- Test: `api/http/handler/auth_handler_test.go`
- Test: `pkg/storage/postgres/tenant_schema_integration_test.go`

- [ ] **Step 1: Add provisioning failure tests for both create paths**

Inject a failing provisioner and assert no 201 or tenant-scoped token is returned. Assert compensation/failed state occurs.

- [ ] **Step 2: Add historical schema fixture**

Create a legacy chunk whose workspace name cannot map. Provision the schema and assert the chunk remains available in a
quarantine state/table rather than being deleted.

- [ ] **Step 3: Confirm RED**

Run: `go test ./api/http/handler -run 'Create.*Provision' -count=1`

Run: `go test ./pkg/storage/postgres -run 'LegacyKnowledgeChunk' -count=1`

- [ ] **Step 4: Move provisioning into an application use case**

Create tenant in non-active provisioning state, provision schema, then activate and issue credentials. On failure persist
failed state or compensate through the repository port.

- [ ] **Step 5: Replace destructive DDL**

Remove `DELETE ... workspace_id IS NULL`. Quarantine unmapped rows with safe idempotent DDL before applying constraints;
keep runtime queries from treating quarantined rows as valid workspace chunks.

- [ ] **Step 6: Verify GREEN and migration parity**

Run focused tests plus `/bin/bash scripts/quality/check-migration-boundaries.sh`.

### Task 5: Make Knowledge Ingestion And Milvus Compatibility Safe (AR-008, AR-009)

**Files:**

- Modify: `internal/knowledge/application/ingest_service.go`
- Test: `internal/knowledge/application/ingest_service_test.go`
- Modify: `pkg/storage/milvus/client.go`
- Test: `pkg/storage/milvus/client_test.go`

- [ ] **Step 1: Add PG failure and timeout terminal-state tests**

Parent and leaf repository failures must bubble to the job result and call `MarkIngestFailed`. A timed-out context must use
a fresh bounded cleanup context for the terminal write.

- [ ] **Step 2: Add no-drop compatibility tests**

Mock an existing wrong-dimension/missing-field collection and assert `DropCollection` is never called; require a stable
incompatible-schema error.

- [ ] **Step 3: Confirm RED**

Run: `go test ./internal/knowledge/application ./pkg/storage/milvus -run 'PersistFailure|TimeoutFailure|IncompatibleCollection' -count=1`

- [ ] **Step 4: Propagate persistence failures**

Return parent/leaf insert errors. Use `context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)` for terminal state.
Do not increment completed metrics if completion state write fails.

- [ ] **Step 5: Remove request-time DropCollection**

Return a domain/infrastructure incompatibility error that directs operators to explicit reindex. Keep compatible index
creation idempotent.

- [ ] **Step 6: Verify GREEN**

Run the Step 3 command and require exit 0.

### Task 6: Quarantine Poison Outbox And Close Replaced MCP Clients (AR-010, AR-013)

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `internal/migration/sql/tenant_schema.sql`
- Modify: `internal/memory/infrastructure/pipeline/outbox_poller.go`
- Test: `internal/memory/infrastructure/pipeline/outbox_poller_test.go`
- Modify: `internal/mcp/infrastructure/client_manager.go`
- Test: `internal/mcp/infrastructure/client_manager_test.go`

- [ ] **Step 1: Add poison-message transaction tests**

Malformed payload writes safe metadata to `memory_outbox_quarantine` then deletes the original in the same transaction.
If quarantine insert fails, original deletion must not execute.

- [ ] **Step 2: Add repeated reconnect lifecycle test**

Run two unhealthy cycles and assert every displaced fake client receives exactly one `Disconnect`; the active fresh client
remains registered.

- [ ] **Step 3: Confirm RED**

Run: `go test ./internal/memory/infrastructure/pipeline ./internal/mcp/infrastructure -run 'MalformedOutbox|ReconnectCloses' -count=1`

- [ ] **Step 4: Implement quarantine and atomic swap**

Persist outbox ID, tenant/schema, payload hash, error class and timestamps only. For MCP, swap under lock and disconnect the
old client after unlocking, handling stop/reconnect races.

- [ ] **Step 5: Verify GREEN and race behavior**

Run focused tests with `-race` and require exit 0.

### Task 7: Add Real Readiness And Shared Rate Limiting (AR-002, AR-011, AR-014)

**Files:**

- Modify: `api/middleware/require_active_tenant.go`
- Test: `api/middleware/require_active_tenant_test.go`
- Modify: `api/middleware/rate_limit.go`
- Test: `api/middleware/rate_limit_test.go`
- Modify: `api/http/router.go`
- Modify: `api/wiring/container.go`
- Modify: `internal/platform/harness/harness.go`
- Modify: `helm/templates/deployment.yaml`
- Modify: `k8s/deployment.yaml`
- Test: `api/http/contract_test.go`

- [ ] **Step 1: Add fail-closed tenant status cases**

Active passes, inactive/not-found returns 403, DB failure returns 503, missing tenant context is rejected on protected paths,
and explicit nil-DB development mode remains supported.

- [ ] **Step 2: Add readiness contract tests**

`/livez` remains 200 for dependency failure. `/readyz` returns 503 when bootstrap or mandatory component health fails and
200 when all mandatory checks pass. `/health` keeps its compatibility response.

- [ ] **Step 3: Add two-instance limiter test**

Instantiate two middleware stores backed by one Redis test backend and prove they consume one shared quota with route and
identity keys; assert `Retry-After` on rejection.

- [ ] **Step 4: Confirm RED**

Run focused middleware/router tests.

- [ ] **Step 5: Implement bounded readiness and Redis limiter**

Expose Harness health through Container without importing infrastructure into handlers. Use atomic Redis operations with
an explicit local fallback only when Redis is intentionally disabled, not on Redis errors.

- [ ] **Step 6: Migrate probes and verify GREEN**

Point startup/readiness to `/readyz` and liveness to `/livez`. Run focused tests and Helm render assertions.

### Task 8: Correct API Registration And Error Contracts (AR-015, AR-016, AR-018)

**Files:**

- Modify: `api/http/handler/agent_exec_handler.go`
- Modify: `api/middleware/error_mapping.go`
- Modify: `api/http/handler/tenant_handler.go`
- Modify: `api/middleware/jwt.go`
- Modify: `api/http/router.go`
- Test: `api/http/handler/agent_handler_test.go`
- Test: `api/http/handler/tenant_handler_test.go`
- Test: `api/http/router_auth_test.go`
- Modify: `api/http/testdata/contracts/*.golden.json`

- [ ] **Step 1: Add failing contract tests**

Provider/tool/timeout errors return stable non-2xx `{"error":"..."}`; owner self-delete reaches the service; JWT/guest/
refresh/tenant routes register without GitHub config while GitHub routes do not.

- [ ] **Step 2: Confirm RED**

Run focused handler/router contract tests.

- [ ] **Step 3: Route errors through ErrorHandler**

Define/map domain sentinels for dependency timeout/unavailable and invalid execution. Preserve `202` approval behavior.

- [ ] **Step 4: Centralize role context access and split route gates**

Use shared constants/accessors for `auth.role`. Register base auth/tenant routes when JWT dependencies exist; register only
GitHub endpoints when the GitHub client is configured.

- [ ] **Step 5: Regenerate intended golden contracts and verify GREEN**

Run focused tests and inspect every golden diff for intentional status-only changes.

### Task 9: Harden Helm, Deployment, CI, And Demo (AR-005, AR-019, AR-020, AR-021, AR-022)

**Files:**

- Modify: `helm/values.yaml`
- Modify: `helm/values-prod.yaml`
- Modify: `helm/values-demo.yaml`
- Modify: `helm/values-demo-local.yaml`
- Modify: `helm/templates/deployment.yaml`
- Modify: `.github/workflows/deploy.yml`
- Modify: `.github/workflows/ci.yml`
- Modify: `scripts/quality/check-deployment-safety.sh`
- Test: `scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 1: Add failing deployment safety cases**

Reject remote HTTP callback/frontend URLs, production `sslmode=disable`, `StrictHostKeyChecking=no`,
`insecure-skip-tls-verify`, unpinned gosec, suppressed gosec exit, and advisory-only coverage.

- [ ] **Step 2: Confirm RED**

Run: `/bin/bash scripts/quality/check-deployment-safety-test.sh`

- [ ] **Step 3: Implement secure configuration**

Production DB TLS defaults to `verify-full` with CA secret values. Demo remote values use HTTPS and secure cookies. Deploy
uses protected known_hosts/kube CA secrets. Add Secret checksum for chart-managed secrets.

- [ ] **Step 4: Enforce CI gates**

Pin a reviewed gosec version, remove `|| true`, keep SARIF upload under `if: always()`, and make coverage below 80 exit 1.

- [ ] **Step 5: Verify GREEN**

Run deployment safety tests, `helm lint helm`, and production/demo `helm template` checks.

### Task 10: Remove Frontend Advisories And Fix Secret Scan Source (AR-023, AR-024)

**Files:**

- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Create: `scripts/quality/secret-scan.sh`
- Create: `scripts/quality/secret-scan-test.sh`
- Modify: `tmp/cron/secret-scan.sh` only as a deployed local copy after tracked source passes

- [ ] **Step 1: Capture current advisory baseline**

Run `npm --prefix web audit --json` and retain only advisory IDs/counts in test evidence, never credential data.

- [ ] **Step 2: Upgrade compatible dependencies**

Update React Router, form-data and Babel transitive locks first. Upgrade Vite/Vitest to patched compatible releases; if a
major is required, update config using upstream migration behavior and keep the change isolated.

- [ ] **Step 3: Verify frontend**

Run audit, lint, typecheck, bounded Vitest, and production build. Require zero unaccepted audit findings.

- [ ] **Step 4: Add tracked scanner and regression test**

The tracked scanner excludes `tmp/`, `.env`, its output directory and other untracked local secret stores while scanning
tracked working-tree content. A two-run test proves the second report does not ingest the first.

- [ ] **Step 5: Deploy local cron copy and verify**

Update the ignored local cron script from the tracked source, run twice with a temporary output directory, and confirm no
self-amplification and no raw secret output.

### Task 11: Full Verification And Audit Closeout

**Files:**

- Modify: `docs/audits/service-governance-2026-07-20-generated-reports.md`

- [ ] **Step 1: Run architecture, migration, Go and frontend checks**

Run serialized project verification (`stratum-verify go-full`, `stratum-verify frontend-full`), architecture and deployment
safety scripts, Helm lint/render and fresh dependency scans.

- [ ] **Step 2: Run required E2E paths**

Use real API/database paths for refresh, tenant provisioning and owner deletion; NATS/PG for poison outbox; Milvus for
non-destructive incompatibility; MCP for reconnect cleanup; browser DOM/network assertions for OAuth exchange at desktop
and mobile viewports.

- [ ] **Step 3: Clean temporary state**

Delete temporary E2E scripts through patch operations, stop self-started services, and remove only this run's test data.

- [ ] **Step 4: Update audit statuses**

Append repair commit/test/E2E evidence per AR item. Mark any infrastructure-dependent item unverified rather than closed.

- [ ] **Step 5: Fresh completion verification**

Run `git diff --check`, inspect `git status`, and rerun the commands that prove every final claim immediately before
closeout.
