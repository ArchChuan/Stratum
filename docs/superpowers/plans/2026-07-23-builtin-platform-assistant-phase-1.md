# Built-in Platform Assistant Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every tenant one platform-managed assistant that answers from versioned official documentation and performs role-scoped, read-only application diagnostics.

**Architecture:** Persist one stable assistant identity in each tenant schema, then compose its immutable platform Profile at runtime. Add two hard-coded internal tools for official-document search and tenant diagnostics; deterministic policy resolves tenant/user/role and produces structured evidence artifacts while the model only summarizes bounded evidence.

**Tech Stack:** Go 1.25.12, PostgreSQL/pgx, Gin, React 18, TypeScript, Ant Design 5, Vitest, Playwright, existing ReAct/SSE/Opik infrastructure.

---

## File Structure

### Backend

- `pkg/storage/postgres/tenant_schema.sql`: add managed identity and message artifact DDL; seed the unique assistant instance.
- `pkg/storage/postgres/tenant_schema_test.go`: verify DDL order and managed-instance statements.
- `pkg/storage/postgres/tenant_schema_integration_test.go`: verify idempotency, collision failure, and rollback.
- `internal/agent/domain/system_assistant.go`: system key, Profile, citation, diagnostic report, and artifact value objects.
- `internal/agent/domain/errors.go`: new file for stable managed-assistant and evidence errors.
- `internal/agent/domain/port/system_assistant.go`: Profile, official-doc, role, and diagnostic consumer ports.
- `internal/agent/domain/agent.go`: managed identity fields and execution artifacts.
- `internal/agent/infrastructure/persistence/agent_repo.go`: scan/persist managed identity and update only the assistant model.
- `internal/agent/infrastructure/persistence/agent_repo_test.go`: repository behavior and rollback tests.
- `internal/agent/infrastructure/persistence/chat_store.go`: persist and load structured message artifacts.
- `internal/agent/infrastructure/persistence/chat_store_test.go`: artifact round-trip tests.
- `internal/agent/infrastructure/officialdocs/catalog.json`: generated, embedded official-document chunks.
- `internal/agent/infrastructure/officialdocs/catalog.go`: immutable catalog loader and deterministic search.
- `internal/agent/infrastructure/officialdocs/catalog_test.go`: citations, versions, ranking, and no-match tests.
- `internal/agent/infrastructure/officialdocs/generate/main.go`: build catalog from repository Markdown.
- `docs/assistant/catalog.yaml`: allowlist of official source documents, product version, and stable links.
- `internal/agent/application/system_assistant_profile.go`: immutable Profile and runtime composition.
- `internal/agent/application/system_assistant_tools.go`: system-only tool definitions and deterministic handlers.
- `internal/agent/application/system_assistant_tools_test.go`: system-only exposure, role scope, and failure semantics.
- `internal/agent/application/agent.go`: execution options and hard-coded internal tool dispatch.
- `internal/agent/application/graph/react.go`: execute only the two known internal tools.
- `internal/agent/application/graph/react_test.go`: tool-call/result pairing and untrusted-result wrapping.
- `internal/agent/application/agent_service.go`: compose Profile, attach tools, record Profile manifest and artifacts.
- `internal/agent/application/agent_service_test.go`: CRUD protection, model setting, no-model failure, Profile manifest.
- `api/wiring/system_assistant.go`: thin role and cross-context diagnostic adapters.
- `api/wiring/agent.go`: inject Profile, docs, role, and diagnostic ports.
- `pkg/observability/provider.go`: extend the metrics interface with bounded assistant observations.
- `pkg/observability/prometheus.go`: implement assistant request, evidence, and latency metrics.
- `pkg/observability/observability_test.go`: verify metric labels stay bounded.
- `api/http/handler/system_assistant_handler.go`: dedicated settings transport.
- `api/http/handler/system_assistant_handler_test.go`: admin/member and error-contract tests.
- `api/http/handler/agent_dto.go`: managed identity and artifact response fields.
- `api/http/handler/agent_exec_handler.go`: render structured artifacts in sync and SSE completion responses.
- `api/http/router.go`: register dedicated settings endpoints under tenant RBAC.
- `api/http/contract_test.go` and `api/http/testdata/contracts/*.golden.json`: freeze new fields and routes.

