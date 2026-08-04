# Platform MCP Phase 1 Implementation Plan

> **DEPRECATED (2026-08-04):** 本计划方向已被反转 —— platform-mcp 整体废弃，系统助手工具改回进程内直调。计划内容仅作历史参考，不再执行。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the platform assistant's code-injected internal tools with a tenant-provisioned, platform-managed MCP server that uses the same Agent/MCP execution path as ordinary Agents while enforcing service identity, scoped delegation, tenant isolation, and an independent platform-assistant UI tab.

**Architecture:** Add a stateless `cmd/platform-mcp` service that exposes closed MCP tools over streamable HTTP and calls Stratum's internal HTTP API over mTLS. The backend provisions one immutable logical server per tenant, signs short-lived invocation tokens only for the managed assistant/server binding, exchanges them for route-scoped API delegation tokens, and keeps all business authorization in existing handlers/application services. Phase 1 migrates official-doc search, tenant diagnostics, and resource-change proposal creation; later resource domains get separate plans after this control plane passes E2E and soak gates.

**Tech Stack:** Go 1.25.12, Gin, RS256 JWT, PostgreSQL/pgx, existing MCP JSON-RPC client, cert-manager, Kubernetes NetworkPolicy, OpenTelemetry, Prometheus, React 18, TypeScript, Ant Design, Playwright.

---

## Scope decomposition

This plan implements only Phase 1 from the approved design:

- managed MCP identity and tenant provision;
- shared MCP execution path for the platform assistant;
- invocation/delegation tokens and replay prevention;
- internal mTLS listener and workload identity;
- Platform MCP server with the three existing assistant capabilities;
- removal of internal-tool injection after parity verification;
- independent “平台助手” tab using the existing Agent/conversation model;
- deployment, observability, security regression, E2E, soak, and attestation.

Agent/Skill/MCP/Knowledge/Model CRUD is Phase 2. Workflow/Memory CRUD is Phase 3. IAM is excluded.

## File structure

### New backend/control-plane files

- `pkg/platformmcp/contract.go`: immutable platform MCP identity and tool-to-API contracts.
- `pkg/platformmcp/claims.go`: invocation and API-delegation claims without importing `internal/`.
- `internal/iam/domain/port/delegation_token.go`: signing/verifying ports.
- `internal/iam/infrastructure/token/delegation.go`: RS256 issuer/audience/token-use validation.
- `internal/iam/application/mcp_token_exchange.go`: membership, binding, approval, route-scope, and replay orchestration.
- `internal/iam/infrastructure/persistence/mcp_token_replay_repo.go`: tenant-scoped one-time JTI consumption.
- `api/middleware/delegation_jwt.go`: delegated Bearer authentication and route-scope enforcement.
- `api/http/handler/mcp_token_exchange_handler.go`: mTLS-only token exchange endpoint.
- `api/http/handler/platform_assistant_capability_handler.go`: HTTP endpoints for official docs, diagnostics, and proposal creation.
- `api/wiring/platform_mcp.go`: adapters from existing assistant capabilities to the new handlers/token exchange.
- `cmd/platform-mcp/main.go`: independent process entry point.
- `internal/platformmcp/server/server.go`: JSON-RPC/MCP transport and health endpoints.
- `internal/platformmcp/application/tools.go`: closed tool catalogue and dispatch.
- `internal/platformmcp/infrastructure/stratum_client.go`: mTLS token exchange and delegated API calls.
- `internal/platformmcp/infrastructure/tlsreload.go`: atomically reloaded client/server certificates.

### Modified shared/runtime files

- `internal/mcp/domain/mcp.go`: managed identity fields on server config/read model.
- `internal/mcp/application/mcp_service.go`: reject tenant mutation/deletion of platform-managed servers.
- `internal/mcp/infrastructure/client.go`: per-call invocation-token and trace propagation.
- `api/wiring/mcp.go`: invocation-token provider and shared execution path.
- `internal/agent/application/agent_service.go`: resolve platform tools through MCP IDs, then remove internal injection.
- `internal/agent/application/system_assistant_tools.go`: deleted after parity is proven.
- `pkg/storage/postgres/tenant_schema.sql`: managed MCP, binding, replay, and proposal metadata DDL/backfill.
- `api/http/router.go`: internal mTLS routes and delegated middleware composition.
- `api/wiring/wiring.go`: lifecycle construction and reverse-order shutdown.

