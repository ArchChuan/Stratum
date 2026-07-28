# System Stateful IAM and OAuth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete every IAM manifest capability through credential-safe, source-bound, local headless Chromium acceptance.

**Architecture:** The canonical runner owns a loopback GitHub-compatible OAuth process and injects guarded endpoint overrides into the real backend. Browser journeys use product UI for all primary operations and reconcile HTTP plus PostgreSQL evidence before capability completion.

**Tech Stack:** Go 1.25, Gin, React 18, Ant Design 5, TypeScript, Playwright, Vitest, PostgreSQL, Bash.

---

## Task 1: Guard injectable GitHub OAuth endpoints

**Files:**

- Modify: `config/config.go`
- Modify: `config/config_test.go`
- Modify: `api/wiring/platform.go`
- Modify: `api/http/handler/auth_oauth_handler.go`
- Test: `api/http/handler/auth_oauth_handler_test.go`

- [ ] **Step 1: Write failing configuration tests** that assert official defaults, reject overrides without `STRATUM_E2E_MODE=true`, reject non-loopback overrides, and accept a complete loopback endpoint set in E2E mode.
- [ ] **Step 2: Run `go test ./config -run GitHubOAuth -count=1`** and confirm failure because endpoint fields and validation do not exist.
- [ ] **Step 3: Add `GitHubAuthorizeURL`, `GitHubTokenURL`, and `GitHubUserURL` to `Config`**, preserve official defaults, parse URLs, require one complete override set, and allow it only in explicit E2E mode on loopback HTTP hosts.
- [ ] **Step 4: Run the focused config tests** and confirm they pass.
- [ ] **Step 5: Write a failing OAuth handler test** asserting that login redirects to the injected authorize endpoint while preserving callback and state parameters.
- [ ] **Step 6: Run `go test ./api/http/handler -run GitHubLogin -count=1`** and confirm the handler still redirects to the hardcoded GitHub URL.
- [ ] **Step 7: Inject the authorize URL through handler dependencies and wiring**, and pass configured token/user URLs to `NewGitHubClient`.
- [ ] **Step 8: Run `go test ./config ./api/http/handler ./api/wiring -count=1`** and confirm success.
- [ ] **Step 9: Commit with `[feat](iam): guard oauth endpoint overrides`**.

## Task 2: Add the loopback GitHub provider

**Files:**

- Create: `cmd/e2e-github-oauth/main.go`
- Create: `cmd/e2e-github-oauth/main_test.go`
- Modify: `scripts/e2e/system-stateful.sh`
- Modify: `scripts/e2e/system-stateful-test.sh`

- [ ] **Step 1: Write failing protocol tests** covering state-preserving authorize redirect, single-use code exchange, bearer-protected profile/email endpoints, invalid client rejection, and sanitized failures.
- [ ] **Step 2: Run `go test ./cmd/e2e-github-oauth -count=1`** and confirm failure because the command does not exist.
- [ ] **Step 3: Implement the minimal in-memory provider** with generated codes/tokens, deterministic identity selection, bounded HTTP server timeouts, loopback bind validation, readiness output, and signal-based shutdown.
- [ ] **Step 4: Run the provider tests** and confirm all protocol cases pass.
- [ ] **Step 5: Add failing runner contract assertions** for provider startup before backend, endpoint environment injection, health polling, exit detection, and trap cleanup.
- [ ] **Step 6: Run `bash scripts/e2e/system-stateful-test.sh`** and confirm the new assertions fail.
- [ ] **Step 7: Extend the runner lifecycle** with a provider PID, loopback URLs, explicit E2E mode, health polling, sanitized provider log, and cleanup.
- [ ] **Step 8: Re-run the runner contract and focused Go tests** and confirm success.
- [ ] **Step 9: Commit with `[test](e2e): add local github oauth provider`**.

## Task 3: Add system-admin tenant creation UI

**Files:**

- Modify: `web/src/modules/iam/api/tenant.api.ts`
- Modify: `web/src/modules/iam/api/tenant.api.test.ts`
- Modify: `web/src/modules/iam/pages/admin/TenantsListPage.tsx`
- Modify: `web/src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx`