### Frontend

- `web/src/modules/agent/model/agent.ts`: parse managed identity, citations, and diagnostic reports.
- `web/src/modules/agent/api/agent.api.ts`: get/update system-assistant model.
- `web/src/modules/agent/hooks/useChatPage.ts`: stable system-first ordering and artifact hydration.
- `web/src/modules/agent/components/ChatConversationSidebar.tsx`: system label in selector.
- `web/src/modules/agent/components/ChatHeader.tsx`: managed label and admin settings action.
- `web/src/modules/agent/components/SystemAssistantModelModal.tsx`: model-only admin form.
- `web/src/modules/agent/components/DiagnosticReport.tsx`: collapsible facts, inferences, gaps, steps, and citations.
- `web/src/modules/agent/components/ChatMessageList.tsx`: render structured reports below the summary.
- `web/src/modules/agent/pages/AgentChatPage.tsx`: no-model member/admin states.
- `web/src/modules/agent/**/*.test.tsx`: list ordering, role visibility, report, and responsive tests.

### E2E And Documentation

- `test/e2e/system_assistant_test.go`: real API/PostgreSQL/Agent-loop tenant isolation harness.
- `web/e2e/system-assistant.spec.ts`: desktop/mobile browser acceptance.
- `docs/agent/agent.md`, `docs/agent/agent-chat-flow.md`, `docs/SPEC.md`: current-state documentation after implementation.

## Task 1: Persist One Managed Assistant Per Tenant

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_integration_test.go`
- Modify: `internal/agent/domain/agent.go`
- Create: `internal/agent/domain/errors.go`
- Modify: `internal/agent/infrastructure/persistence/agent_repo.go`
- Modify: `internal/agent/infrastructure/persistence/agent_repo_test.go`

- [ ] **Step 1: Write failing tenant-schema tests**

Add integration cases that provision a fresh tenant twice and assert exactly one row:

```go
const systemAssistantKey = "stratum.platform_assistant"

func assertOneSystemAssistant(t *testing.T, pool *pgxpool.Pool, tenantID string) {
 t.Helper()
 var count int
 tenantCtx := tenantdb.WithTenant(context.Background(), tenantdb.TenantContext{TenantID: tenantID})
 err := tenantdb.ExecTenant(tenantCtx, pool, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx,
   `SELECT count(*) FROM agents WHERE system_key=$1`, systemAssistantKey,
  ).Scan(&count)
 })
 require.NoError(t, err)
 require.Equal(t, 1, count)
}
```

Add a historical-schema case with an existing ordinary Agent named `Stratum 系统助手`; provisioning must fail and leave the ordinary row unchanged. Add a transaction-failure case proving no partial managed row remains.

- [ ] **Step 2: Run the schema tests and verify failure**

Run: `go test ./pkg/storage/postgres -run 'SystemAssistant|TenantSchemaHistory' -count=1`

Expected: FAIL because `agents.system_key` and the seeded row do not exist.

- [ ] **Step 3: Add idempotent DDL and managed domain fields**

Immediately after the current `agents` compatibility `ALTER TABLE` statements, add:

```sql
ALTER TABLE agents ADD COLUMN IF NOT EXISTS system_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_system_key
    ON agents(system_key) WHERE system_key IS NOT NULL;