### Frontend and deployment files

- `web/src/modules/agent/pages/AgentManagementPage.tsx`: two-tab shell.
- `web/src/modules/agent/pages/PlatformAssistantPage.tsx`: fixed-assistant conversation entry.
- `web/src/modules/agent/hooks/usePlatformAssistantPage.ts`: fixed-ID conversation behavior.
- `web/src/modules/agent/routes.tsx`: `/agents/assistant` and management routing.
- `helm/templates/platform-mcp-*.yaml`: Deployment, Service, ServiceAccount, NetworkPolicy, certificates, ServiceMonitor, PrometheusRule.
- `helm/values.yaml`, `helm/values-prod.yaml`: image, replicas, TLS, resources, and autoscaling values.

## Task 1: Verify and freeze the MCP protocol contract

**Files:**

- Create: `docs/evidence/platform-mcp-protocol-2026-07-29.md`
- Modify: `docs/superpowers/specs/2026-07-29-platform-mcp-control-plane-design.md`
- Test: `internal/mcp/infrastructure/integration_test.go`

- [ ] **Step 1: Restore an official-document read path and capture versioned evidence**

Run from the feature worktree:

```bash
curl -fsSL https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization > /tmp/mcp-authorization.html
curl -fsSL https://modelcontextprotocol.io/specification/2025-06-18/server/tools > /tmp/mcp-tools.html
curl -fsSL https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization > /tmp/mcp-authorization-current.html
curl -fsSL https://modelcontextprotocol.io/specification/2025-11-25/server/tools > /tmp/mcp-tools-current.html
```

Expected: both commands exit `0` and both files are non-empty. If the current official specification supersedes `2025-06-18`, capture both versions and record the exact authorization, token-passthrough, transport, and tools differences before continuing.

- [ ] **Step 2: Write the evidence note**

Create `docs/evidence/platform-mcp-protocol-2026-07-29.md` with this structure and replace each quoted claim only with text verified from the fetched official pages:

```markdown
# Platform MCP protocol evidence

- Verified on: 2026-07-29
- Compatibility baseline: 2025-06-18
- Current specification checked: 2025-11-25
- Authorization sources:
  - https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization
  - https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
- Tools sources:
  - https://modelcontextprotocol.io/specification/2025-06-18/server/tools
  - https://modelcontextprotocol.io/specification/2025-11-25/server/tools

## Required contracts

1. HTTP authorization responsibility
2. Token passthrough
3. Audience/resource binding
4. Tool input validation and access control
5. Streamable HTTP session/header behavior used by Stratum

For each heading, quote the verified normative sentence, section anchor, version, and any version difference. Do not proceed with token implementation if either current URL returns 404 or the current version differs from 2025-11-25; update the design's version matrix first.

## Stratum decisions

- MCP configuration is not identity.
- Platform credentials are issued per tool call.
- Business authorization remains in Stratum HTTP handlers/application services.
```

- [ ] **Step 3: Add a protocol compatibility test before server work**

Add to `internal/mcp/infrastructure/integration_test.go`:

```go
func TestStreamableHTTPClientPropagatesMCPAndTraceHeaders(t *testing.T) {
    // Start an httptest server that records Accept, Content-Type,
    // MCP-Protocol-Version, traceparent, and Authorization.
    // Call ListTools and CallTool through BaseClient.
    // Assert JSON-RPC 2.0, tools/list, tools/call, and the verified headers.
}
```

- [ ] **Step 4: Run the focused test and commit**

```bash
go test ./internal/mcp/infrastructure -run TestStreamableHTTPClientPropagatesMCPAndTraceHeaders -count=1
git add docs/evidence docs/superpowers/specs internal/mcp/infrastructure/integration_test.go
git commit -m "[docs](mcp): freeze platform MCP protocol contract"
```

Expected: test passes and every evidence claim includes an official section anchor and version.

## Task 2: Add immutable platform MCP identity and tenant provision

**Files:**