- [ ] **Step 1: Write failing API and page tests** asserting `POST /admin/tenants`, the visible `创建租户` command, required name/slug validation, plan/status submission, loading state, short success notification, persistent failure notification, modal reset, and list refresh.
- [ ] **Step 2: Run the two focused Vitest files** and confirm failures are caused by the absent API method and modal.
- [ ] **Step 3: Add the typed `createTenant` API method** through the shared client and implement the Ant Design modal with name, slug, plan, and status fields.
- [ ] **Step 4: Re-run focused tests**, then run `npm --prefix web run lint -- --quiet` and confirm success.
- [ ] **Step 5: Commit with `[feat](iam): add admin tenant creation ui`**.

## Task 4: Execute OAuth registration and returning login

**Files:**

- Modify: `web/e2e/stateful/core/actors.ts`
- Modify: `web/e2e/stateful/core/database.ts`
- Modify: `web/e2e/stateful/packs/iam.ts`
- Test: `web/e2e/stateful/core/actors.test.ts`
- Test: `web/e2e/stateful/core/database.test.ts`

- [ ] **Step 1: Write failing helper tests** for disposable OAuth actor metadata, exact registered-user/tenant cleanup, and preservation of execution plus cleanup failures.
- [ ] **Step 2: Run the stateful Vitest configuration** and confirm the helper tests fail for missing behavior.
- [ ] **Step 3: Implement only the required actor metadata and exact cleanup helpers**, keeping all credentials in memory.
- [ ] **Step 4: Re-run stateful unit tests** and confirm success.
- [ ] **Step 5: Extend the IAM pack** to open `/auth/github`, observe callback exchange, complete onboarding registration, reconcile verified identity/tenant/membership, then repeat OAuth entry and verify returning login.
- [ ] **Step 6: Run `STATEFUL_E2E_PACKS=iam make e2e-system-short`** and iterate on evidence-backed product or fixture failures until OAuth callback, exchange, and register capabilities pass.
- [ ] **Step 7: Commit with `[test](e2e): cover oauth registration journey`**.

## Task 5: Execute the remaining IAM mutations

**Files:**

- Modify: `web/e2e/stateful/packs/iam.ts`
- Modify: `web/e2e/stateful/core/database.ts`
- Test: `web/e2e/stateful/core/database.test.ts`

- [ ] **Step 1: Add failing cleanup/reconciliation tests** for exact disposable admin-created and self-deleted tenant records.
- [ ] **Step 2: Run stateful Vitest** and confirm expected failures, implement minimal helpers, and re-run to green.
- [ ] **Step 3: Drive system-admin tenant creation through the new modal**, observe `POST /admin/tenants`, reconcile the row and database, then remove it through the existing UI.
- [ ] **Step 4: Configure embed model through tenant settings**, observe `PATCH /tenant/embed-model`, refresh, reconcile durable settings, and assert the set-once UI is locked.
- [ ] **Step 5: Create a disposable tenant through onboarding and self-delete it through settings**, observe `DELETE /tenant`, reconcile routing and database removal.
- [ ] **Step 6: Logout a disposable authenticated actor through the user menu**, observe `POST /auth/logout`, assert `/login`, and confirm refresh cannot restore the revoked session.
- [ ] **Step 7: Run `STATEFUL_E2E_PACKS=iam make e2e-system-short`** until all IAM capabilities, cleanup, and evidence reconciliation pass without skips.
- [ ] **Step 8: Commit with `[test](e2e): complete iam stateful acceptance`**.

## Task 6: Close IAM and proceed to system acceptance

**Files:**

- Modify: `test/e2e/attestations/*`
- Modify: `.agents/skills/stratum-e2e-development/SKILL.md` only if the versioned workflow contract requires correction discovered by runtime evidence

- [ ] **Step 1: Run `STATEFUL_E2E_PACKS=dashboard,iam make e2e-system-short`** and confirm both packs pass with exact cleanup.
- [ ] **Step 2: Run `make e2e-attestation-check`** and confirm the committed source digest and capability manifest match.
- [ ] **Step 3: Run focused Go tests, stateful Vitest, IAM frontend tests, `make fe-lint`, and `make fe-build`** with clean output.
- [ ] **Step 4: Commit IAM-safe attestation and documentation changes** with `[test](e2e): attest iam browser acceptance`.
- [ ] **Step 5: Continue with the separate workflow/agent/skill/MCP and knowledge/memory/evaluation pack plans**, then run the all-pack short and 3600-second soak before branch completion.