INSERT INTO agents (
    id, name, type, description, system_prompt, llm_model, embed_model,
    max_iterations, max_context_tokens, memory_scope, system_key
) VALUES (
    'stratum-platform-assistant',
    'Stratum 系统助手',
    'react',
    '基于官方资料指导平台使用并诊断当前租户应用状态',
    '', '', '', 10, 8000, 'user', 'stratum.platform_assistant'
)
ON CONFLICT (id) DO NOTHING;
```

Do not use `ON CONFLICT (name)` and do not rename a colliding ordinary Agent. The collision must fail provisioning and surface operator action.

Extend `domain.AgentConfig` and `application.AgentDTO`:

```go
SystemKey      string
IsSystem       bool
ManagementMode string
```

Add `domain.ErrSystemAssistantManaged = errors.New("system assistant is platform managed")`.

- [ ] **Step 4: Update repository scans and writes**

Include `system_key` in every `agents` SELECT and `Scan`. Ordinary `Register` always writes `NULL`; add focused methods to `port.AgentRepo`:

```go
GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error)
UpdateSystemAssistantModel(ctx context.Context, model string) error
```

The update SQL must be:

```sql
UPDATE agents SET llm_model=$1, updated_at=NOW()
WHERE system_key='stratum.platform_assistant'
```

Return `domain.ErrNotFound` when no row changes. `Remove` and general `Update` must first load `system_key` in the same tenant transaction and return `ErrSystemAssistantManaged` before relation cleanup.

- [ ] **Step 5: Run focused repository and schema tests**

Run: `go test ./pkg/storage/postgres ./internal/agent/infrastructure/persistence -run 'SystemAssistant|AgentRepo' -count=1`

Expected: PASS, including idempotency, name-collision failure, protected update/delete, and rollback.

- [ ] **Step 6: Commit the managed identity slice**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go pkg/storage/postgres/tenant_schema_integration_test.go internal/agent/domain internal/agent/infrastructure/persistence
git commit -m "[feat](agent): provision managed platform assistant"
```

## Task 2: Compose An Immutable Runtime Profile

**Files:**

- Create: `internal/agent/domain/system_assistant.go`
- Create: `internal/agent/domain/port/system_assistant.go`
- Create: `internal/agent/application/system_assistant_profile.go`
- Create: `internal/agent/application/system_assistant_profile_test.go`
- Modify: `internal/agent/application/registry.go`
- Modify: `internal/agent/application/agent_service.go`

- [ ] **Step 1: Write failing Profile composition tests**

Cover ordinary Agent passthrough, system Agent replacement of protected fields, model preservation, empty tenant extensions, and version recording:

```go
func TestComposeSystemAssistantProfilePreservesOnlyTenantModel(t *testing.T) {
 cfg := &domain.AgentConfig{SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus", SystemPrompt: "tenant override"}
 got, err := ComposeSystemAssistantProfile(cfg, BuiltinSystemAssistantProfile())
 require.NoError(t, err)
 require.Equal(t, "qwen-plus", got.LLMModel)
 require.NotContains(t, got.SystemPrompt, "tenant override")
 require.Empty(t, got.AllowedSkills)
 require.Empty(t, got.MCPToolIDs)
 require.Empty(t, got.KnowledgeWorkspaceIDs)
}
```

- [ ] **Step 2: Run the tests and verify failure**

Run: `go test ./internal/agent/application -run SystemAssistantProfile -count=1`

Expected: FAIL because the Profile types and composer do not exist.

- [ ] **Step 3: Define the Profile and composer**

Use fixed, code-reviewed values:

```go
const SystemAssistantKey = "stratum.platform_assistant"

type SystemAssistantProfile struct {
 Key          string
 Version      string
 Name         string
 Description  string
 SystemPrompt string
 MaxIterations int
 MaxContextTokens int
}
```

`BuiltinSystemAssistantProfiles()` returns a map containing version `2026-07-23.v1`, and `CurrentSystemAssistantProfileVersion` selects the active entry. Retain old entries when adding a version so trace evidence remains resolvable and rollback is a one-line active-version change. The prompt must state: current-tenant only; official citations required; fact/inference/gap separation; no writes; no secret requests; unavailable evidence is not health.

`ComposeSystemAssistantProfile` copies the input, replaces all protected fields, clears tenant Skill/MCP/Knowledge relations, preserves only `ID`, `LLMModel`, `EmbedModel`, `MemoryScope`, and marks `IsSystem=true`, `ManagementMode="platform"`.

- [ ] **Step 4: Inject composition in `Registry.hydrate`**

Add a `SystemAssistantProfile` dependency to `Registry`; return a hydrated Agent only after successful composition. Change `Registry.Get/GetAll` to return errors instead of silently converting repository or composition failures into not-found/empty results, and synchronize all call sites and mocks. This prevents a storage failure from appearing as “no agents”.