- Create: `pkg/platformmcp/contract.go`
- Modify: `internal/mcp/domain/mcp.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Test: `pkg/storage/postgres/tenant_schema_test.go`
- Test: `pkg/storage/postgres/tenant_schema_integration_test.go`

- [ ] **Step 1: Write failing schema and identity tests**

Add assertions for:

```go
require.Contains(t, sql, "system_key TEXT")
require.Contains(t, sql, "management_mode TEXT NOT NULL DEFAULT 'tenant_managed'")
require.Contains(t, sql, "stratum.platform_mcp")
require.Contains(t, sql, "stratum-platform-assistant")
```

Add an integration test that provisions the same tenant twice and asserts exactly one managed MCP row and the exact platform-assistant tool bindings.

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./pkg/storage/postgres -run 'TestTenantSchema.*PlatformMCP|TestProvisionTenantSchemaPlatformMCP' -count=1
```

Expected: FAIL because managed MCP columns and seed do not exist.

- [ ] **Step 3: Add the immutable contract**

Create `pkg/platformmcp/contract.go`:

```go
package platformmcp

const (
    SystemAssistantKey = "stratum.platform_assistant"
    SystemServerID      = "stratum-platform-mcp"
    SystemServerKey     = "stratum.platform_mcp"
    ManagementPlatform = "platform_managed"
    ManagementTenant   = "tenant_managed"
)

var Phase1ToolNames = []string{
    "stratum_search_official_docs",
    "stratum_diagnose_tenant",
    "stratum_propose_resource_change",
}
```

- [ ] **Step 4: Extend the MCP domain projection**

Add to `domain.ServerConfig` and `domain.Server`:

```go
SystemKey      string `json:"-" yaml:"-"`
ManagementMode string `json:"management_mode" yaml:"management_mode"`
```

Do not accept `SystemKey` from public DTO binding.

- [ ] **Step 5: Add idempotent tenant DDL and seed**

Add columns immediately after `mcp_configs` creation, then insert the managed server and `agent_mcp_tool_links` for every `platformmcp.Phase1ToolNames` equivalent SQL literal. Use `ON CONFLICT DO NOTHING`, validate the managed identity with a `DO $$ ... RAISE EXCEPTION` block, and do not perform network calls during provision.

- [ ] **Step 6: Run schema tests and commit**

```bash
go test ./pkg/storage/postgres -run 'TestTenantSchema.*PlatformMCP|TestProvisionTenantSchemaPlatformMCP' -count=1
git add pkg/platformmcp internal/mcp/domain/mcp.go pkg/storage/postgres
git commit -m "[feat](mcp): provision managed platform MCP identity"
```

## Task 3: Protect managed MCP CRUD and bindings

**Files:**

- Modify: `internal/mcp/domain/errors.go`
- Modify: `internal/mcp/application/mcp_service.go`
- Modify: `api/http/dto/mcp_config.go`
- Modify: `internal/agent/application/agent_service.go`
- Test: `internal/mcp/application/mcp_service_test.go`
- Test: `api/http/handler/mcp_handler_test.go`
- Test: `internal/agent/application/agent_service_test.go`

- [ ] **Step 1: Write failing protection tests**

Cover all of these exact cases:

```text
tenant create payload containing system_key -> 400
update managed server -> ErrPlatformManagedServer
delete managed server -> ErrPlatformManagedServer
disconnect managed server -> ErrPlatformManagedServer
ordinary Agent update containing mcp:stratum-platform-mcp:* -> ErrPlatformMCPBindingForbidden
platform assistant system binding survives model-only settings update
```

- [ ] **Step 2: Add sentinels and guards**

```go
var (
    ErrPlatformManagedServer      = errors.New("platform-managed MCP server cannot be changed")
    ErrPlatformMCPBindingForbidden = errors.New("platform MCP tools may only bind to the platform assistant")
)
```

In MCP service mutation methods, load stored config first and reject `ManagementMode == platformmcp.ManagementPlatform`. In Agent create/update validation, reject platform tool IDs unless the existing persisted Agent has `SystemKey == platformmcp.SystemAssistantKey`; public ordinary Agent requests can never set that key.

- [ ] **Step 3: Run tests and commit**

```bash
go test ./internal/mcp/application ./api/http/handler ./internal/agent/application \
  -run 'PlatformManaged|PlatformMCPBinding' -count=1
git add internal/mcp api/http/dto api/http/handler internal/agent
git commit -m "[fix](mcp): protect managed server and bindings"
```

## Task 4: Implement scoped delegation token services

**Files:**

