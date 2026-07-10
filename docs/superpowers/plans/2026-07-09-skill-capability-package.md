# Skill Capability Package Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Evolve Skill from `type/config` runtime records into versioned capability packages that publish stable Agent Tool Contracts.

**Architecture:** Add versioned Skill storage beside the legacy `skills(type, config)` table, backfill existing skills into published versions, then switch Agent tool resolution from generic prompt-shaped tools to published `tool_contract` snapshots. Keep legacy endpoints and execution fallback during migration.

**Tech Stack:** Go 1.25, Gin, pgx v5, tenant PostgreSQL schemas, React/Vite/Ant Design, existing ReAct Agent loop and Skill Gateway.

---

## File Map

- `pkg/storage/postgres/tenant_schema.sql`: tenant DDL for `skills.active_version_id`, `skills.draft_version_id`, `skill_versions`, `skill_test_cases`, and `skill_eval_runs`.
- `internal/skill/domain/version.go`: new domain value objects for capability, tool contract, implementation, test case, eval run, and version status.
- `internal/skill/domain/version_test.go`: domain validation tests for tool names, schemas, and publish readiness.
- `internal/skill/domain/port/version_repository.go`: repository ports for versioned Skill storage and published tool resolution.
- `internal/skill/infrastructure/persistence/skill_version_repo.go`: Postgres adapter for versioned tables.
- `internal/skill/infrastructure/persistence/skill_version_repo_test.go`: SQL shape/unit tests where possible; integration tests can follow later.
- `internal/skill/application/version_service.go`: draft/version use cases, backfill helpers, contract generation baseline, publish gate.
- `internal/skill/application/version_service_test.go`: application-layer TDD tests with fake repos.
- `internal/agent/domain/port/capability.go`: extend `SkillCapRequest` and tool index types to include `VersionID`.
- `internal/agent/application/agent_service.go`: replace legacy `SkillLookup` tool construction with version-aware resolver.
- `internal/agent/application/graph/react.go`: carry `VersionID` through Skill tool execution and trace fields.
- `internal/skill/infrastructure/gateway/providers/registry_adapter.go`: execute by `skill_versions.implementation` when version ID is provided, fallback to legacy row.
- `api/http/dto/request.go`: new draft/version request DTOs.
- `api/http/dto/skill_response.go`: new response DTOs if needed to avoid bloating request DTO file.
- `api/http/handler/skill_handler.go`: add draft/version handlers while preserving legacy routes.
- `api/http/router.go`: register new Skill routes.
- `api/http/testdata/contracts/*.golden.json`: add unauth contract goldens for new public API surface.
- `web/src/modules/skill/*`: later phase for Skill workspace tabs.

---

## Task 1: Tenant Schema For Versioned Skills

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Create: `pkg/storage/postgres/tenant_schema_test.go`

- [x] **Step 1: Write failing schema test**

Create `pkg/storage/postgres/tenant_schema_test.go`:

```go
package postgres_test

import (
 "os"
 "strings"
 "testing"
)

func TestTenantSchemaContainsVersionedSkillTables(t *testing.T) {
 data, err := os.ReadFile("tenant_schema.sql")
 if err != nil {
  t.Fatal(err)
 }
 sql := string(data)

 required := []string{
  "ALTER TABLE skills ADD COLUMN IF NOT EXISTS active_version_id TEXT",
  "ALTER TABLE skills ADD COLUMN IF NOT EXISTS draft_version_id TEXT",
  "CREATE TABLE IF NOT EXISTS skill_versions",
  "CREATE TABLE IF NOT EXISTS skill_test_cases",
  "CREATE TABLE IF NOT EXISTS skill_eval_runs",
  "skill_id      TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE",
  "tool_contract JSONB NOT NULL DEFAULT '{}'",
  "implementation JSONB NOT NULL DEFAULT '{}'",
 }
 for _, want := range required {
  if !strings.Contains(sql, want) {
   t.Fatalf("tenant_schema.sql missing %q", want)
  }
 }
}
```