- [ ] **Step 5: Pin Profile version in trace metadata**

In `assembleOptions`, add:

```go
if cfg.SystemKey == domain.SystemAssistantKey {
 evolutionTrace.ResourceManifest["system-assistant-profile"] = s.deps.SystemAssistantProfile.Version
}
```

Do not add a new tenant execution-history table; Opik/trace resource manifests are the current execution evidence source.

- [ ] **Step 6: Run Agent application tests**

Run: `go test ./internal/agent/application/... -run 'SystemAssistant|Registry|AgentService' -count=1`

Expected: PASS with repository failures propagated and Profile version present in trace metadata.

- [ ] **Step 7: Commit the Profile slice**

```bash
git add internal/agent/domain internal/agent/application
git commit -m "[feat](agent): compose managed assistant profile"
```

## Task 3: Build A Versioned Read-only Official Document Catalog

**Files:**

- Create: `docs/assistant/catalog.yaml`
- Create: `internal/agent/infrastructure/officialdocs/generate/main.go`
- Create: `internal/agent/infrastructure/officialdocs/catalog.go`
- Create: `internal/agent/infrastructure/officialdocs/catalog.json`
- Create: `internal/agent/infrastructure/officialdocs/catalog_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Write failing catalog tests**

Define expected result shape:

```go
type Citation struct {
 DocumentID     string `json:"documentId"`
 Title          string `json:"title"`
 ProductVersion string `json:"productVersion"`
 Section        string `json:"section"`
 URL            string `json:"url"`
 Excerpt        string `json:"excerpt"`
}
```

Test Chinese and ASCII queries, stable ordering, maximum excerpt length, configured version/link, empty-query rejection, and an unmatched query returning `domain.ErrOfficialEvidenceNotFound` with no fabricated citation.

- [ ] **Step 2: Run the catalog tests and verify failure**

Run: `go test ./internal/agent/infrastructure/officialdocs -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add the source manifest and generator**

The YAML manifest explicitly allowlists source files and stable links:

```yaml
product_version: "2026.07"
documents:
  - id: agent-guide
    title: Agent 使用指南
    source: docs/agent/agent.md
    url: /docs/agent/agent
  - id: mcp-guide
    title: MCP 使用指南
    source: docs/mcp-integration.md
    url: /docs/mcp-integration
  - id: knowledge-guide
    title: Knowledge 使用指南
    source: docs/agent/knowledge-workspace.md
    url: /docs/agent/knowledge-workspace
```

The generator resolves paths from repository root, splits by Markdown headings with `pkg/textchunk`, emits normalized JSON, rejects duplicate IDs/URLs and empty sections, and sorts by `(documentId, section, ordinal)`. Add `//go:generate go run ./generate -manifest ../../../../docs/assistant/catalog.yaml -out catalog.json`.

- [ ] **Step 4: Implement deterministic search**

Embed `catalog.json` with `//go:embed`. Tokenize ASCII words and Chinese rune bigrams, score title/section hits above body hits, cap results with a package constant, and return only catalog content. Do not read repository files at runtime and do not query tenant Knowledge.

- [ ] **Step 5: Verify reproducibility and tests**

Run: `go generate ./internal/agent/infrastructure/officialdocs`

Run: `git diff --exit-code -- internal/agent/infrastructure/officialdocs/catalog.json`

Run: `go test ./internal/agent/infrastructure/officialdocs -count=1`

Expected: generated catalog is stable and all catalog tests PASS.

- [ ] **Step 6: Commit the official-doc slice**

```bash
git add docs/assistant internal/agent/infrastructure/officialdocs go.mod go.sum
git commit -m "[feat](agent): add versioned official docs catalog"
```

## Task 4: Define Role-scoped Diagnostic Evidence

**Files:**

- Modify: `internal/agent/domain/system_assistant.go`
- Modify: `internal/agent/domain/port/system_assistant.go`
- Create: `internal/agent/application/system_assistant_policy.go`
- Create: `internal/agent/application/system_assistant_policy_test.go`
- Create: `api/wiring/system_assistant.go`
- Create: `api/wiring/system_assistant_test.go`
- Modify: `api/wiring/agent.go`

