# Unified Model Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove tenant-settings model configuration and make ModelRegistry-backed model management the only source for runtime resolution and every model selector.

**Architecture:** Tenant settings will reject and filter the legacy `llm_api_keys` field, while a public migration removes stored copies. A shared authenticated `/models` catalogue, backed by provider-aware ModelRegistry eligibility, supplies chat and embedding choices to Agent, system-assistant, and Knowledge UI.

**Tech Stack:** Go 1.25, PostgreSQL/pgx, Gin, React 18, TypeScript, Ant Design, Vitest, stateful Playwright E2E

**Design spec:** `docs/superpowers/specs/2026-07-29-unified-model-configuration-design.md`

---

## Task 1: Remove legacy tenant credential behavior

**Files:**

- Modify: `internal/iam/application/tenant_service_test.go`
- Modify: `internal/iam/application/tenant_service.go`
- Modify: `api/http/handler/tenant_handler_test.go`
- Modify: `web/src/modules/iam/pages/tenant/SettingsPage.tsx`
- Modify: `web/src/modules/iam/hooks/useTenantSettings.ts`
- Modify: `web/src/modules/iam/api/tenant.api.ts`
- Modify: `web/src/modules/iam/model/auth.ts`
- Delete: `web/src/modules/iam/components/TenantApiKeyCard.tsx`

- [ ] Write failing service and UI tests proving `llm_api_keys` is absent from reads and rejected on writes while tenant name updates still work.
- [ ] Run targeted Go and Vitest tests; confirm failures reference the still-present legacy behavior/UI.
- [ ] Remove encryption, masking, cache invalidation, hook state and API schema support; return `ErrInvalidSettings` for the reserved key.
- [ ] Run the targeted tests and confirm they pass.

## Task 2: Delete stored legacy credentials

**Files:**

- Create: `pkg/migration/sql/027_remove_tenant_llm_api_keys.up.sql`
- Create: `pkg/migration/sql/027_remove_tenant_llm_api_keys.down.sql`
- Modify: `pkg/migration/migration_test.go`

- [ ] Write a failing migration test with settings containing `llm_api_keys` plus an unrelated key.
- [ ] Verify it fails because migration 027 is absent.
- [ ] Add an idempotent public-schema update that removes only `llm_api_keys`; document the intentionally irreversible down migration.
- [ ] Verify historical schema order, idempotency, and preservation of unrelated settings.

## Task 3: Make ModelRegistry catalogue match runtime eligibility

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_registry_test.go`
- Modify: `internal/llmgateway/infrastructure/model_registry.go`
- Modify: `api/wiring/tenant_resolver_test.go`
- Modify: `api/wiring/tenant_resolver.go`
- Modify: `api/wiring/system_assistant.go`

- [ ] Write failing tests for disabled provider, missing provider, unsupported chat/embed protocol, and registry-backed diagnostics.
- [ ] Run targeted tests and confirm the unavailable models are currently listed or diagnostics still query tenant settings.
- [ ] Centralize eligible model listing/resolution in ModelRegistry and replace settings-based diagnostics.
- [ ] Run targeted tests and confirm catalogue, validation, diagnostics and runtime resolution agree.

## Task 4: Use one frontend catalogue for every selector

**Files:**

- Modify: `web/src/modules/llm/api/llm.api.ts`
- Create: `web/src/modules/llm/hooks/useModelCatalogue.ts`
- Modify: `web/src/modules/agent/components/AgentFormSections.test.tsx`
- Modify: `web/src/modules/agent/components/AgentFormSections.tsx`
- Modify: `web/src/modules/agent/hooks/__tests__/useCreateAgentPage.test.tsx`
- Modify: `web/src/modules/agent/hooks/useCreateAgentPage.ts`
- Modify: `web/src/modules/agent/hooks/useEditAgentPage.ts`
- Create: `web/src/modules/knowledge/components/WorkspaceCreateModal.test.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceCreateModal.tsx`
- Modify: `web/src/modules/knowledge/hooks/useKnowledgePage.ts`
- Modify: `web/src/constants/index.ts`

- [ ] Write failing component/hook tests proving chat and embedding options come from `/models`, empty catalogues have no fallback, and unavailable edit values are labelled.
- [ ] Run targeted Vitest tests and confirm hardcoded constants/defaults cause the expected failures.
- [ ] Add the shared catalogue API/hook, pass capability-specific options into forms, and remove `CHAT_MODEL_OPTIONS`/`EMBEDDING_MODEL_OPTIONS`.
- [ ] Run targeted tests, lint and TypeScript build.

## Task 5: Align runtime messages, fixtures and acceptance evidence

**Files:**

- Modify: `internal/knowledge/application/rag_service.go`
- Modify: `web/e2e/stateful/core/database.ts`
- Modify: `e2e/evaluation-evolution/bootstrap.go`
- Modify: `scripts/e2e/platform-assistant-remote-verify.sh`
- Modify: `scripts/quality/check-deployment-safety-test.sh`
- Modify: current architecture/user documentation that claims `llm_api_keys` is active

- [ ] Write or update guard tests so legacy field references in executable code fail, except migration history/design records.
- [ ] Replace fixture setup with provider/model rows and update diagnostics text to direct users to 模型管理.
- [ ] Change remote SQL evidence to count enabled provider/model pairs in tenant schemas.
- [ ] Run targeted E2E/bootstrap compile checks and documentation/reference scans.

## Task 6: Full verification

- [ ] Run `gofmt` and frontend formatting on changed files.
- [ ] Run targeted Go/Vitest tests, then `go vet` and `stratum-verify go-test`.
- [ ] Run `make fe-lint && make fe-build` and `make risk-guardrails`.
- [ ] Run `bash scripts/quality/risk-regression-guard.sh --acceptance <changed-files>` and execute the selected system E2E mode to terminal state.
- [ ] Run `make e2e-attestation-check`, inspect `git diff --check`, and review the final diff for secret leakage and unrelated changes.