- [x] **Step 2: Run test to verify RED**

Run: `go test ./pkg/storage/postgres -run TestTenantSchemaContainsVersionedSkillTables -count=1`

Expected: FAIL because the new columns/tables do not exist.

- [x] **Step 3: Add idempotent tenant DDL**

In `pkg/storage/postgres/tenant_schema.sql`, immediately after the existing `CREATE TABLE IF NOT EXISTS skills (...)` block, add:

```sql
ALTER TABLE skills ADD COLUMN IF NOT EXISTS active_version_id TEXT;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS draft_version_id TEXT;

CREATE TABLE IF NOT EXISTS skill_versions (
    id              TEXT PRIMARY KEY,
    skill_id        TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no      INT,
    status          TEXT NOT NULL DEFAULT 'draft',
    capability      JSONB NOT NULL DEFAULT '{}',
    tool_contract   JSONB NOT NULL DEFAULT '{}',
    implementation  JSONB NOT NULL DEFAULT '{}',
    test_baseline   JSONB NOT NULL DEFAULT '{}',
    publish_checks  JSONB NOT NULL DEFAULT '{}',
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_versions_one_draft
    ON skill_versions(skill_id)
    WHERE status = 'draft';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_versions_published_version
    ON skill_versions(skill_id, version_no)
    WHERE version_no IS NOT NULL;

CREATE TABLE IF NOT EXISTS skill_test_cases (
    id              TEXT PRIMARY KEY,
    skill_id        TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    name            TEXT NOT NULL DEFAULT '',
    input           JSONB NOT NULL DEFAULT '{}',
    expected_output JSONB NOT NULL DEFAULT '{}',
    assertion_mode  TEXT NOT NULL DEFAULT 'contains',
    enabled         BOOL NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS skill_eval_runs (
    id              TEXT PRIMARY KEY,
    skill_id        TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_id      TEXT REFERENCES skill_versions(id) ON DELETE SET NULL,
    test_case_id    TEXT REFERENCES skill_test_cases(id) ON DELETE SET NULL,
    mode            TEXT NOT NULL,
    input           JSONB NOT NULL DEFAULT '{}',
    actual_output   JSONB NOT NULL DEFAULT '{}',
    expected_output JSONB NOT NULL DEFAULT '{}',
    passed          BOOL NOT NULL DEFAULT false,
    trace           JSONB NOT NULL DEFAULT '{}',
    duration_ms     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [x] **Step 4: Run schema test**

Run: `go test ./pkg/storage/postgres -run TestTenantSchemaContainsVersionedSkillTables -count=1`

Expected: PASS.

---

## Task 2: Domain Objects And Validation

**Files:**

- Create: `internal/skill/domain/version.go`
- Create: `internal/skill/domain/version_test.go`

- [x] **Step 1: Write failing domain validation tests**

Create `internal/skill/domain/version_test.go`:

```go
package domain

import "testing"

func TestToolContractValidateRequiresConfirmedValidContract(t *testing.T) {
 contract := ToolContract{
  ToolName:        "classify_complaint",
  Description:     "判断客户投诉类型并给出处理建议",
  InputSchema:     map[string]any{"type": "object", "properties": map[string]any{}},
  OutputSchema:    map[string]any{"type": "object"},
  CallingGuidance: "只在用户表达投诉时调用",
  Confirmed:       true,
 }

 if err := contract.Validate(); err != nil {
  t.Fatalf("Validate() error = %v", err)
 }
}

func TestToolContractValidateRejectsUnsafeToolName(t *testing.T) {
 contract := ToolContract{
  ToolName:     "投诉 分类",
  Description:  "判断客户投诉类型",
  InputSchema:  map[string]any{"type": "object"},
  OutputSchema: map[string]any{"type": "object"},
  Confirmed:    true,
 }

 if err := contract.Validate(); err == nil {
  t.Fatal("expected invalid tool name error")
 }
}