- [ ] **Step 1: Write failing policy tests**

Use explicit scopes:

```go
type DiagnosticScope string
const (
 DiagnosticScopeSelf   DiagnosticScope = "self"
 DiagnosticScopeTenant DiagnosticScope = "tenant"
)

type DiagnosticRequest struct {
 TenantID string
 UserID   string
 Scope    DiagnosticScope
 Areas    []DiagnosticArea
}
```

Test `member -> self`, `admin/owner -> tenant`, unknown role -> forbidden, and role resolver error -> forbidden. Test that requested scope can only be narrowed, never widened.

- [ ] **Step 2: Run the policy tests and verify failure**

Run: `go test ./internal/agent/application ./api/wiring -run 'DiagnosticScope|SystemAssistantDiagnostic' -count=1`

Expected: FAIL because policy and adapters do not exist.

- [ ] **Step 3: Define the evidence DTO and consumer ports**

```go
type DiagnosticFact struct {
 Area       DiagnosticArea `json:"area"`
 ObjectID   string         `json:"objectId,omitempty"`
 Statement  string         `json:"statement"`
 Source     string         `json:"source"`
 ObservedAt time.Time      `json:"observedAt"`
}

type DiagnosticEvidenceProvider interface {
 Collect(ctx context.Context, req DiagnosticRequest) (DiagnosticEvidence, error)
}

type TenantRoleResolver interface {
 ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error)
}
```

Evidence errors are per-area and contain safe codes, never raw upstream bodies.

- [ ] **Step 4: Implement thin wiring adapters**

`systemAssistantDiagnosticAdapter` calls existing application services or focused repositories for:

- Agent executions through `TraceEvidenceProvider`, filtering `UserID` for self scope;
- Skill product/revision and evaluation status;
- MCP `ListServers`, `ServerStatus`, tool-policy status, and safe error metadata;
- Knowledge workspace/document ingest status;
- tenant model-configuration completeness.

Each independent read gets `constants.AgentDBQueryTimeout`; use `errgroup.WithContext` with a fixed concurrency limit. Every goroutine must finish before return. An area failure produces `EvidenceGap`; membership/tenant failure aborts the whole collection.

- [ ] **Step 5: Add adapter tests for tenant and role isolation**

Tests must prove self scope excludes another user's executions, tenant scope includes only the current tenant, a failed role lookup calls no evidence provider, and raw MCP/Knowledge error bodies are absent.

- [ ] **Step 6: Run policy and wiring tests**

Run: `go test ./internal/agent/application ./api/wiring -run 'Diagnostic|SystemAssistant' -count=1`

Expected: PASS with fail-closed authorization and bounded concurrency.

- [ ] **Step 7: Commit the diagnostic evidence slice**

```bash
git add internal/agent/domain internal/agent/application api/wiring
git commit -m "[feat](agent): collect role scoped diagnostics"
```

## Task 5: Expose Only Hard-coded Internal Assistant Tools

**Files:**

- Create: `internal/agent/application/system_assistant_tools.go`
- Create: `internal/agent/application/system_assistant_tools_test.go`
- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_test.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `pkg/observability/provider.go`
- Modify: `pkg/observability/prometheus.go`
- Modify: `pkg/observability/observability_test.go`

- [ ] **Step 1: Write failing tool-exposure and execution tests**

Assert ordinary Agents never see the tools and the system assistant sees exactly:

```go
const (
 ToolSearchOfficialDocs = "stratum_search_official_docs"
 ToolDiagnoseTenant     = "stratum_diagnose_tenant"
)
```