- Create: `pkg/platformmcp/claims.go`
- Create: `internal/iam/domain/port/delegation_token.go`
- Create: `internal/iam/infrastructure/token/delegation.go`
- Test: `internal/iam/infrastructure/token/delegation_test.go`
- Modify: `pkg/constants/auth.go`

- [ ] **Step 1: Write failing token tests**

Test success plus rejection of wrong algorithm, issuer, audience, token use, expiry, missing tool, missing route, and a delegation token presented to the invocation verifier.

- [ ] **Step 2: Define separate claims**

```go
type InvocationClaims struct {
    TenantID, UserID, AgentID, ServerID string
    ToolName, ExecutionID, ApprovalID   string
    jwt.RegisteredClaims
    TokenUse string `json:"token_use"`
}

type APIDelegationClaims struct {
    TenantID, AgentID, ServerID, ToolName, ExecutionID string
    HTTPMethod, PathTemplate, ResourceID, ApprovalID     string
    Role, TokenUse                                      string
    jwt.RegisteredClaims
}
```

- [ ] **Step 3: Implement strict sign/verify methods**

Use separate methods and constants:

```go
SignInvocation(InvocationClaims, time.Duration) (string, error)
VerifyInvocation(string) (*InvocationClaims, error)
SignAPIDelegation(APIDelegationClaims, time.Duration) (string, error)
VerifyAPIDelegation(string) (*APIDelegationClaims, error)
```

Require exact `iss=stratum-agent-runtime`, `aud=stratum-platform-mcp` for invocation and `iss=stratum-token-exchange`, `aud=stratum-api` for delegation.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./internal/iam/infrastructure/token -run Delegation -count=1
git add pkg/platformmcp pkg/constants/auth.go internal/iam
git commit -m "[feat](iam): add scoped platform MCP delegation tokens"
```

## Task 5: Add one-time token exchange and replay prevention

**Files:**

- Create: `internal/iam/application/mcp_token_exchange.go`
- Create: `internal/iam/infrastructure/persistence/mcp_token_replay_repo.go`
- Create: `api/http/handler/mcp_token_exchange_handler.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Test: `internal/iam/application/mcp_token_exchange_test.go`
- Test: `internal/iam/infrastructure/persistence/mcp_token_replay_repo_integration_test.go`

- [ ] **Step 1: Write failing exchange tests**

Assert that exchange rejects ordinary Agents, tenant-managed servers, missing bindings, downgraded users, wrong tool contracts, stale approvals, expired tokens, and duplicate JTI. Assert the successful token contains the current database role rather than the invocation-token role.

- [ ] **Step 2: Add the replay table**

```sql
CREATE TABLE IF NOT EXISTS mcp_invocation_jtis (
    jti        TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_mcp_invocation_jtis_expiry
    ON mcp_invocation_jtis(expires_at);
```

Repository methods must accept explicit `tenantID` and use `execTenant`.

- [ ] **Step 3: Implement atomic exchange orchestration**

```go
type MCPTokenExchange struct {
    Tokens      port.DelegationTokenService
    Roles       port.TenantRoleResolver
    Bindings    port.PlatformMCPBindingReader
    Approvals   port.PlatformMCPApprovalReader
    Replay      port.InvocationReplayStore
    Contracts   platformmcp.ContractRegistry
}
```

Order operations: verify signature -> validate managed identities -> consume JTI atomically -> reload current role -> validate contract/approval/resource scope -> sign delegation. Any repository error fails closed.

- [ ] **Step 4: Run unit and PostgreSQL tests, then commit**

```bash
go test ./internal/iam/application -run MCPTokenExchange -count=1
go test ./internal/iam/infrastructure/persistence -run MCPTokenReplay -count=1
git add internal/iam api/http/handler/mcp_token_exchange_handler.go pkg/storage/postgres/tenant_schema.sql
git commit -m "[feat](iam): exchange one-time MCP invocation tokens"
```

## Task 6: Add delegated API middleware and internal mTLS listener

**Files:**

- Create: `api/middleware/delegation_jwt.go`
- Create: `api/middleware/mtls_identity.go`
- Create: `api/http/internal_router.go`
- Modify: `cmd/server/main.go`
- Modify: `api/wiring/wiring.go`
- Test: `api/middleware/delegation_jwt_test.go`
- Test: `api/middleware/mtls_identity_test.go`

