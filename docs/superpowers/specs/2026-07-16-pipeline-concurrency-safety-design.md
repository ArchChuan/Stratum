# Pipeline Concurrency Safety Design

## Goal

Prevent concurrent or stale CI/CD runs from changing production state out of order, deploy every
runtime image by immutable digest, serialize startup DDL across Pods, expose shared-cluster bootstrap
failures, and remove the Gitee mirroring integration.

## Scope

This change resolves audit findings `SG-001` through `SG-005` in
`docs/audits/service-governance-2026-07-16-pipeline-concurrency.md`.

Included:

- GitHub Actions deployment ordering and stale-main protection.
- Immutable dependency image sources, registry publication, and Helm digest references.
- PostgreSQL advisory locking around application startup schema provisioning.
- Idempotent, observable metrics-server bootstrap.
- Removal of the Gitee workflow and its documentation entry.
- Regression tests, Helm rendering checks, project checks, and deployment-path validation.

Excluded:

- Replacing GitHub Actions with a GitOps controller.
- Changing production application configuration or `config/prod.yaml`.
- Changing unrelated CI jobs or application behavior.

## Architecture

### Build in parallel, mutate production serially

Application tests and SHA-tagged backend/frontend builds remain outside the production critical
section. A single downstream job owns all mutable production operations: dependency image
publication, namespace and Secret application, metrics-server bootstrap, Helm upgrade, and rollout
verification.

That job uses a repository-wide production concurrency group with `cancel-in-progress: false`. An
in-progress Helm operation is therefore never terminated by a newer run. GitHub Actions may replace
an older pending job with a newer pending job; this is desirable because only the newest queued
candidate needs deployment.

Immediately after entering the critical section, a main-branch run queries GitHub for the current
`main` commit SHA. If it differs from `github.sha`, the job exits successfully before publishing
dependencies or touching Kubernetes. Tag and manually dispatched runs are serialized by the same
group but are not compared with `main`, because they intentionally identify a selected revision.

### Immutable dependency images

Every upstream dependency uses a fixed source version. In particular, MinIO no longer uses
`latest`. The custom PostgreSQL plus zhparser image uses an explicitly versioned destination name.

The production job publishes dependencies while holding the production concurrency slot. After
each push it resolves the destination registry digest and exposes that digest to the Helm command.
Helm values support either the existing `repository:tag` form or a new digest form. Production uses
`repository@sha256:...`; local and development values remain compatible with tags.

Digest resolution is fail-closed: a missing or malformed digest stops deployment. The workflow does
not use a check-then-push decision to establish correctness. Re-publishing an identical version is
allowed, but the digest passed to Helm is always the digest observed after publication in the same
serialized job.

### Database schema advisory lock

`BootstrapTenants` acquires one dedicated PostgreSQL pool connection and obtains a fixed,
application-specific session advisory lock before public schema provisioning, default-tenant
creation, and all tenant schema provisioning. All application instances use the same lock key.

Lock acquisition has a bounded timeout derived from a child context. Timeout or database errors
fail bootstrap rather than allowing unlocked DDL. Unlock runs in a defer on the same connection;
releasing the connection also guarantees PostgreSQL releases a session lock if explicit unlock
fails. Tenant creation outside startup continues to use its existing per-tenant provisioning path,
whose transaction and idempotent DDL remain unchanged.

### Shared Kubernetes bootstrap

Namespace, Secrets, metrics-server, Helm, and rollout verification execute only within the
serialized production job. metrics-server uses a fixed release manifest URL rather than `latest`.
The workflow checks whether `--kubelet-insecure-tls` is already present before applying the patch.
Patch failures are not suppressed; unexpected cluster state fails the deployment visibly.

### Gitee removal

`.github/workflows/mirror.yml` is deleted. The workflow inventory in the CI/CD guide no longer
mentions repository mirroring. No Gitee secret is read, migrated, or printed; unused repository
secrets can be removed later through GitHub settings without being required for code correctness.

## Error Handling

- A stale main SHA exits before any mutable production operation and reports why it skipped.
- Failure to query the current main SHA fails closed instead of assuming the run is current.
- Missing image digests, invalid digest syntax, registry failures, cluster bootstrap failures, Helm
  failures, and rollout failures all fail the workflow.
- Advisory-lock timeout and unlock errors retain the original bootstrap error where one exists and
  are logged without exposing connection data or credentials.
- Diagnostic Kubernetes output remains sanitized and must not dump Secret values.

## Testing

### Static workflow contracts

A shell test validates that:

- the mutable production job has the fixed concurrency group and does not cancel in progress;
- stale-main verification occurs before dependency publication and Kubernetes mutation;
- dependency sources and production Helm arguments contain no `latest` tags;
- digest outputs are required for every production dependency;
- metrics-server is version-pinned and has no `|| true` error suppression;
- the Gitee workflow and Gitee references are absent.

The existing migration-boundary test pattern is reused for a self-contained shell regression test.

### Helm rendering

Helm templates are rendered with test digest values. Assertions verify backend, frontend, and all
dependency images render as `repository@sha256:...`, while ordinary tag-based rendering remains
valid for local development.

### PostgreSQL locking

Unit tests cover lock acquisition timeout/error propagation through an injectable lock boundary.
An integration test starts two bootstrap attempts against the same PostgreSQL database, holds the
first advisory lock, and proves the second cannot enter schema provisioning until the first releases
it. The test also verifies lock release after a provisioning error.

### Required verification

- Workflow contract test and migration guardrails.
- Relevant Go unit and PostgreSQL integration tests.
- `helm lint` and digest/tag rendering assertions.
- `go vet && go test -short ./...`.
- `go test -v -race -timeout 30s ./...` before delivery when the repository suite fits its declared
  timeout; any environment-only failure is reported explicitly.
- No frontend checks are required because no frontend files or behavior change.
- A live production deployment is not triggered by local verification. GitHub Actions concurrency,
  registry digest publication, and cluster mutation are finally proven by the first authorized CD
  run, without printing credentials or raw Secret data.

## Acceptance Criteria

1. At most one job can perform production mutations at a time.
2. A stale main run performs no dependency publication or Kubernetes mutation.
3. An active Helm operation is never cancelled merely because a newer run starts.
4. Production backend, frontend, PostgreSQL, Redis, NATS, etcd, MinIO, and Milvus images are passed
   to Kubernetes by digest.
5. No production dependency source or deployed image uses `latest`.
6. Concurrent application startups serialize the complete schema bootstrap with a bounded
   PostgreSQL advisory lock.
7. metrics-server bootstrap is version-pinned, idempotent, and fail-visible.
8. Gitee workflow and project documentation references are absent.
9. The audit report records the repair and verification status for all five findings.