Test invalid areas, forged tenant/user arguments, docs no-match, diagnostic gaps, timeout, and tool output wrapped as untrusted evidence before returning to the model.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/agent/application/... -run 'SystemAssistantTool|OfficialDocsTool|DiagnosticTool' -count=1`

Expected: FAIL because the definitions and dispatch cases are absent.

- [ ] **Step 3: Add execution options and fixed definitions**

Tool schemas accept only query/areas; they do not accept tenant, user, role, SQL, URL, command, or arbitrary resource identifiers. Add typed callbacks to `ExecutionConfig`:

```go
OfficialDocsSearchFn func(context.Context, string) ([]domain.Citation, error)
DiagnosticFn         func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
```

- [ ] **Step 4: Add hard-coded ReAct dispatch**

In `graph/react.go`, handle only the two constant names. Derive tenant/user/role from the closure created by `AgentService.assembleOptions`; ignore any similarly named MCP tool. Convert results to bounded JSON, pass them through the existing result guard/untrusted wrapper, and append typed execution artifacts.

- [ ] **Step 5: Attach tools only for the managed system key**

In `assembleOptions`, branch on `a.GetConfig().SystemKey`. If its model is empty, return `domain.ErrAssistantModelUnavailable` before resolving a capability gateway. Otherwise attach the two tool definitions and callbacks. Do not attach tenant Skill, MCP, Knowledge, or memory-recall tools to the system assistant in phase 1.

- [ ] **Step 6: Add bounded assistant metrics**

Record bounded-label metrics for request outcome, time to first streamed token, official-search result count, diagnostic area outcome, diagnostic duration, and evidence-gap count. Labels may contain role class, area, outcome, and Profile version; they must not contain tenant, user, query, document title, or resource ID. Add metric tests that reject unbounded labels.

- [ ] **Step 7: Run Agent loop tests**

Run: `go test ./internal/agent/application/... -run 'SystemAssistant|OfficialDocs|Diagnostic' -count=1`

Expected: PASS; ordinary Agents retain their existing tool catalog and system assistant tool failures remain visible.

- [ ] **Step 8: Commit the internal-tool slice**

```bash
git add internal/agent/application internal/agent/domain pkg/observability
git commit -m "[feat](agent): add governed assistant tools"
```

## Task 6: Persist Structured Diagnostic Artifacts

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `internal/agent/domain/agent.go`
- Modify: `internal/agent/infrastructure/persistence/chat_store.go`
- Modify: `internal/agent/infrastructure/persistence/chat_store_test.go`
- Modify: `api/http/handler/agent_dto.go`
- Modify: `api/http/handler/agent_exec_handler.go`
- Modify: `api/http/handler/agent_exec_handler_test.go`
- Modify: `api/http/handler/sse_writer_test.go`

- [ ] **Step 1: Write failing artifact round-trip tests**

Define:

```go
type ExecutionArtifact struct {
 Type             string            `json:"type"`
 ProfileVersion   string            `json:"profileVersion,omitempty"`
 Citations        []Citation        `json:"citations,omitempty"`
 DiagnosticReport *DiagnosticReport `json:"diagnosticReport,omitempty"`
}
```

Test PostgreSQL write/read, sync response, SSE `done` response, and historical chat hydration. Malformed stored JSON must return an error, not silently drop evidence.

- [ ] **Step 2: Run persistence and handler tests and verify failure**

Run: `go test ./internal/agent/infrastructure/persistence ./api/http/handler -run 'Artifact|DiagnosticReport|ExecuteAgent' -count=1`

Expected: FAIL because artifacts are not persisted or rendered.

- [ ] **Step 3: Add schema and domain fields**

Add after `chat_messages` creation:

```sql
ALTER TABLE chat_messages
    ADD COLUMN IF NOT EXISTS artifacts_json JSONB NOT NULL DEFAULT '[]';