func TestSkillVersionPublishableRequiresCapabilityContractImplementationAndTests(t *testing.T) {
 version := SkillVersion{
  Status: VersionStatusDraft,
  Capability: Capability{
   Goal:      "判断客户投诉类型",
   WhenToUse: "用户表达投诉时",
   Examples:  []CapabilityExample{{Input: "快递没更新", ExpectedOutput: "物流问题"}},
  },
  ToolContract: ToolContract{
   ToolName:     "classify_complaint",
   Description:  "判断客户投诉类型",
   InputSchema:  map[string]any{"type": "object"},
   OutputSchema: map[string]any{"type": "object"},
   Confirmed:    true,
  },
  Implementation: Implementation{
   Mode: "prompt",
   Source: map[string]any{"promptTemplate": "分类：{{.input}}"},
  },
 }

 if err := version.ValidatePublishable(1); err != nil {
  t.Fatalf("ValidatePublishable() error = %v", err)
 }
}
```

- [x] **Step 2: Run test to verify RED**

Run: `go test ./internal/skill/domain -run 'TestToolContract|TestSkillVersionPublishable' -count=1`

Expected: FAIL because types are undefined.

- [x] **Step 3: Add domain objects**

Create `internal/skill/domain/version.go`:

```go
package domain

import (
 "fmt"
 "regexp"
 "strings"
)

type VersionStatus string

const (
 VersionStatusDraft      VersionStatus = "draft"
 VersionStatusPublished  VersionStatus = "published"
 VersionStatusDeprecated VersionStatus = "deprecated"
)

type Capability struct {
 Goal       string              `json:"goal"`
 WhenToUse  string              `json:"whenToUse"`
 InputSpec  string              `json:"inputSpec,omitempty"`
 OutputSpec string              `json:"outputSpec,omitempty"`
 Examples   []CapabilityExample `json:"examples,omitempty"`
}

type CapabilityExample struct {
 Input          any `json:"input"`
 ExpectedOutput any `json:"expectedOutput"`
}

type ToolContract struct {
 ToolName        string         `json:"toolName"`
 Description     string         `json:"description"`
 InputSchema     map[string]any `json:"inputSchema"`
 OutputSchema    map[string]any `json:"outputSchema"`
 CallingGuidance string         `json:"callingGuidance,omitempty"`
 Confirmed       bool           `json:"confirmed"`
}

type Implementation struct {
 Mode        string         `json:"mode"`
 Source      map[string]any `json:"source"`
 Runtime     map[string]any `json:"runtime,omitempty"`
 Permissions map[string]any `json:"permissions,omitempty"`
 SecretRefs  []string       `json:"secretRefs,omitempty"`
}

type SkillVersion struct {
 ID             string
 SkillID        string
 VersionNo      int
 Status         VersionStatus
 Capability     Capability
 ToolContract   ToolContract
 Implementation Implementation
 TestBaseline   map[string]any
 PublishChecks  map[string]any
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

func (c ToolContract) Validate() error {
 if !toolNamePattern.MatchString(c.ToolName) {
  return fmt.Errorf("invalid tool name: %s", c.ToolName)
 }
 if strings.TrimSpace(c.Description) == "" {
  return fmt.Errorf("tool description required")
 }
 if !isObjectSchema(c.InputSchema) {
  return fmt.Errorf("input schema must be object schema")
 }
 if !isObjectSchema(c.OutputSchema) {
  return fmt.Errorf("output schema must be object schema")
 }
 if !c.Confirmed {
  return fmt.Errorf("tool contract not confirmed")
 }
 return nil
}

func (v SkillVersion) ValidatePublishable(enabledTestCount int) error {
 if strings.TrimSpace(v.Capability.Goal) == "" {
  return fmt.Errorf("capability goal required")
 }
 if strings.TrimSpace(v.Capability.WhenToUse) == "" {
  return fmt.Errorf("capability whenToUse required")
 }
 if len(v.Capability.Examples) == 0 && enabledTestCount == 0 {
  return fmt.Errorf("at least one example or enabled test case required")
 }
 if err := v.ToolContract.Validate(); err != nil {
  return err
 }
 if strings.TrimSpace(v.Implementation.Mode) == "" {
  return fmt.Errorf("implementation mode required")
 }
 if v.Implementation.Source == nil {
  return fmt.Errorf("implementation source required")
 }
 return nil
}

func isObjectSchema(schema map[string]any) bool {
 if schema == nil {
  return false
 }
 typ, _ := schema["type"].(string)
 return typ == "object"
}
```

- [x] **Step 4: Run domain tests**

Run: `go test ./internal/skill/domain -run 'TestToolContract|TestSkillVersionPublishable' -count=1`

Expected: PASS.

---

## Task 3: Version Repository Port And Application Service

**Files:**

- Create: `internal/skill/domain/port/version_repository.go`
- Create: `internal/skill/application/version_service.go`
- Create: `internal/skill/application/version_service_test.go`

- [x] **Step 1: Write failing application tests**

Create `internal/skill/application/version_service_test.go` with fake repositories and these tests:

```go
package application

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/skill/domain"
 "go.uber.org/zap"
)

