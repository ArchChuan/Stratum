# Pipeline Concurrency Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serialize production mutations, reject stale deployments, deploy immutable image digests, lock startup DDL, expose cluster bootstrap failures, and remove Gitee mirroring.

**Architecture:** Keep tests and SHA application-image builds parallel, then put dependency publication and every Kubernetes mutation in one non-cancelling production concurrency job. Add digest-aware Helm image rendering and a bounded PostgreSQL session advisory lock around the complete startup schema bootstrap.

**Tech Stack:** GitHub Actions YAML, Bash, Helm templates, Go 1.25, pgx v5, PostgreSQL advisory locks.

---

## Task 1: Add failing delivery-contract tests

**Files:**

- Create: `scripts/quality/check-deployment-safety-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write the failing shell test**

Create assertions that inspect `.github/workflows/deploy.yml`, Helm templates, and repository files.
The test must require a `stratum-production` concurrency group with
`cancel-in-progress: false`, a stale-main gate before the dependency step, fixed dependency versions,
digest Helm arguments, a fixed metrics-server version without `|| true`, and absence of
`.github/workflows/mirror.yml` and case-insensitive Gitee references in tracked workflow/deployment
documentation.

- [ ] **Step 2: Run the test and verify RED**

Run: `bash scripts/quality/check-deployment-safety-test.sh`

Expected: FAIL because the deployment job has no concurrency/stale gate, MinIO and metrics-server
use `latest`, Helm has no digest support, and the Gitee workflow exists.

- [ ] **Step 3: Add the test to the quality target**

Add a `deployment-safety-test` Make target and include it in the relevant local quality aggregate,
following the existing migration guardrail target style.

## Task 2: Render all Helm images by digest when configured

**Files:**

- Modify: `helm/templates/_helpers.tpl`
- Modify: `helm/templates/deployment.yaml`
- Modify: `helm/templates/frontend-deployment.yaml`
- Modify: `helm/templates/dependencies.yaml`
- Modify: `helm/values.yaml`
- Modify: `helm/values-demo.yaml`
- Create: `scripts/quality/check-helm-image-rendering-test.sh`

- [ ] **Step 1: Write the failing Helm rendering test**

Render the chart once with ordinary tags and once with distinct valid-looking `sha256:` digests for
`app`, `frontend`, `database`, `redis`, `nats`, `etcd`, `minio`, and `milvus`. Assert tag rendering
remains present and digest rendering produces `repository@sha256:...` for every configured image.

- [ ] **Step 2: Run the rendering test and verify RED**

Run: `bash scripts/quality/check-helm-image-rendering-test.sh`

Expected: FAIL because the chart ignores digest values.

- [ ] **Step 3: Implement one image-reference helper**

Add a Helm helper accepting an image map. If `.digest` is non-empty, render
`<repository>@<digest>`; otherwise render `<repository>:<tag>`. Replace all eight production image
expressions with the helper and add empty `digest` defaults to base/demo values.

- [ ] **Step 4: Verify GREEN**

Run: `bash scripts/quality/check-helm-image-rendering-test.sh && helm lint ./helm -f helm/values-demo.yaml`

Expected: PASS.

## Task 3: Serialize and harden the production deployment job

**Files:**

- Modify: `.github/workflows/deploy.yml`
- Modify: `helm/values-demo.yaml`

- [ ] **Step 1: Split immutable build from production mutation**

Keep application build/push in `build-and-push`. Move dependency publication into `deploy`, and add
job-level concurrency group `stratum-production` with `cancel-in-progress: false` so the slot covers
dependencies, shared cluster resources, Helm, and rollout verification.

- [ ] **Step 2: Add the fail-closed stale-main gate**

For `refs/heads/main`, call the GitHub commits API using the workflow token and compare the returned
SHA with `github.sha`. Write `current=true|false` to a step output. Gate every later deploy step on
`current == 'true'`; API or parsing failures fail the job. Tags and manual dispatches set
`current=true`.

- [ ] **Step 3: Publish pinned dependency images and capture digests**

Use fixed upstream versions, including a fixed MinIO release and versioned zhparser image tag.
Publish while holding the concurrency slot. Resolve each destination with
`docker buildx imagetools inspect` and require a `sha256:<64 hex>` digest. Store only digests in step
outputs; never print credentials.

- [ ] **Step 4: Deploy every image by digest**

Resolve backend/frontend destination digests after login and pass all eight digest values through
`--set-string ...image.digest=...`. Keep repository values separate and do not pass production tags
to Helm.

- [ ] **Step 5: Make metrics-server fixed and fail-visible**

Use a fixed metrics-server release manifest. Inspect container args before patching; patch only when
the insecure TLS flag is absent. Remove error suppression so apply, inspect, or patch failures stop
deployment.

- [ ] **Step 6: Verify the delivery contract GREEN**

Run: `bash scripts/quality/check-deployment-safety-test.sh`

Expected: PASS.

## Task 4: Serialize startup schema provisioning with PostgreSQL

**Files:**

- Modify: `pkg/storage/postgres/tenant.go`
- Modify: `pkg/storage/postgres/pool_test.go`
- Modify: `pkg/tenantdb/schema.go`
- Modify: `internal/platform/runtime/runtime.go`
- Modify: `internal/platform/runtime/runtime_test.go`

- [ ] **Step 1: Write failing lock lifecycle tests**

Add tests around a small connection interface proving lock SQL executes before the callback, unlock
executes afterward, callback errors are preserved, and unlock errors are joined rather than hiding
the callback error. Add a runtime test proving `BootstrapTenants` invokes the lock boundary once.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test ./pkg/storage/postgres ./cmd/server -run 'SchemaProvisionLock|BootstrapTenantSchemas' -count=1`