- [ ] **Step 1: Write failing middleware tests**

Test exact method/path/resource matching, wrong audience, wrong SPIFFE URI, same-CA wrong workload, missing client certificate, and successful injection into `tenantdb.TenantContext`.

- [ ] **Step 2: Implement exact route-scope middleware**

```go
func RequireDelegatedScope(tokens port.DelegationTokenVerifier) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, err := verifyDelegationBearer(c, tokens)
        if err != nil || claims.HTTPMethod != c.Request.Method ||
            claims.PathTemplate != c.FullPath() ||
            (claims.ResourceID != "" && claims.ResourceID != c.Param("id")) {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "delegated scope denied"})
            return
        }
        injectDelegatedClaims(c, claims)
        c.Next()
    }
}
```

- [ ] **Step 3: Add a separate TLS listener**

Start public `:8080` and internal `:8443` servers under the existing harness. Internal TLS must use `tls.RequireAndVerifyClientCert`, the internal CA pool, and exact URI `spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp`. Shutdown remains reverse-order and waits for both servers.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./api/middleware ./api/http -run 'Delegat|MTLS|InternalRouter' -count=1
git add api/middleware api/http/internal_router.go api/wiring/wiring.go cmd/server/main.go
git commit -m "[feat](api): add mTLS delegated internal API"
```

## Task 7: Build the Platform MCP server skeleton

**Files:**

- Create: `cmd/platform-mcp/main.go`
- Create: `internal/platformmcp/server/server.go`
- Create: `internal/platformmcp/application/tools.go`
- Create: `internal/platformmcp/infrastructure/tlsreload.go`
- Test: `internal/platformmcp/server/server_test.go`
- Test: `internal/platformmcp/infrastructure/tlsreload_test.go`

- [ ] **Step 1: Write failing protocol and lifecycle tests**

Assert `initialize`, `tools/list`, `tools/call`, unknown method, invalid arguments, `/healthz`, `/readyz`, graceful shutdown, certificate reload, and no raw arguments in logs.

- [ ] **Step 2: Implement a focused JSON-RPC server**

Use the verified Phase 1 protocol contract and existing MCP request/response shapes. Register only the three phase-one tools. Reject unknown fields before dispatch and return MCP `isError` results without exposing upstream bodies.

```go
type Dispatcher interface {
    ListTools(context.Context) []domain.Tool
    CallTool(context.Context, string, map[string]any) (agentport.MCPToolResult, error)
}
```

- [ ] **Step 3: Wire health, readiness, metrics, and signal shutdown**

`/readyz` requires a loaded certificate, CA, contracts, and successful bounded backend readiness check. Use `observability.NewLogger`, existing OTEL setup, and a distinct `service.name=stratum-platform-mcp`.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./internal/platformmcp/... ./cmd/platform-mcp -count=1
git add cmd/platform-mcp internal/platformmcp
git commit -m "[feat](mcp): add platform MCP server runtime"
```

## Task 8: Implement the mTLS Stratum API client and invocation auth

**Files:**

- Create: `internal/platformmcp/infrastructure/stratum_client.go`
- Modify: `internal/mcp/infrastructure/client.go`
- Modify: `api/wiring/mcp.go`
- Test: `internal/platformmcp/infrastructure/stratum_client_test.go`
- Test: `api/wiring/mcp_agent_adapter_test.go`

- [ ] **Step 1: Write failing propagation tests**

Assert that only a managed platform server call receives a short-lived invocation Bearer; ordinary MCP calls preserve existing static auth behavior. Assert `traceparent`, execution ID, tool name, and approval ID are bound, and copied URLs never trigger platform auth.

- [ ] **Step 2: Add a per-call credential provider**

```go
type InvocationCredentialProvider interface {
    Authorization(ctx context.Context, serverID, toolName string) (string, error)
}
```

`BaseClient.CallTool` requests a credential from the provider only when the persisted config has the immutable platform system identity. Do not store the returned token in `ServerConfig`, headers, client fields, logs, or caches.

- [ ] **Step 3: Implement exchange plus delegated request**

`StratumClient.Call` performs: mTLS POST token exchange -> parse delegation token -> mTLS request to the fixed contract method/path -> safe response projection. Use request-scoped timeouts and close response bodies on every path.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./internal/platformmcp/infrastructure ./internal/mcp/infrastructure ./api/wiring \
  -run 'Invocation|PlatformMCP|StratumClient' -count=1