func TestVersionServiceCreateSkillCreatesDraftVersionAndTestCase(t *testing.T) {
 repo := newFakeVersionRepo()
 svc := NewVersionService(repo, zap.NewNop())

 view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
  Name:           "投诉分类",
  Goal:           "判断客户投诉类型",
  WhenToUse:      "用户表达投诉时",
  SampleInput:    "快递没更新",
  ExpectedOutput: "物流问题",
 })
 if err != nil {
  t.Fatalf("CreateSkillDraft() error = %v", err)
 }
 if view.Skill.Name != "投诉分类" {
  t.Fatalf("expected skill name, got %q", view.Skill.Name)
 }
 if view.Draft.Capability.Goal != "判断客户投诉类型" {
  t.Fatalf("expected capability goal, got %q", view.Draft.Capability.Goal)
 }
 if view.Draft.ToolContract.ToolName == "" {
  t.Fatal("expected generated tool name")
 }
 if len(repo.testCases) != 1 {
  t.Fatalf("expected first test case, got %d", len(repo.testCases))
 }
}

func TestVersionServicePublishDraftRequiresPublishableVersion(t *testing.T) {
 repo := newFakeVersionRepo()
 svc := NewVersionService(repo, zap.NewNop())
 view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
  Name:           "投诉分类",
  Goal:           "判断客户投诉类型",
  WhenToUse:      "用户表达投诉时",
  SampleInput:    "快递没更新",
  ExpectedOutput: "物流问题",
 })
 if err != nil {
  t.Fatal(err)
 }
 draft := repo.versions[view.Draft.ID]
 draft.ToolContract.Confirmed = false
 repo.versions[view.Draft.ID] = draft

 if _, err := svc.PublishDraft(context.Background(), view.Skill.ID); err == nil {
  t.Fatal("expected unconfirmed contract to block publish")
 }
}

type fakeVersionRepo struct {
 skills    map[string]SkillProduct
 versions  map[string]domain.SkillVersion
 testCases map[string]SkillTestCase
}

func newFakeVersionRepo() *fakeVersionRepo {
 return &fakeVersionRepo{
  skills:    map[string]SkillProduct{},
  versions:  map[string]domain.SkillVersion{},
  testCases: map[string]SkillTestCase{},
 }
}
```

Complete the fake methods as required by the new port.

- [x] **Step 2: Run test to verify RED**

Run: `go test ./internal/skill/application -run 'TestVersionService' -count=1`

Expected: FAIL because `VersionService` and types are undefined.

- [x] **Step 3: Add repository port**

Create `internal/skill/domain/port/version_repository.go`:

```go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillProductRow struct {
 ID              string
 Name            string
 Description     string
 Status          string
 ActiveVersionID string
 DraftVersionID  string
}