```

Add `Artifacts []ExecutionArtifact` to `AgentResult` and `ChatMessage`. Marshal to `string(b)` before pgx JSONB writes.

- [ ] **Step 4: Build reports deterministically from tool artifacts**

The final natural-language answer remains `Output`. Build `DiagnosticReport` from collected facts/gaps/citations and tool timings, with separate arrays for `facts`, `inferences`, `evidenceGaps`, `recommendedActions`, `citations`, and `steps`. Do not parse claims back out of model prose. Inferences may be empty in phase 1 unless the model returns a separately validated structured field.

- [ ] **Step 5: Render artifacts in sync and stream responses**

Add `artifacts` to `AgentExecutionResult`. The SSE completion payload must use the same DTO conversion function as synchronous execution so contracts cannot drift.

- [ ] **Step 6: Run focused tests**

Run: `go test ./internal/agent/infrastructure/persistence ./api/http/handler -run 'Artifact|DiagnosticReport|ExecuteAgent' -count=1`

Expected: PASS with identical sync/SSE artifact shapes.

- [ ] **Step 7: Commit the artifact slice**

```bash
git add pkg/storage/postgres/tenant_schema.sql internal/agent api/http/handler
git commit -m "[feat](agent): persist diagnostic artifacts"
```

## Task 7: Add Managed Settings And HTTP Contracts

**Files:**

- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent_service_test.go`
- Create: `api/http/handler/system_assistant_handler.go`
- Create: `api/http/handler/system_assistant_handler_test.go`
- Modify: `api/http/handler/agent_dto.go`
- Modify: `api/http/router.go`
- Modify: `api/http/contract_test.go`
- Modify/Create: `api/http/testdata/contracts/*.golden.json`

- [ ] **Step 1: Write failing service and handler tests**

Test list ordering, `isSystem/managementMode`, member GET visibility, admin-only PUT, empty/unknown model rejection, general update/delete protection, and frozen `{"error":"..."}` response bodies.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/agent/application ./api/http/handler -run 'SystemAssistant|ManagedAgent' -count=1`

Expected: FAIL because the dedicated use cases and routes do not exist.

- [ ] **Step 3: Add dedicated application methods**

```go
type SystemAssistantSettings struct {
 AgentID string
 Model   string
 Ready   bool
}

func (s *AgentService) GetSystemAssistantSettings(ctx context.Context) (SystemAssistantSettings, error)
func (s *AgentService) UpdateSystemAssistantModel(ctx context.Context, model string) (SystemAssistantSettings, error)
```

Validate the model against the existing tenant model catalog/resolver; do not accept a free-form provider credential. Sort `List` with system Agent first and preserve ordinary Agent creation order afterward.

- [ ] **Step 4: Add routes under existing RBAC**

Register:

```go
agents.GET("/system/settings", handler.GetSettings)
agents.PUT("/system/settings", requireAdmin, requireActive, handler.UpdateModel)
```

Place static `/system/settings` routes before `/:id` routes. GET returns readiness without secrets; PUT accepts only `{ "llmModel": "..." }`.

- [ ] **Step 5: Update and verify contract goldens**

Run: `go test ./api/http -run Contract -count=1 -update`

Review every changed golden; only managed fields, settings routes, and artifacts may change.

Run: `go test ./api/http ./api/http/handler -run 'Contract|SystemAssistant|ManagedAgent' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit HTTP contracts**

```bash
git add internal/agent/application api/http
git commit -m "[feat](api): expose managed assistant settings"
```

## Task 8: Build The System-first Chat Experience

**Files:**