git add internal/platformmcp/infrastructure internal/mcp/infrastructure/client.go api/wiring/mcp.go
git commit -m "[feat](mcp): authenticate platform tool calls per execution"
```

## Task 9: Expose the three Phase 1 capabilities through HTTP and MCP

**Files:**

- Create: `api/http/handler/platform_assistant_capability_handler.go`
- Modify: `api/wiring/system_assistant.go`
- Modify: `api/wiring/resource_change_proposal.go`
- Modify: `pkg/platformmcp/contract.go`
- Modify: `internal/platformmcp/application/tools.go`
- Test: `api/http/handler/platform_assistant_capability_handler_test.go`
- Test: `internal/platformmcp/application/tools_test.go`

- [ ] **Step 1: Write failing handler and tool tests**

Cover exact closed schemas for:

```text
stratum_search_official_docs -> POST /internal/platform-assistant/docs/search
stratum_diagnose_tenant -> POST /internal/platform-assistant/diagnostics
stratum_propose_resource_change -> POST /internal/platform-assistant/proposals
```

Members may call the first two under existing scope rules; proposal creation requires current admin/owner. Unknown fields return 400. Diagnostic evidence gaps remain gaps.

- [ ] **Step 2: Add immutable contracts**

```go
var Phase1Contracts = []ToolContract{
    {Name: ToolSearchOfficialDocs, Method: http.MethodPost, Path: "/internal/platform-assistant/docs/search", Risk: RiskRead},
    {Name: ToolDiagnoseTenant, Method: http.MethodPost, Path: "/internal/platform-assistant/diagnostics", Risk: RiskRead},
    {Name: ToolProposeResourceChange, Method: http.MethodPost, Path: "/internal/platform-assistant/proposals", Risk: RiskWriteReversible, MinimumRole: "admin"},
}
```

- [ ] **Step 3: Adapt existing services without cross-context imports**

Handlers call consumer-side ports assembled in `api/wiring`; they do not import MCP infrastructure or sibling context application packages. Reuse existing official-doc provider, diagnostic provider, proposal validators, safe projections, and metrics.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./api/http/handler ./api/wiring ./internal/platformmcp/application -run 'PlatformAssistantCapability|Phase1Tool' -count=1
git add api/http/handler api/wiring pkg/platformmcp internal/platformmcp/application
git commit -m "[feat](agent): expose platform assistant capabilities through MCP"
```

## Task 10: Move the platform assistant onto the shared MCP resolver

**Files:**

- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/system_assistant_profile.go`
- Delete: `internal/agent/application/system_assistant_tools.go`
- Delete: `internal/agent/domain/system_assistant_tools.go`
- Modify/Delete: corresponding `system_assistant_tools_test.go` cases
- Test: `internal/agent/application/system_assistant_tools_test.go`
- Test: `internal/agent/application/tool_permission_e2e_test.go`

- [ ] **Step 1: Change tests to require MCP provider tools**

Replace assertions for internal provider types with:

```go
require.Equal(t, domain.ProviderTypeMCP, tool.ProviderType)
require.Equal(t, platformmcp.SystemServerID, tool.ServerID)
require.Contains(t, system.Config().MCPToolIDs, "mcp:stratum-platform-mcp:stratum_diagnose_tenant")
```

Also assert ordinary Agents receive none of these tools even if their request payload includes copied IDs.

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/agent/application -run 'SystemAssistant.*MCP|ToolPermission' -count=1
```

- [ ] **Step 3: Remove the system-assistant branch from tool resolution**

Use `buildExtraToolsChecked` with the persisted managed `MCPToolIDs` for both ordinary and system Agents. Retain system-assistant mode only for immutable profile, memory suppression, role-aware output, and metrics—not tool injection.

- [ ] **Step 4: Delete obsolete internal tool definitions and callbacks**

Search before deletion:

```bash
rg -n "SystemAssistantToolDefinitions|WithOfficialDocsSearchFn|WithDiagnosticFn|withProposalCreateFn" internal api
```

Remove every production reference and convert surviving behavior tests to MCP-result fixtures. Do not leave a fallback flag.

- [ ] **Step 5: Run tests and commit**