type SkillTestCaseRow struct {
 ID             string
 SkillID        string
 Name           string
 Input          any
 ExpectedOutput any
 AssertionMode  string
 Enabled        bool
}

type VersionRepo interface {
 InsertSkillWithDraft(ctx context.Context, skill SkillProductRow, draft domain.SkillVersion, firstCase SkillTestCaseRow) error
 GetSkill(ctx context.Context, skillID string) (SkillProductRow, bool, error)
 GetDraftVersion(ctx context.Context, skillID string) (domain.SkillVersion, bool, error)
 CountEnabledTestCases(ctx context.Context, skillID string) (int, error)
 PublishDraft(ctx context.Context, skillID, draftVersionID string, nextVersionNo int, baseline map[string]any) (domain.SkillVersion, error)
 NextVersionNo(ctx context.Context, skillID string) (int, error)
}
```

- [x] **Step 4: Add minimal VersionService**

Create `internal/skill/application/version_service.go` with:

```go
package application

import (
 "context"
 "regexp"
 "strings"

 "github.com/byteBuilderX/stratum/internal/skill/domain"
 "github.com/byteBuilderX/stratum/internal/skill/domain/port"
 "github.com/google/uuid"
 "go.uber.org/zap"
)

type SkillProduct = port.SkillProductRow
type SkillTestCase = port.SkillTestCaseRow

type CreateSkillDraftInput struct {
 Name           string
 Goal           string
 WhenToUse      string
 SampleInput    any
 ExpectedOutput any
}

type SkillWorkspaceView struct {
 Skill SkillProduct
 Draft domain.SkillVersion
}

type VersionService struct {
 repo   port.VersionRepo
 logger *zap.Logger
}

func NewVersionService(repo port.VersionRepo, logger *zap.Logger) *VersionService {
 return &VersionService{repo: repo, logger: logger}
}

func (s *VersionService) CreateSkillDraft(ctx context.Context, in CreateSkillDraftInput) (SkillWorkspaceView, error) {
 skillID := uuid.Must(uuid.NewV7()).String()
 draftID := uuid.Must(uuid.NewV7()).String()
 toolName := generatedToolName(in.Name)
 draft := domain.SkillVersion{
  ID:      draftID,
  SkillID: skillID,
  Status:  domain.VersionStatusDraft,
  Capability: domain.Capability{
   Goal:      in.Goal,
   WhenToUse: in.WhenToUse,
   Examples:  []domain.CapabilityExample{{Input: in.SampleInput, ExpectedOutput: in.ExpectedOutput}},
  },
  ToolContract: domain.ToolContract{
   ToolName:     toolName,
   Description:  strings.TrimSpace(in.WhenToUse + "，" + in.Goal),
   InputSchema:  map[string]any{"type": "object"},
   OutputSchema: map[string]any{"type": "object"},
   Confirmed:    false,
  },
  Implementation: domain.Implementation{
   Mode: "prompt",
   Source: map[string]any{
    "promptTemplate": in.Goal + "\n\n输入：{{.input}}",
   },
  },
 }
 skill := port.SkillProductRow{
  ID:             skillID,
  Name:           in.Name,
  Description:    in.Goal,
  Status:         "draft",
  DraftVersionID: draftID,
 }
 firstCase := port.SkillTestCaseRow{
  ID:             uuid.Must(uuid.NewV7()).String(),
  SkillID:        skillID,
  Name:           "初始样例",
  Input:          in.SampleInput,
  ExpectedOutput: in.ExpectedOutput,
  AssertionMode:  "contains",
  Enabled:        true,
 }
 if err := s.repo.InsertSkillWithDraft(ctx, skill, draft, firstCase); err != nil {
  return SkillWorkspaceView{}, err
 }
 return SkillWorkspaceView{Skill: skill, Draft: draft}, nil
}