- Modify: `web/src/modules/agent/model/agent.ts`
- Modify: `web/src/modules/agent/api/agent.api.ts`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Modify: `web/src/modules/agent/components/ChatConversationSidebar.tsx`
- Modify: `web/src/modules/agent/components/ChatHeader.tsx`
- Create: `web/src/modules/agent/components/SystemAssistantModelModal.tsx`
- Create: `web/src/modules/agent/components/DiagnosticReport.tsx`
- Modify: `web/src/modules/agent/components/ChatMessageList.tsx`
- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`
- Create/Modify: `web/src/modules/agent/**/__tests__/*.test.tsx`

- [ ] **Step 1: Write failing model and component tests**

Test Zod defaults, system-first selector options, “系统内置” label, admin settings button, member contact-admin state, model modal payload, citation links, facts/gaps separation, and narrow mobile rendering without overlap.

- [ ] **Step 2: Run frontend tests and verify failure**

Run: `cd web && npm test -- --run src/modules/agent`

Expected: FAIL because managed fields and components do not exist.

- [ ] **Step 3: Extend frontend models**

Add `isSystem`, `managementMode`, `artifacts`, `DiagnosticReport`, and `Citation` schemas. Use `.nullish().transform(v => v ?? [])` for every artifact array so historical messages remain compatible.

- [ ] **Step 4: Implement system-first navigation and settings**

Render Select labels as React nodes with the system tag. Keep the system Agent first even when the API order is stale. The modal contains one model Select populated from the existing model API; no prompt, Skill, MCP, Knowledge, or credential fields.

- [ ] **Step 5: Render the two-layer answer**

Keep `ChatMarkdown` as the summary. Below it render an Ant Design `Collapse` with facts, evidence gaps, recommended actions, tool timings, and citations. Use stable dimensions and wrapping; do not nest cards.

- [ ] **Step 6: Run unit and responsive tests**

Run: `cd web && npm test -- --run src/modules/agent`

Run: `make fe-lint && make fe-build`

Expected: all commands PASS.

- [ ] **Step 7: Commit the frontend slice**

```bash
git add web/src/modules/agent
git commit -m "[feat](frontend): surface platform assistant"
```

## Task 9: Verify Real Tenant, API, Agent-loop, And Browser Chains

**Files:**

- Create: `test/e2e/system_assistant_test.go`
- Create: `web/e2e/system-assistant.spec.ts`
- Modify: `docs/agent/agent.md`
- Modify: `docs/agent/agent-chat-flow.md`
- Modify: `docs/SPEC.md`

- [ ] **Step 1: Read and activate the required E2E skill**

Read `stratum-e2e-development` completely and follow its environment, real-service, browser, cleanup, and evidence requirements. Record the selected database, model stub/provider, and viewport matrix without printing credentials.

- [ ] **Step 2: Add the backend real-chain harness**

Cover two tenants and two users per tenant:

```go
func TestSystemAssistantTenantIsolationAndRoleScope(t *testing.T) {
 // provision A/B, assert one managed instance each, execute official search,
 // execute member/admin diagnostics, and assert no A identifiers in B artifacts.
}
```

Include no-model, role-read failure, docs no-match, one diagnostic-area failure, malformed tenant ID, protected delete/update, and Profile manifest evidence.

- [ ] **Step 3: Add browser acceptance**

At desktop and mobile widths, verify fixed system-first selection, managed tag, admin/member no-model states, streamed summary, collapsible report, citations, evidence gap, and absence of edit/delete controls. Capture screenshots and check no text overlap.

- [ ] **Step 4: Run risk and targeted integration gates**

Run: `make risk-guardrails`

Run: `make tool-permission-test`

Run: `STRATUM_TEST_POSTGRES_URL=... go test -v -count=1 ./test/e2e -run SystemAssistant`

Run: `cd web && npx playwright test e2e/system-assistant.spec.ts`

Expected: all gates PASS; the PostgreSQL test must fail rather than skip when its URL is absent in CI.

- [ ] **Step 5: Run full backend and frontend verification**

Run: `go vet ./...`

Run: `go test -short ./...`

Run: `go test -v -race -timeout 30s ./...`

Run: `make fe-lint && make fe-build`

Expected: all commands PASS with no leaked background process.

- [ ] **Step 6: Update current-state documentation**

Document exact Profile versioning, managed fields, official catalog generation, role evidence matrix, internal tools, artifacts, routes, error codes, and E2E commands. Do not describe phase 2 as implemented.

- [ ] **Step 7: Commit E2E and documentation**

```bash
git add test/e2e web/e2e docs/agent docs/SPEC.md
git commit -m "[test](agent): verify platform assistant phase one"
```

## Phase 1 Completion Gate

- [ ] Run `git diff origin/main --check`.
- [ ] Run `make risk-guardrails` and every command in Task 9 with fresh output.
- [ ] Confirm every tenant-scoped repository call uses tenant context/`execTenant`.
- [ ] Confirm ordinary Agents cannot see either system tool.
- [ ] Confirm a failed membership lookup produces no evidence query.
- [ ] Confirm official no-match returns a knowledge gap, not model fallback.
- [ ] Confirm logs, traces, SSE, artifacts, screenshots, and fixtures contain no secret.
- [ ] Confirm the phase 1 UI has no resource-change entry point.