```bash
go test ./internal/agent/... ./api/wiring/... -count=1
rg -n "SystemAssistantToolDefinitions|ProviderTypeInternal" internal/agent api/wiring && exit 1 || true
git add internal/agent api/wiring
git commit -m "[refactor](agent): route platform tools through shared MCP"
```

## Task 11: Add the independent platform-assistant frontend tab

**Files:**

- Create: `web/src/modules/agent/pages/AgentManagementPage.tsx`
- Create: `web/src/modules/agent/pages/PlatformAssistantPage.tsx`
- Create: `web/src/modules/agent/hooks/usePlatformAssistantPage.ts`
- Modify: `web/src/modules/agent/pages/AgentsListPage.tsx`
- Modify: `web/src/modules/agent/routes.tsx`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Test: `web/src/modules/agent/pages/__tests__/PlatformAssistantPage.test.tsx`
- Test: `web/src/modules/agent/pages/__tests__/AgentsListPage.test.tsx`

- [ ] **Step 1: Write failing tab and fixed-assistant tests**

Assert:

```text
/agents defaults to the 平台助手 tab
平台助手 tab contains 新建会话 and history but no Agent selector
Agent 列表 tab excludes isSystem Agents
creating a platform conversation calls /agents/stratum-platform-assistant/conversations
streaming calls /agents/stratum-platform-assistant/execute/stream
proposal cards and settings remain visible in the platform tab
```

- [ ] **Step 2: Implement the tab shell**

```tsx
<Tabs
  activeKey={activeTab}
  onChange={handleTabChange}
  items={[
    { key: 'assistant', label: '平台助手', children: <PlatformAssistantPage /> },
    { key: 'list', label: 'Agent 列表', children: <AgentsListPage /> },
  ]}
/>
```

Use route state or nested routes so reload preserves the selected tab. `PlatformAssistantPage` reuses shared chat components but passes the fixed assistant ID and omits Agent selection.

- [ ] **Step 3: Enforce backend list semantics**

Update the ordinary Agent list API/query to exclude `system_key IS NOT NULL`; keep the dedicated system settings/get path for the platform tab. Add API tests for pagination count and search exclusion.

- [ ] **Step 4: Run frontend tests and commit**

```bash
cd web
npm test -- --run src/modules/agent/pages/__tests__/PlatformAssistantPage.test.tsx src/modules/agent/pages/__tests__/AgentsListPage.test.tsx
cd ..
git add web api/http/handler internal/agent
git commit -m "[feat](frontend): separate platform assistant tab"
```

## Task 12: Add Helm deployment, mTLS certificates, and network isolation

**Files:**

- Create: `helm/templates/platform-mcp-deployment.yaml`
- Create: `helm/templates/platform-mcp-service.yaml`
- Create: `helm/templates/platform-mcp-serviceaccount.yaml`
- Create: `helm/templates/platform-mcp-networkpolicy.yaml`
- Create: `helm/templates/internal-certificates.yaml`
- Create: `helm/templates/backend-internal-service.yaml`
- Modify: `helm/templates/deployment.yaml`
- Modify: `helm/values.yaml`
- Modify: `helm/values-prod.yaml`
- Test: `scripts/quality/check-platform-mcp-rendering-test.sh`

- [ ] **Step 1: Write a failing Helm rendering guard**

The script must render production values and assert:

```text
Platform MCP Service is ClusterIP
no Platform MCP Ingress exists
separate ServiceAccounts exist
automountServiceAccountToken is false
internal Services expose 8443 only
Certificate URI SANs match both SPIFFE identities
NetworkPolicy denies MCP access to 5432, 6379, 4222, and 19530
replicaCount is at least 2 in production
```

- [ ] **Step 2: Add values and templates**

Use this values shape:

```yaml
platformMCP:
  enabled: true
  replicaCount: 2
  image:
    repository: ghcr.io/bytebuilderx/stratum-platform-mcp
    tag: latest
  service:
    port: 8443
  tls:
    issuerName: stratum-internal-ca
  resources:
    requests: { cpu: 100m, memory: 128Mi }
    limits: { cpu: 500m, memory: 512Mi }
```

- [ ] **Step 3: Run rendering and safety tests, then commit**

```bash
bash scripts/quality/check-platform-mcp-rendering-test.sh
make deployment-safety-test
git add helm scripts/quality/check-platform-mcp-rendering-test.sh
git commit -m "[feat](deploy): add isolated platform MCP service"
```