func (s *VersionService) PublishDraft(ctx context.Context, skillID string) (domain.SkillVersion, error) {
 draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
 if err != nil {
  return domain.SkillVersion{}, err
 }
 if !ok {
  return domain.SkillVersion{}, domain.ErrSkillNotFound
 }
 testCount, err := s.repo.CountEnabledTestCases(ctx, skillID)
 if err != nil {
  return domain.SkillVersion{}, err
 }
 if err := draft.ValidatePublishable(testCount); err != nil {
  return domain.SkillVersion{}, err
 }
 next, err := s.repo.NextVersionNo(ctx, skillID)
 if err != nil {
  return domain.SkillVersion{}, err
 }
 return s.repo.PublishDraft(ctx, skillID, draft.ID, next, map[string]any{"enabled_test_count": testCount})
}

var nonToolName = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func generatedToolName(name string) string {
 out := strings.ToLower(nonToolName.ReplaceAllString(name, "_"))
 out = strings.Trim(out, "_")
 if out == "" || (out[0] >= '0' && out[0] <= '9') {
  out = "skill_" + out
 }
 if len(out) > 64 {
  out = out[:64]
 }
 return out
}
```

- [x] **Step 5: Finish fake repo and run tests**

Run: `go test ./internal/skill/application -run 'TestVersionService' -count=1`

Expected: PASS.

---

## Task 4: Agent Tool Contract Resolver

**Files:**

- Modify: `internal/agent/domain/port/capability.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent_service_test.go`

- [x] **Step 1: Write failing test for real contract schema**

In `internal/agent/application/agent_service_test.go`, add a test using a fake resolver:

```go
func TestBuildExtraToolsUsesSkillToolResolverContracts(t *testing.T) {
 resolver := &fakeSkillToolResolver{
  tools: []port.ToolDefinition{{
   Name:        "tenant_t1_classify_complaint",
   Description: "判断客户投诉类型",
   InputSchema: map[string]any{
    "type": "object",
    "properties": map[string]any{
     "complaintText": map[string]any{"type": "string"},
    },
    "required": []string{"complaintText"},
   },
  }},
  index: map[string]port.SkillToolRef{
   "tenant_t1_classify_complaint": {SkillID: "skill-1", VersionID: "version-1"},
  },
 }
 svc := &AgentService{deps: AgentServiceDeps{SkillToolResolver: resolver}}

 tools, index := svc.buildExtraTools(context.Background(), "t1", nil, []string{"skill-1"})

 if len(tools) != 1 {
  t.Fatalf("expected one tool, got %d", len(tools))
 }
 props := tools[0].InputSchema["properties"].(map[string]any)
 if _, ok := props["complaintText"]; !ok {
  t.Fatalf("expected resolver schema, got %#v", tools[0].InputSchema)
 }
 if index["tenant_t1_classify_complaint"].VersionID != "version-1" {
  t.Fatalf("expected version index, got %#v", index)
 }
}
```

- [x] **Step 2: Run test to verify RED**

Run: `go test ./internal/agent/application -run TestBuildExtraToolsUsesSkillToolResolverContracts -count=1`

Expected: FAIL because resolver/index types do not exist.

- [x] **Step 3: Add port types**

In `internal/agent/domain/port/capability.go`, add:

```go
type SkillToolRef struct {
 SkillID   string
 VersionID string
}

type SkillToolResolver interface {
 ResolveTools(ctx context.Context, tenantID string, skillIDs []string) ([]ToolDefinition, map[string]SkillToolRef, error)
}
```

Change `SkillCapRequest` to include:

```go
VersionID string
```

- [x] **Step 4: Update AgentService deps and buildExtraTools**

Add `SkillToolResolver port.SkillToolResolver` to `AgentServiceDeps`.

Change `buildExtraTools` return type:

```go
func (s *AgentService) buildExtraTools(ctx context.Context, tenantID string, mcpServerIDs, allowedSkills []string) ([]port.ToolDefinition, map[string]port.SkillToolRef)
```

Use resolver first:

```go
if s.deps.SkillToolResolver != nil && len(allowedSkills) > 0 {
    resolvedTools, resolvedIndex, err := s.deps.SkillToolResolver.ResolveTools(ctx, tenantID, allowedSkills)
    if err == nil {
        tools = append(tools, resolvedTools...)
        for name, ref := range resolvedIndex {
            index[name] = ref
        }
        return tools, index
    }
}
```

Keep legacy fallback:

```go
index[toolName] = port.SkillToolRef{SkillID: skillID}
```

- [x] **Step 5: Run agent application tests**

Run: `go test ./internal/agent/application -run 'TestBuildExtraTools|TestAgent' -count=1`

Expected: PASS after updating compile errors.

---

## Task 5: ReAct Tool Node Carries Version ID

**Files:**

- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_test.go`
- Modify: `internal/agent/domain/port/capability.go`