Expected: FAIL because the lock boundary does not exist.

- [ ] **Step 3: Implement the bounded transaction advisory lock**

Add `WithSchemaProvisionLock(ctx, pool, fn)`. Begin an explicit transaction and execute
`SELECT pg_advisory_xact_lock($1)` with a fixed Stratum lock key. Commit after the callback;
callback, lock, or commit failures roll back on a short background timeout. This supersedes the
original session-lock implementation, which can leak locks through PgBouncer transaction pooling.
Re-export it from `pkg/tenantdb`.

- [ ] **Step 4: Wrap the complete bootstrap**

In `BootstrapTenants`, create a bounded child context and execute public schema provisioning,
default-tenant creation, and all-tenant provisioning inside `WithSchemaProvisionLock`. Preserve the
existing warning behavior for individual tenant provisioning while failing public/default setup.

- [ ] **Step 5: Verify GREEN and race behavior**

Run:
`go test ./pkg/storage/postgres ./cmd/server -run 'SchemaProvisionLock|BootstrapTenantSchemas' -count=1`

Then run:
`go test -race ./pkg/storage/postgres ./cmd/server -count=1`

Expected: PASS.

## Task 5: Remove Gitee and update delivery documentation

**Files:**

- Delete: `.github/workflows/mirror.yml`
- Modify: `docs/deployment/CI_CD_GUIDE.md`
- Modify: `docs/audits/service-governance-2026-07-16-pipeline-concurrency.md`

- [ ] **Step 1: Delete the mirror workflow and documentation inventory row**

Remove the workflow file and remove repository-mirroring references from the CI/CD guide. Do not
touch GitHub repository secrets because that is external state and code no longer reads them.

- [ ] **Step 2: Update the audit report**

Append repair status and verification evidence for `SG-001` through `SG-005`, preserving the
original findings and distinguishing local/static verification from the first real CD run.

- [ ] **Step 3: Verify removal**

Run:
`test ! -e .github/workflows/mirror.yml && ! git grep -in gitee -- .github docs/deployment`

Expected: PASS with no output.

## Task 6: Full verification and closeout

**Files:**

- Modify only files required to fix verification defects introduced by Tasks 1-5.

- [ ] **Step 1: Run format and static configuration checks**

Run:
`gofmt -w pkg/storage/postgres/tenant.go pkg/storage/postgres/pool_test.go pkg/tenantdb/schema.go internal/platform/runtime/runtime.go internal/platform/runtime/runtime_test.go`

Run:
`bash scripts/quality/check-deployment-safety-test.sh && bash scripts/quality/check-helm-image-rendering-test.sh && helm lint ./helm -f helm/values-demo.yaml && npx markdownlint-cli2 docs/deployment/CI_CD_GUIDE.md docs/audits/service-governance-2026-07-16-pipeline-concurrency.md`

Expected: PASS.

- [ ] **Step 2: Run required Go checks**

Run: `go vet ./... && go test -short ./...`

Expected: PASS.

- [ ] **Step 3: Run PR-level race tests**

Run: `go test -v -race -timeout 30s ./...`

Expected: PASS, or report exact environment/package timeout evidence without claiming it passed.

- [ ] **Step 4: Validate the final diff**

Run: `git diff --check && git status --short && git diff --stat`

Expected: no whitespace errors; only task-related files changed plus the pre-existing audit report.
