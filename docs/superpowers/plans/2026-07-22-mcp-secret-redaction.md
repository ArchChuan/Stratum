# MCP Credential Redaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent stored MCP credentials from reaching browsers while preserving existing secrets during ordinary configuration edits.

**Architecture:** Keep `domain.ServerConfig` as the complete internal value, introduce an HTTP read DTO that omits protected values, and merge omitted secrets in the application service before persistence. Model the redacted read contract separately in TypeScript so the edit form never receives an existing credential.

**Tech Stack:** Go 1.22, Gin, React 18, TypeScript, Ant Design, Vitest, Playwright

---

## Task 1: Safe HTTP Read Contract

**Files:**

- Create: `api/http/dto/mcp_config.go`
- Create: `api/http/dto/mcp_config_test.go`
- Modify: `api/http/handler/mcp_handler.go`
- Modify: `api/http/handler/mcp_handler_test.go`

- [ ] Write table-driven tests proving auth credentials and sensitive `headers`/`env` entries never serialize, while non-sensitive configuration remains present.
- [ ] Run `go test ./api/http/dto ./api/http/handler -run 'MCP|ServerConfig' -count=1` and confirm the new tests fail because no safe DTO exists.
- [ ] Implement `NewMCPServerConfigResponse`, `MCPAuthConfigResponse`, and a case-insensitive sensitive-key classifier covering authorization, API keys, tokens, secrets, passwords, and credentials.
- [ ] Change `GetServerConfig` to serialize the safe response DTO.
- [ ] Re-run the focused tests and confirm they pass.

## Task 2: Preserve Omitted Secrets During Update

**Files:**

- Modify: `internal/mcp/application/mcp_service.go`
- Create: `internal/mcp/application/mcp_service_secret_test.go`

- [ ] Add a focused `port.ServerManager` fake and tests for preserving an unchanged auth credential, replacing a supplied credential, clearing credentials on auth-type change/removal, and preserving omitted sensitive header/environment entries.
- [ ] Run `go test ./internal/mcp/application -run 'UpdateServer.*Secret' -count=1` and confirm failures show the incoming redacted config currently overwrites stored secrets.
- [ ] Add a backend merge helper called by `UpdateServer` before `manager.UpdateServer`; clone maps/config values so cached stored configuration is never mutated.
- [ ] Re-run the focused application tests and confirm they pass.

## Task 3: Administrator Read Boundary

**Files:**

- Modify: `api/http/handler/mcp_handler.go`
- Modify: `api/http/handler/mcp_handler_test.go`

- [ ] Add a route registration test with real `RequireTenantRole("admin")` middleware proving a member receives 403 and an administrator reaches the config handler.
- [ ] Run the focused test and confirm it fails because the GET config route currently uses only the base middleware.
- [ ] Register `GET /servers/:id/config` through `admin(...)` and update the route comment to match the enforced policy.
- [ ] Re-run handler tests and confirm the permission test passes.

## Task 4: Frontend Redacted Edit Flow

**Files:**

- Modify: `web/src/modules/mcp/model/mcp.ts`
- Modify: `web/src/modules/mcp/api/mcp.api.ts`
- Modify: `web/src/modules/mcp/hooks/useEditMCPPage.ts`
- Modify: `web/src/modules/mcp/components/MCPAuthSection.tsx`
- Create: `web/src/modules/mcp/hooks/useEditMCPPage.test.ts`

- [ ] Add Vitest coverage proving read configuration contains only `credential_configured`, form values keep secret inputs empty, and update payloads omit empty replacement credentials.
- [ ] Run `npm --prefix web test -- src/modules/mcp/hooks/useEditMCPPage.test.ts` and confirm the tests fail against the existing readable-secret model.
- [ ] Introduce `MCPServerConfigResponse`, map it to empty secret inputs, and build update payloads without empty credential properties.
- [ ] Pass edit/create context into `MCPAuthSection`; keep create validation required, but make edit replacement optional and show `已配置，留空则保留原凭据` when applicable.
- [ ] Re-run the focused Vitest test, frontend typecheck, and lint.

## Task 5: Regression and End-to-End Verification

**Files:**

- Modify only if verification exposes a defect in the scoped implementation.
- Create temporarily and remove before completion: `web/e2e/tmp-mcp-secret-redaction.spec.ts` or an equivalent `tmp-` verifier.

- [ ] Run `gofmt` on changed Go files and execute focused Go and frontend tests.
- [ ] Run `go vet ./...`, `go test -short ./...`, `npm --prefix web run lint`, `npm --prefix web run typecheck`, and `npm --prefix web run build`.
- [ ] Run `make risk-guardrails` and the tracked-worktree secret scan.
- [ ] Start required local services, then verify an administrator gets a redacted GET response, a member gets 403, and an edit with no replacement retains the stored credential without printing it.
- [ ] Drive the edit page in Playwright, assert no secret appears in the network response or input value, save without replacement, reload, and confirm `credential_configured` remains true.
- [ ] Stop all processes started for verification and remove temporary scripts/specs.