- [x] **Step 1: Write failing graph test**

Add a graph test asserting Skill `CapabilityRequest.Skill.VersionID` is set when `SkillToolIndex` contains a versioned ref.

Use existing fake `CapabilityGateway` pattern in `react_test.go`, and assert:

```go
if got.Skill.VersionID != "version-1" {
    t.Fatalf("expected version-1, got %q", got.Skill.VersionID)
}
```

- [x] **Step 2: Run test to verify RED**

Run: `go test ./internal/agent/application/graph -run TestReActToolNodePassesSkillVersionID -count=1`

Expected: FAIL.

- [x] **Step 3: Update ReActState and tool node**

Change:

```go
SkillToolIndex map[string]string
```

to:

```go
SkillToolIndex map[string]port.SkillToolRef
```

In tool execution:

```go
ref, ok := s.SkillToolIndex[tc.Name]
...
Skill: &port.SkillCapRequest{SkillID: ref.SkillID, VersionID: ref.VersionID, Input: tc.Arguments}
```

- [x] **Step 4: Run graph tests**

Run: `go test ./internal/agent/application/graph -count=1`

Expected: PASS.

---

## Task 6: Version-Aware Skill Gateway Execution

**Files:**

- Modify: `internal/skill/infrastructure/gateway/providers/registry_adapter.go`
- Add tests in existing provider or gateway test files.

- [x] **Step 1: Write failing provider test**

Add a test that builds a fake tenant DB row in `skill_versions` and executes with a version ID. If no DB integration helper exists, write the test at the adapter boundary with a fake loader extracted in Step 3.

Expected behavior:

```text
VersionID present → load implementation from skill_versions
VersionID empty   → fallback to legacy skills table
```

- [x] **Step 2: Run test to verify RED**

Run targeted provider tests.

- [x] **Step 3: Refactor DBSkillAdapter**

Add method:

```go
func (a *DBSkillAdapter) ExecuteVersion(ctx context.Context, versionID string, input any) (any, error)
```

Or extend `Execute` input metadata through gateway request if the provider interface is kept stable.

Implementation should:

```text
SELECT skill_id, implementation FROM skill_versions WHERE id=$1 AND status IN ('draft','published')
build skill executor from implementation.mode/source
execute with input
```

Keep legacy execution path unchanged when version is absent.

- [x] **Step 4: Run skill gateway tests**

Run: `go test ./internal/skill/infrastructure/gateway/... -count=1`

Expected: PASS.

---

## Task 7: HTTP Draft/Version API Skeleton

**Files:**

- Modify: `api/http/dto/request.go`
- Create or modify: `api/http/dto/skill_response.go`
- Modify: `api/http/handler/skill_handler.go`
- Modify: `api/http/router.go`
- Modify: `api/http/handler/skill_handler_test.go`
- Add: `api/http/testdata/contracts/post_skills__id_publish.golden.json`

- [x] **Step 1: Write failing handler test**

Add handler tests for:

```text
POST /skills creates Skill workspace draft
POST /skills/:id/publish rejects unconfirmed contract
```

Use fake `VersionService` interface injected into `SkillHandler` or a separate `SkillWorkspaceHandler`.

- [x] **Step 2: Run handler tests to verify RED**