## Task 13: Add observability and alerts

**Files:**

- Modify: `pkg/observability/provider.go`
- Modify: `pkg/observability/prometheus.go`
- Test: `pkg/observability/observability_test.go`
- Create: `helm/templates/platform-mcp-servicemonitor.yaml`
- Create: `helm/templates/platform-mcp-prometheusrule.yaml`
- Create: `grafana/platform-mcp-dashboard.json`

- [ ] **Step 1: Write failing bounded-label tests**

Assert every `platform_mcp_*` metric uses only `tool_class`, `risk_level`, `outcome`, and `status_class`; reject tenant, user, execution, proposal, resource, or raw error labels.

- [ ] **Step 2: Implement metrics and spans**

Add counters/histograms for requests, latency, in-flight, auth denials, token exchange, replay denial, API outcomes, unknown outcomes, certificate expiry, and contract mismatch. Propagate `traceparent`/`tracestate`; never place credentials or user content in baggage.

- [ ] **Step 3: Add alerts and dashboard**

Alert on zero ready replicas, sustained readiness failure, token-exchange errors, API 5xx/timeout, replay/auth denial spikes, unknown outcomes, contract mismatch, certificate expiry/rotation failure, and latency budget breach.

- [ ] **Step 4: Run tests and commit**

```bash
go test ./pkg/observability -count=1
helm template stratum ./helm -f helm/values-prod.yaml >/tmp/stratum-platform-mcp-rendered.yaml
git add pkg/observability helm/templates/platform-mcp-* grafana/platform-mcp-dashboard.json
git commit -m "[feat](observability): monitor platform MCP control plane"
```

## Task 14: Full verification and Phase 1 attestation

**Files:**

- Create: `web/e2e/platform-mcp-system-assistant.spec.ts`
- Modify: `web/e2e/stateful/packs/agent-skill-mcp.ts`
- Create: `docs/e2e/platform-mcp-phase-1.md`

- [ ] **Step 1: Add the real browser/API/database journey**

The headless test must prove:

```text
open Agent 管理 -> 平台助手
create conversation without selecting an Agent
ask for tenant diagnostics
observe a real MCP tool call and diagnostic artifact
create a resource-change proposal as admin
open the proposal review page
verify ordinary Agent list excludes the platform assistant
attempt copied Platform MCP URL as tenant-managed config and observe authorization denial
replay an invocation token and observe denial
query the test database for managed server identity, binding, execution, trace, proposal, and consumed JTI
scan UI, logs, traces, and proposal JSON for secret markers
```

- [ ] **Step 2: Run focused and fast gates**

```bash
bash scripts/quality/risk-regression-guard.sh --explain
go vet ./...
go test -short ./...
make fe-lint
make fe-build
make risk-guardrails
make tool-permission-test
make e2e-system-short
```

Expected: all commands pass with no skipped platform-MCP capability.

- [ ] **Step 3: Run race, soak, and attestation**

```bash
go test -v -race -timeout 30s ./...
STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak
make e2e-attestation-check
```

Expected: all pass; attestation matches the current source digest; cleanup completes; no unreconciled capability remains.

- [ ] **Step 4: Record evidence and commit**

Write `docs/e2e/platform-mcp-phase-1.md` with command, timestamp, source commit, result, attestation ID, and any explicit residual risk. Do not include tokens, cookies, passwords, private keys, or raw API keys.

```bash
git diff --check
git add web/e2e docs/e2e/platform-mcp-phase-1.md
git commit -m "[test](mcp): attest platform MCP phase one"
```

## Plan self-review result

- Spec coverage: Phase 1 identity, shared resolver, HTTP boundary, mTLS, token exchange, three migrated tools, frontend tab, deployment, observability, and E2E are mapped to Tasks 1–14.
- Deferred by approved phase boundary: domain CRUD for Agent/Skill/MCP/Knowledge/Model; Workflow/Memory CRUD.
- Excluded by explicit decision: IAM.
- Completeness scan: every code-changing step includes an exact file, command, expected result, and concrete contract.
- Type consistency: `SystemServerID`, `SystemServerKey`, `InvocationClaims`, `APIDelegationClaims`, and `ToolContract` names are used consistently across tasks.
