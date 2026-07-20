# IAM Token and Runtime Boundaries Implementation Plan

**Goal:** Enforce the approved IAM crypto and executable composition boundaries, then verify them through real PostgreSQL/API paths.

**Architecture:** IAM application and HTTP consumers use an IAM-owned token port implemented by `internal/iam/infrastructure/token`. Process orchestration lives in `cmd/server`; reusable platform packages do not import API packages.

**Tech Stack:** Go 1.22+, golang-jwt v5, pgx v5, Docker Compose, go-arch-lint.

## Task 1: Guard and move IAM token implementation

- [ ] Add a failing architecture test forbidding RSA and `golang-jwt` imports in IAM application.
- [ ] Define token claims and `TokenService` in `internal/iam/domain/port`.
- [ ] Move RS256 implementation and behavior tests to `internal/iam/infrastructure/token`.
- [ ] Convert handlers, middleware, router, and wiring to the port without changing claims or HTTP behavior.
- [ ] Run focused IAM and API tests.

## Task 2: Guard and move runtime composition

- [ ] Add a failing architecture assertion that `internal/platform` imports neither `api/http` nor `api/wiring`.
- [ ] Move tracing, tenant bootstrap, Harness registration, HTTP lifecycle, and signal handling to focused `cmd/server` files.
- [ ] Move runtime tests to `cmd/server` and preserve component/bootstrap behavior.
- [ ] Remove `internal/platform/runtime` and its architecture-lint component/permissions.
- [ ] Run focused server tests and architecture guard.

## Task 3: Exercise the real PostgreSQL and HTTP paths

- [ ] Inspect Compose configuration and start only required dependencies.
- [ ] Provision public schema and tenant schemas twice to prove idempotency.
- [ ] Verify valid tenant write/read isolation and invalid tenant rejection through production executors/repositories.
- [ ] Start the backend, verify `/health`, and exercise an authenticated API path when prerequisites are available.
- [ ] Record database persistence evidence and clean exact test data plus self-started processes/containers.

## Task 4: Complete repository verification

- [ ] Run focused tests, `stratum-verify go-test`, vet, architecture guard, and `git diff --check`.
- [ ] Confirm the Windows proxy port, then run `stratum-verify go-full` with one-shot `HTTP_PROXY`/`HTTPS_PROXY` values.
- [ ] Obtain an independent read-only review and resolve all Critical/Important findings.