Run: `go test ./api/http/handler -run 'Skill.*Draft|Skill.*Publish' -count=1`

Expected: FAIL.

- [x] **Step 3: Add handler thin methods**

Add DTOs:

```go
type CreateSkillDraftRequest struct {
 Name           string `json:"name" binding:"required"`
 Goal           string `json:"goal" binding:"required"`
 WhenToUse      string `json:"whenToUse" binding:"required"`
 SampleInput    any    `json:"sampleInput" binding:"required"`
 ExpectedOutput any    `json:"expectedOutput" binding:"required"`
}
```

Handler should bind, call application, render response. Keep ≤15 lines per method by using helper mappers.

- [x] **Step 4: Register routes**

In `api/http/router.go`:

```go
skills.POST("/:id/publish", requireActive, skillHandler.PublishSkill)
```

Do not remove legacy routes yet.

- [x] **Step 5: Run handler and contract tests**

Run:

```bash
go test ./api/http/handler -count=1
go test ./api/http -run TestContracts -count=1
```

Expected: PASS.

---

## Task 8: Frontend Skill Workspace First Slice

**Files:**

- Modify: `web/src/modules/skill/model/skill.ts`
- Modify: `web/src/modules/skill/api/skill.api.ts`
- Replace or add pages under `web/src/modules/skill/pages/`
- Add tests under `web/src/modules/skill/model/__tests__/`

- [x] **Step 1: Write model tests**

Add tests for:

```text
buildCreateSkillDraftPayload
skill workspace response schema parses active/draft versions
```

- [x] **Step 2: Run frontend model tests to verify RED**

Run: `npm --prefix web test -- src/modules/skill/model/__tests__/skill.test.ts`

Expected: FAIL.

- [x] **Step 3: Add model/API support**

Add types:

```ts
Capability
ToolContract
Implementation
SkillVersion
SkillWorkspace
```

Add API methods:

```ts
createDraft
getWorkspace
updateCapability
updateContract
updateImplementation
publish
```

- [x] **Step 4: Replace create page with minimal capability form**

Create page should only collect:

```text
name
goal
whenToUse
sampleInput
expectedOutput
```

After success navigate to `/skills/:id/workspace`.

- [x] **Step 5: Add workspace tabs skeleton**

Add tabs:

```text
能力
契约
实现
测试
版本
```

First slice can render draft values read-only except capability edit.

- [x] **Step 6: Run frontend tests/lint**

Run:

```bash
npm --prefix web test -- src/modules/skill/model/__tests__/skill.test.ts
npm --prefix web run lint -- src/modules/skill/model/skill.ts src/modules/skill/api/skill.api.ts src/modules/skill/pages/CreateSkillPage.tsx
```

Expected: PASS.

---

## Verification

Before claiming implementation complete, run:

```bash
go test ./api/http ./api/http/handler ./internal/skill/... ./internal/agent/application/... ./pkg/storage/postgres -count=1
npm --prefix web test -- src/modules/skill/model/__tests__/skill.test.ts
npm --prefix web run lint -- src/modules/skill/model/skill.ts src/modules/skill/model/__tests__/skill.test.ts src/modules/skill/api/skill.api.ts src/modules/skill/pages/CreateSkillPage.tsx
npm --prefix web run typecheck
```

Known current issue: `npm --prefix web run typecheck` may fail on `tsconfig.json(25,27): Invalid value for '--ignoreDeprecations'`. Report this separately if unchanged.

---

## Self-Review

Spec coverage:

- Data model: Tasks 1-3.
- Agent loop collaboration: Tasks 4-5.
- Gateway version execution: Task 6.
- API surface: Task 7.
- Frontend workspace: Task 8.

Intentional phase deferrals:

- Full generated implementation via LLM is not included in this plan.
- Agent trial implementation is documented but not fully implemented until versioned resolver and gateway execution are stable.
- `agent_skill_bindings` table is deferred; existing `AllowedSkills` remains the first binding mechanism.
