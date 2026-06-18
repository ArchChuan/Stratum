# Agent Handler DDD 重构实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除 agent_handler.go / agent_crud_handler.go / agent_exec_handler.go 三个文件的全部架构违规(pgxpool 直持、tenantdb 引用、raw SQL、业务编排)，handler 退化为纯 transport(bind → svc → render)，业务下沉到 application.AgentService。

**Architecture:** 新增 6 个 domain/port 接口(TenantSettingsReader / SkillMetadataRepo / TenantGatewayProvider 已有，新增 CapabilityGatewayFactory / RAGSearchProvider / ExecutionRecorder)+ 1 个 AgentService 聚合 CRUD+Execute+Stream；handler 持单一 AgentService 引用；wiring 层实现 port adapter。

**Tech Stack:** Go 1.22+ · pgx v5 · Gin v1.9 · 现有 DDD 分层(internal/agent/{domain,application,infrastructure})· testify/mock · golangci-lint depguard

## 全局约束

- Go 1.22+，stdlib only in domain/，application/ 禁 pgx/redis/milvus 直引
- handler/ 禁 import `internal/*/infrastructure`、`pkg/tenantdb`、`pkg/storage/postgres`、`github.com/jackc/pgx*`
- 方法长度 ≤30 行(handler)、≤50 行(application service)
- 测试覆盖率 ≥80%，全部 `-race` 跑通
- 错误 `c.Error(err)` 交 middleware，禁内联 `c.JSON(http.StatusXxx, dto.ErrorResponse{})`
- 提交信息格式 `feat(agent): <description>` / `refactor(handler): <description>`

---

### Task 1: 新增 domain/port 接口定义

**Files:**

- Create: `internal/agent/domain/port/tenant_settings.go`
- Create: `internal/agent/domain/port/skill_metadata.go`
- Create: `internal/agent/domain/port/rag_search.go`
- Modify: `internal/agent/domain/port/capability.go:1-100`(已有,补充注释)

**Interfaces:**

- Consumes: 无(首个 task)
- Produces:

  ```go
  type TenantSettingsReader interface {
      GetEmbedModel(ctx context.Context, tenantID string) (string, error)
  }
  type SkillMetadataRepo interface {
      GetNameAndDesc(ctx context.Context, skillID string) (name, desc string, err error)
  }
  type RAGSearchProvider interface {
      SearchKnowledge(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int) (string, error)
  }
  ```

- [ ] **Step 1: 写 TenantSettingsReader 接口测试(consumer-side port 风格)**

```go
// internal/agent/domain/port/tenant_settings_test.go
package port_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/stretchr/testify/assert"
)

// mockTenantSettingsReader 是测试桩，验证 port 契约可被 mock
type mockTenantSettingsReader struct {
 embedModel string
 err        error
}

func (m *mockTenantSettingsReader) GetEmbedModel(ctx context.Context, tenantID string) (string, error) {
 return m.embedModel, m.err
}

func TestTenantSettingsReader_Interface(t *testing.T) {
 var _ port.TenantSettingsReader = (*mockTenantSettingsReader)(nil)

 m := &mockTenantSettingsReader{embedModel: "text-embedding-3-small", err: nil}
 model, err := m.GetEmbedModel(context.Background(), "tenant-123")

 assert.NoError(t, err)
 assert.Equal(t, "text-embedding-3-small", model)
}
```

- [ ] **Step 2: 跑测试验证失败(接口未定义)**

```bash
cd /home/yang/go-projects/stratum
go test ./internal/agent/domain/port/... -v -run TestTenantSettingsReader
```

预期: `undefined: port.TenantSettingsReader`

- [ ] **Step 3: 写 TenantSettingsReader 接口定义**

```go
// internal/agent/domain/port/tenant_settings.go
package port

import "context"

// TenantSettingsReader 是消费者侧 port，用于 agent application 查询
// 租户级配置(embed_model 等)，无需 import iam infrastructure。
// 实现位于 api/wiring/agent.go 的 thin adapter。
type TenantSettingsReader interface {
 // GetEmbedModel 返回租户默认 embed_model；不存在时返回 ("", nil)。
 GetEmbedModel(ctx context.Context, tenantID string) (string, error)
}
```

- [ ] **Step 4: 写 SkillMetadataRepo + RAGSearchProvider 接口与测试**

```go
// internal/agent/domain/port/skill_metadata.go
package port

import "context"

// SkillMetadataRepo 提供技能元数据(name/description)用于构建 ToolDefinition，
// 避免 handler 直接查 tenant schema。实现位于 api/wiring/agent.go。
type SkillMetadataRepo interface {
 // GetNameAndDesc 查询 tenant schema 的 skills 表；不存在返回 ("", "", ErrSkillNotFound)。
 GetNameAndDesc(ctx context.Context, skillID string) (name, desc string, err error)
}

// internal/agent/domain/port/rag_search.go
package port

import "context"

// RAGSearchProvider 封装知识库检索能力，agent application 调用此 port
// 而非直接 import knowledge/application。实现位于 api/wiring/agent.go。
type RAGSearchProvider interface {
 // SearchKnowledge 在指定 workspaces 内检索 query，返回拼接后的上下文字符串。
 SearchKnowledge(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int) (string, error)
}
```

测试文件同 Step 1 模式，每个接口一个 `*_test.go`，mock 实现 + interface 类型断言。

- [ ] **Step 5: 跑全部 port 测试验证通过**

```bash
go test ./internal/agent/domain/port/... -v
```

预期: PASS (3 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/domain/port/tenant_settings.go \
        internal/agent/domain/port/tenant_settings_test.go \
        internal/agent/domain/port/skill_metadata.go \
        internal/agent/domain/port/skill_metadata_test.go \
        internal/agent/domain/port/rag_search.go \
        internal/agent/domain/port/rag_search_test.go
git commit -m "feat(agent): add consumer-side ports for tenant settings, skill metadata, RAG search"
```

---

### Task 2: 新增 AgentService 聚合层

**Files:**

- Create: `internal/agent/application/agent_service.go`
- Create: `internal/agent/application/agent_service_test.go`

**Interfaces:**

- Consumes: `port.TenantSettingsReader`, `port.SkillMetadataRepo`, `port.RAGSearchProvider`, `port.TenantCapabilityResolver`(已有), `*Registry`(已有), `ExecutionStore`(已有)
- Produces:

  ```go
  type AgentService struct { /* 聚合 CRUD + Execute */ }
  func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error)
  func (s *AgentService) Update(ctx context.Context, id string, in UpdateAgentInput) (AgentDTO, error)
  func (s *AgentService) Get(ctx context.Context, id string) (AgentDTO, error)
  func (s *AgentService) List(ctx context.Context) ([]AgentDTO, error)
  func (s *AgentService) Delete(ctx context.Context, id string) error
  func (s *AgentService) Execute(ctx context.Context, id, query string, opts ExecOptions) (ExecutionResult, error)
  func (s *AgentService) ExecuteStream(ctx context.Context, id, query string, opts ExecOptions, tokenCb func(string)) (ExecutionResult, error)
  func (s *AgentService) ListExecutions(ctx context.Context, page, pageSize int) ([]ExecutionDTO, int64, error)
  ```

- [ ] **Step 1: 写 AgentService.Create 失败测试**

```go
// internal/agent/application/agent_service_test.go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/application"
 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/mock"
 "go.uber.org/zap"
)

type mockTenantSettingsReader struct{ mock.Mock }
func (m *mockTenantSettingsReader) GetEmbedModel(ctx context.Context, tid string) (string, error) {
 args := m.Called(ctx, tid)
 return args.String(0), args.Error(1)
}

type mockRegistry struct{ mock.Mock }
func (m *mockRegistry) Register(ctx context.Context, a application.Agent) error {
 return m.Called(ctx, a).Error(0)
}
func (m *mockRegistry) Get(ctx context.Context, id string) (application.Agent, bool) {
 args := m.Called(ctx, id)
 if a := args.Get(0); a != nil {
  return a.(application.Agent), args.Bool(1)
 }
 return nil, false
}
func (m *mockRegistry) GetAll(ctx context.Context) []application.Agent {
 args := m.Called(ctx)
 return args.Get(0).([]application.Agent)
}
func (m *mockRegistry) Remove(ctx context.Context, id string) error {
 return m.Called(ctx, id).Error(0)
}
func (m *mockRegistry) Update(ctx context.Context, cfg *domain.AgentConfig) error {
 return m.Called(ctx, cfg).Error(0)
}

func TestAgentService_Create(t *testing.T) {
 logger := zap.NewNop()
 tsReader := new(mockTenantSettingsReader)
 reg := new(mockRegistry)

 tsReader.On("GetEmbedModel", mock.Anything, "tenant-1").Return("text-embedding-ada-002", nil)
 reg.On("Register", mock.Anything, mock.Anything).Return(nil)

 svc := application.NewAgentService(reg, tsReader, nil, nil, nil, nil, logger)

 in := application.CreateAgentInput{
  TenantID:      "tenant-1",
  Name:          "TestAgent",
  LLMModel:      "gpt-4",
  MaxIterations: 10,
 }

 dto, err := svc.Create(context.Background(), in)

 assert.NoError(t, err)
 assert.Equal(t, "TestAgent", dto.Name)
 assert.Equal(t, "text-embedding-ada-002", dto.EmbedModel)
 tsReader.AssertExpectations(t)
 reg.AssertExpectations(t)
}
```

- [ ] **Step 2: 跑测试验证失败**

```bash
go test ./internal/agent/application/... -v -run TestAgentService_Create
```

预期: `undefined: application.NewAgentService`

- [ ] **Step 3: 实现 AgentService.Create**

```go
// internal/agent/application/agent_service.go
package application

import (
 "context"
 "fmt"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/google/uuid"
 "go.uber.org/zap"
)

type AgentService struct {
 registry       *Registry
 tenantSettings port.TenantSettingsReader
 skillMeta      port.SkillMetadataRepo
 ragSearch      port.RAGSearchProvider
 tenantResolver port.TenantCapabilityResolver
 execStore      ExecutionStore
 logger         *zap.Logger
}

func NewAgentService(
 reg *Registry,
 ts port.TenantSettingsReader,
 sm port.SkillMetadataRepo,
 rs port.RAGSearchProvider,
 tr port.TenantCapabilityResolver,
 es ExecutionStore,
 logger *zap.Logger,
) *AgentService {
 return &AgentService{
  registry:       reg,
  tenantSettings: ts,
  skillMeta:      sm,
  ragSearch:      rs,
  tenantResolver: tr,
  execStore:      es,
  logger:         logger,
 }
}

type CreateAgentInput struct {
 TenantID              string
 Name                  string
 Type                  string
 Description           string
 Persona               string
 SystemPrompt          string
 LLMModel              string
 EmbedModel            string // 可选，空时继承租户默认
 MaxIterations         int
 MaxContextTokens      int
 AllowedSkills         []string
 MCPServerIDs          []string
 KnowledgeWorkspaceIDs []string
}

type AgentDTO struct {
 ID                    string
 Name                  string
 Type                  string
 Description           string
 Persona               string
 SystemPrompt          string
 LLMModel              string
 EmbedModel            string
 MaxIterations         int
 MaxContextTokens      int
 AllowedSkills         []string
 MCPServerIDs          []string
 KnowledgeWorkspaceIDs []string
 CreatedAt             string
}

func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error) {
 embedModel := in.EmbedModel
 if embedModel == "" && s.tenantSettings != nil {
  inherited, err := s.tenantSettings.GetEmbedModel(ctx, in.TenantID)
  if err != nil {
   return AgentDTO{}, fmt.Errorf("agent service: get embed_model: %w", err)
  }
  embedModel = inherited
 }

 id := uuid.New().String()
 agentType := parseAgentType(in.Type)

 cfg := &domain.AgentConfig{
  ID:                    id,
  Name:                  in.Name,
  Type:                  agentType,
  Description:           in.Description,
  Persona:               in.Persona,
  SystemPrompt:          in.SystemPrompt,
  LLMModel:              in.LLMModel,
  EmbedModel:            embedModel,
  MaxIterations:         in.MaxIterations,
  MaxContextTokens:      in.MaxContextTokens,
  AllowedSkills:         in.AllowedSkills,
  MCPServerIDs:          in.MCPServerIDs,
  KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
  Capabilities:          []domain.AgentCapability{},
 }

 a := NewBaseAgent(cfg, s.logger)
 if err := s.registry.Register(ctx, a); err != nil {
  return AgentDTO{}, fmt.Errorf("agent service: register: %w", err)
 }

 s.logger.Info("agent created", zap.String("id", id), zap.String("name", in.Name))

 return AgentDTO{
  ID:                    id,
  Name:                  in.Name,
  Type:                  in.Type,
  Description:           in.Description,
  Persona:               in.Persona,
  SystemPrompt:          in.SystemPrompt,
  LLMModel:              in.LLMModel,
  EmbedModel:            embedModel,
  MaxIterations:         in.MaxIterations,
  MaxContextTokens:      in.MaxContextTokens,
  AllowedSkills:         in.AllowedSkills,
  MCPServerIDs:          in.MCPServerIDs,
  KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
 }, nil
}

func parseAgentType(t string) domain.AgentType {
 switch t {
 case "react":
  return domain.ReActAgent
 case "cot":
  return domain.CoTAgent
 case "planning":
  return domain.PlanningAgent
 case "tool_calling":
  return domain.ToolCallingAgent
 case "rag":
  return domain.RAGAgent
 case "swarm":
  return domain.SwarmAgent
 default:
  return domain.ReActAgent
 }
}
```

- [ ] **Step 4: 跑测试验证通过**

```bash
go test ./internal/agent/application/... -v -run TestAgentService_Create
```

预期: PASS

- [ ] **Step 5: 实现 AgentService.{Get, List, Update, Delete}**

补充 agent_service.go 内其余 CRUD 方法(每个 ≤20 行)，复用 `s.registry.*` 调用。测试 agent_service_test.go 同步新增。

- [ ] **Step 6: 跑全部 CRUD 测试**

```bash
go test ./internal/agent/application/... -v -run TestAgentService
```

预期: PASS (5 CRUD tests)

- [ ] **Step 7: Commit**

```bash
git add internal/agent/application/agent_service.go \
        internal/agent/application/agent_service_test.go
git commit -m "feat(agent): add AgentService CRUD methods with tenant settings inheritance"
```

---

### Task 3: AgentService.Execute + ExecuteStream(下沉 assembleOptions/recordExecution)

**Files:**

- Modify: `internal/agent/application/agent_service.go`(append)
- Modify: `internal/agent/application/agent_service_test.go`(append)

**Interfaces:**

- Consumes: Task 2 的 AgentService、`port.TenantCapabilityResolver`(已有)、`port.MCPToolProvider`(已有)
- Produces:

  ```go
  type ExecOptions struct {
      TenantID, UserID, TraceID, ConversationID string
      MaxStepsOverride int
      TimeoutOverride  time.Duration
  }
  type ExecutionResult struct {
      Output     string
      Steps      int
      TokensUsed int
      Duration   time.Duration
      Thoughts   []Thought
      ToolCalls  []ToolCall
  }
  func (s *AgentService) Execute(ctx, id, query string, opts ExecOptions) (ExecutionResult, error)
  func (s *AgentService) ExecuteStream(ctx, id, query string, opts ExecOptions, tokenCb func(string)) (ExecutionResult, error)
  ```

- [ ] **Step 1: 写 Execute 失败路径测试(agent 不存在 → 返回 ErrNotFound)**

```go
func TestAgentService_Execute_NotFound(t *testing.T) {
 logger := zap.NewNop()
 reg := new(mockRegistry)
 reg.On("Get", mock.Anything, "missing").Return(nil, false)

 svc := application.NewAgentService(reg, nil, nil, nil, nil, nil, logger)
 _, err := svc.Execute(context.Background(), "missing", "hello", application.ExecOptions{TenantID: "t1"})

 assert.ErrorIs(t, err, application.ErrNotFound)
}
```

- [ ] **Step 2: 跑测试验证失败**

```bash
go test ./internal/agent/application/... -v -run TestAgentService_Execute_NotFound
```

预期: `undefined: AgentService.Execute`

- [ ] **Step 3: 实现 Execute(下沉原 handler.assembleOptions 全部逻辑)**

```go
// agent_service.go (append)
type ExecOptions struct {
 TenantID         string
 UserID           string
 TraceID          string
 ConversationID   string
 MaxStepsOverride int
 TimeoutOverride  time.Duration
}

type ExecutionResult struct {
 Output     string
 Steps      int
 TokensUsed int
 Duration   time.Duration
 Thoughts   []Thought
 ToolCalls  []ToolCall
}

func (s *AgentService) Execute(ctx context.Context, id, query string, opts ExecOptions) (ExecutionResult, error) {
 a, ok := s.registry.Get(ctx, id)
 if !ok {
  return ExecutionResult{}, ErrNotFound
 }

 execOpts, _ := s.assembleOptions(ctx, a, opts, false, nil)
 timeout := opts.TimeoutOverride
 if timeout == 0 {
  timeout = constants.AgentExecTimeout
 }
 execCtx, cancel := context.WithTimeout(ctx, timeout)
 defer cancel()

 start := time.Now()
 result, err := a.Execute(execCtx, query, execOpts...)
 durationMs := int(time.Since(start).Milliseconds())

 s.recordExecution(ctx, id, opts.UserID, a.GetConfig().Name, query, result, err, durationMs)

 if err != nil {
  s.logger.Error("agent execution failed", zap.String("agentId", id), zap.Error(err))
  return ExecutionResult{}, err
 }
 return ExecutionResult{
  Output:     result.Output,
  Steps:      result.Steps,
  TokensUsed: result.TokensUsed,
  Duration:   result.Duration,
  Thoughts:   result.Thoughts,
  ToolCalls:  result.ToolCalls,
 }, nil
}

func (s *AgentService) assembleOptions(
 ctx context.Context, a Agent, opts ExecOptions, stream bool, tokenCb func(string),
) ([]ExecutionOption, context.Context) {
 maxSteps := a.GetConfig().MaxIterations
 if opts.MaxStepsOverride > 0 {
  maxSteps = opts.MaxStepsOverride
 }
 out := []ExecutionOption{WithMaxSteps(maxSteps)}

 if s.tenantResolver != nil {
  if capGW, apiKeys, ok := s.tenantResolver.Resolve(ctx, opts.TenantID); ok {
   if stream {
    ctx = s.tenantResolver.InjectCompleter(ctx, opts.TenantID)
   }
   type capGWSetter interface {
    SetCapGateway(port.CapabilityGateway)
   }
   if setter, ok := a.(capGWSetter); ok {
    setter.SetCapGateway(capGW)
   }
   if len(apiKeys) > 0 {
    out = append(out, WithLLMAPIKeys(apiKeys))
   }
  }
 }

 out = append(out,
  WithTenantID(opts.TenantID),
  WithTraceID(opts.TraceID),
  WithUserID(opts.UserID),
 )
 if opts.ConversationID != "" {
  out = append(out, WithConversationID(opts.ConversationID), WithHistoryWindow(20))
 }
 out = append(out, WithExtraTools(s.buildExtraTools(ctx, a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills)))
 if s.ragSearch != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
  out = append(out, WithRAGSearchFn(s.ragSearchAdapter(opts.TenantID)))
 }
 if stream && tokenCb != nil {
  out = append(out, WithTokenCallback(tokenCb))
 }
 return out, ctx
}

func (s *AgentService) ragSearchAdapter(tenantID string) func(context.Context, []string, string, int) (string, error) {
 return func(ctx context.Context, ws []string, q string, topK int) (string, error) {
  return s.ragSearch.SearchKnowledge(ctx, tenantID, ws, q, topK)
 }
}
```

- [ ] **Step 4: 实现 buildExtraTools / recordExecution 内部 helper**

```go
func (s *AgentService) buildExtraTools(ctx context.Context, mcpIDs, skillIDs []string) []port.ToolDefinition {
 var tools []port.ToolDefinition
 if s.mcpToolProvider != nil {
  for _, sid := range mcpIDs {
   tools = append(tools, s.mcpToolProvider.ToolsForServer(ctx, sid)...)
  }
 }
 for _, sid := range skillIDs {
  name, desc := sid, sid
  if s.skillMeta != nil {
   if n, d, err := s.skillMeta.GetNameAndDesc(ctx, sid); err == nil {
    name, desc = n, d
   }
  }
  tools = append(tools, port.ToolDefinition{
   Name:        sid,
   Description: name + ": " + desc,
   InputSchema: map[string]any{"type": "object"},
  })
 }
 return tools
}

func (s *AgentService) recordExecution(
 ctx context.Context, id, userID, agentName, query string,
 result *AgentResult, err error, durationMs int,
) {
 if s.execStore == nil {
  return
 }
 rec := ExecutionRecord{
  AgentID:      id,
  UserID:       userID,
  AgentName:    agentName,
  InputPreview: truncate(query, previewMaxChars),
  DurationMs:   durationMs,
 }
 if err != nil {
  rec.Status = "error"
  rec.ErrorMessage = err.Error()
 } else {
  rec.Status = "success"
  rec.OutputPreview = truncate(result.Output, previewMaxChars)
  rec.TotalTokens = result.TokensUsed
 }
 // ctx 已携带 tenant 信息(中间件注入)，store 实现内部从 ctx 解析。
 go func() {
  if e := s.execStore.Insert(ctx, rec); e != nil {
   s.logger.Warn("execution record insert failed", zap.Error(e))
  }
 }()
}

const previewMaxChars = 50

func truncate(s string, n int) string {
 if len(s) <= n {
  return s
 }
 return s[:n]
}
```

注意:`recordExecution` 用传入的 `ctx`(handler 的 c.Request.Context())，不再 `context.Background() + tenantdb.WithTenant`。tenant 信息由 `Insert` 实现从 ctx 取(已具备能力)。

- [ ] **Step 5: 实现 ExecuteStream**

```go
func (s *AgentService) ExecuteStream(
 ctx context.Context, id, query string, opts ExecOptions, tokenCb func(string),
) (ExecutionResult, error) {
 a, ok := s.registry.Get(ctx, id)
 if !ok {
  return ExecutionResult{}, ErrNotFound
 }

 execOpts, streamCtx := s.assembleOptions(ctx, a, opts, true, tokenCb)
 timeout := opts.TimeoutOverride
 if timeout == 0 {
  timeout = constants.AgentExecTimeout
 }
 execCtx, cancel := context.WithTimeout(streamCtx, timeout)
 defer cancel()

 start := time.Now()
 result, err := a.Execute(execCtx, query, execOpts...)
 durationMs := int(time.Since(start).Milliseconds())

 s.recordExecution(ctx, id, opts.UserID, a.GetConfig().Name, query, result, err, durationMs)

 if err != nil {
  return ExecutionResult{}, err
 }
 return ExecutionResult{
  Output:     result.Output,
  Steps:      result.Steps,
  TokensUsed: result.TokensUsed,
  Duration:   result.Duration,
  Thoughts:   result.Thoughts,
  ToolCalls:  result.ToolCalls,
 }, nil
}
```

- [ ] **Step 6: 跑全部 service 测试**

```bash
go test ./internal/agent/application/... -v -race
```

预期: PASS (≥7 tests)

- [ ] **Step 7: Commit**

```bash
git add internal/agent/application/agent_service.go \
        internal/agent/application/agent_service_test.go
git commit -m "feat(agent): add Execute/ExecuteStream with assembleOptions logic moved from handler"
```

---

### Task 4: 新增 wiring adapter — TenantSettingsReader / SkillMetadataRepo / RAGSearchProvider

**Files:**

- Modify: `api/wiring/agent.go`(append)
- Create: `internal/skill/infrastructure/persistence/skill_meta_repo.go`
- Create: `internal/skill/infrastructure/persistence/skill_meta_repo_test.go`

**Interfaces:**

- Consumes: `port.TenantSettingsReader`, `port.SkillMetadataRepo`, `port.RAGSearchProvider`(Task 1 定义)
- Produces:

  ```go
  func wireTenantSettingsReader(tenantSvc *iamapp.TenantService) port.TenantSettingsReader
  func wireSkillMetadataRepo(pool *pgxpool.Pool) port.SkillMetadataRepo
  func wireRAGSearchProvider(rag *knowledgeapp.RAGService) port.RAGSearchProvider
  ```

- [ ] **Step 1: 写 SkillMetadataRepo Postgres 实现失败测试**

```go
// internal/skill/infrastructure/persistence/skill_meta_repo_test.go
package persistence_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/pashagolub/pgxmock/v3"
 "github.com/stretchr/testify/assert"
)

func TestSkillMetaRepo_GetNameAndDesc(t *testing.T) {
 mockPool, _ := pgxmock.NewPool()
 defer mockPool.Close()

 mockPool.ExpectQuery(`SELECT name, description FROM skills WHERE id=\$1`).
  WithArgs("skill-1").
  WillReturnRows(pgxmock.NewRows([]string{"name", "description"}).
   AddRow("calculator", "math tool"))

 repo := persistence.NewSkillMetaRepo(mockPool)
 ctx := postgres.WithTenant(context.Background(), postgres.TenantContext{TenantID: "t1"})

 name, desc, err := repo.GetNameAndDesc(ctx, "skill-1")
 assert.NoError(t, err)
 assert.Equal(t, "calculator", name)
 assert.Equal(t, "math tool", desc)
}
```

- [ ] **Step 2: 跑测试验证失败**

```bash
go test ./internal/skill/infrastructure/persistence/... -v -run TestSkillMetaRepo
```

预期: `undefined: persistence.NewSkillMetaRepo`

- [ ] **Step 3: 实现 SkillMetaRepo**

```go
// internal/skill/infrastructure/persistence/skill_meta_repo.go
package persistence

import (
 "context"
 "errors"
 "fmt"

 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/jackc/pgx/v5"
)

var ErrSkillNotFound = errors.New("skill: not found")

type SkillMetaRepo struct {
 pool postgres.PgxPool
}

func NewSkillMetaRepo(pool postgres.PgxPool) *SkillMetaRepo {
 return &SkillMetaRepo{pool: pool}
}

func (r *SkillMetaRepo) GetNameAndDesc(ctx context.Context, skillID string) (string, string, error) {
 tc, ok := postgres.FromContext(ctx)
 if !ok || tc.TenantID == "" {
  return "", "", fmt.Errorf("skill_meta: tenant context required")
 }
 schema := fmt.Sprintf(`"tenant_%s"`, tc.TenantID)
 q := fmt.Sprintf(`SELECT name, description FROM %s.skills WHERE id=$1`, schema)

 var name, desc string
 err := r.pool.QueryRow(ctx, q, skillID).Scan(&name, &desc)
 if errors.Is(err, pgx.ErrNoRows) {
  return "", "", ErrSkillNotFound
 }
 if err != nil {
  return "", "", fmt.Errorf("skill_meta: query: %w", err)
 }
 return name, desc, nil
}
```

- [ ] **Step 4: 跑 repo 测试通过**

```bash
go test ./internal/skill/infrastructure/persistence/... -v -race
```

预期: PASS

- [ ] **Step 5: 在 api/wiring/agent.go 添加三个 thin adapter**

```go
// api/wiring/agent.go (append)

// tenantSettingsAdapter 实现 port.TenantSettingsReader，调用 iam TenantService。
type tenantSettingsAdapter struct {
 svc *iamapp.TenantService
}

func (a *tenantSettingsAdapter) GetEmbedModel(ctx context.Context, tenantID string) (string, error) {
 settings, err := a.svc.GetSettings(ctx, tenantID)
 if err != nil {
  return "", err
 }
 if v, ok := settings["embed_model"].(string); ok {
  return v, nil
 }
 return "", nil
}

func wireTenantSettingsReader(svc *iamapp.TenantService) port.TenantSettingsReader {
 return &tenantSettingsAdapter{svc: svc}
}

// ragSearchAdapter 实现 port.RAGSearchProvider，桥接 knowledge.RAGService。
type ragSearchAdapter struct {
 rag *knowledgeapp.RAGService
}

func (a *ragSearchAdapter) SearchKnowledge(
 ctx context.Context, tenantID string, workspaces []string, query string, topK int,
) (string, error) {
 fn := knowledgeapp.NewRAGSearchFn(a.rag, tenantID)
 return fn(ctx, workspaces, query, topK)
}

func wireRAGSearchProvider(rag *knowledgeapp.RAGService) port.RAGSearchProvider {
 return &ragSearchAdapter{rag: rag}
}

// SkillMetaRepo wireup
func wireSkillMetaRepo(pool *pgxpool.Pool) port.SkillMetadataRepo {
 return skillpersistence.NewSkillMetaRepo(pool)
}
```

- [ ] **Step 6: 在 buildAgent 中装配 AgentService**

```go
// api/wiring/agent.go - buildAgent 修改
func (c *Container) buildAgent(ctx context.Context) error {
 // ... 现有 Registry / ChatStore / ExecStore 构造保持不变 ...

 tsReader := wireTenantSettingsReader(c.IAM.TenantService)
 skillMeta := wireSkillMetaRepo(c.Storage.Pool)
 ragSearch := wireRAGSearchProvider(c.Knowledge.RAGService)

 c.Agent.Service = application.NewAgentService(
  c.Agent.Registry,
  tsReader,
  skillMeta,
  ragSearch,
  c.Agent.TenantResolver,
  c.Agent.ExecStore,
  c.Logger,
 )
 c.Agent.Service.SetMCPToolProvider(c.MCP.AgentToolProvider)
 return nil
}
```

(`SetMCPToolProvider` setter 需要在 Task 3 的 service 上加，避免 NewAgentService 参数过多。)

- [ ] **Step 7: 跑全部 wiring 编译 + 单测**

```bash
go build ./... && go test ./api/wiring/... ./internal/agent/... -race
```

预期: build OK + tests PASS

- [ ] **Step 8: Commit**

```bash
git add api/wiring/agent.go \
        internal/skill/infrastructure/persistence/skill_meta_repo.go \
        internal/skill/infrastructure/persistence/skill_meta_repo_test.go
git commit -m "feat(wiring): add adapters for tenant settings, skill metadata, RAG search"
```

---

### Task 5: 重写 agent_handler.go(transport-only)

**Files:**

- Modify: `api/http/handler/agent_handler.go:1-172`(完整重写)
- Modify: `api/http/handler/handler_test.go:27-34`(NewAgentHandler 参数变更)

**Interfaces:**

- Consumes: `*application.AgentService`(Task 2-4 完成)
- Produces:

  ```go
  type AgentHandler struct { svc *agent.AgentService; logger *zap.Logger }
  func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler
  ```

- [ ] **Step 1: 改写 handler_test.go 的 NewAgentHandler 签名测试**

```go
// api/http/handler/handler_test.go
func TestNewAgentHandler(t *testing.T) {
 logger := zap.NewNop()
 handler := NewAgentHandler(nil, logger)
 if handler == nil {
  t.Error("expected AgentHandler to be non-nil")
 }
}
```

- [ ] **Step 2: 跑测试验证失败(签名不匹配)**

```bash
go test ./api/http/handler/... -run TestNewAgentHandler
```

预期: `too many arguments to NewAgentHandler` 或类似

- [ ] **Step 3: 重写 agent_handler.go**

```go
// api/http/handler/agent_handler.go
package handler

import (
 agent "github.com/byteBuilderX/stratum/internal/agent/application"
 "go.uber.org/zap"
)

const previewMaxChars = 50

type AgentHandler struct {
 svc    *agent.AgentService
 logger *zap.Logger
}

func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler {
 return &AgentHandler{svc: svc, logger: logger}
}

// CreateAgentRequest / UpdateAgentRequest / AgentResponse / ExecuteAgentRequest /
// AgentExecutionResult 保持不变(DTO 形状冻结)
type CreateAgentRequest struct {
 Name                  string   `json:"name" binding:"required"`
 Type                  string   `json:"type"`
 Description           string   `json:"description"`
 Persona               string   `json:"persona"`
 SystemPrompt          string   `json:"systemPrompt"`
 LLMModel              string   `json:"llmModel" binding:"required"`
 EmbedModel            string   `json:"embedModel"`
 MaxIterations         int      `json:"maxIterations" binding:"required"`
 MaxContextTokens      int      `json:"maxContextTokens"`
 AllowedSkills         []string `json:"allowedSkills"`
 MCPServerIDs          []string `json:"mcpServerIds"`
 KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
}
// ... UpdateAgentRequest / AgentResponse / ExecuteAgentRequest / AgentExecutionResult 同原
```

- [ ] **Step 4: 实现 GetAllAgents / GetAgent / DeleteAgent(纯 transport)**

```go
// api/http/handler/agent_crud_handler.go (重写)
package handler

import (
 "net/http"
 "strconv"

 "github.com/byteBuilderX/stratum/api/middleware"
 agent "github.com/byteBuilderX/stratum/internal/agent/application"
 "github.com/gin-gonic/gin"
)

func (h *AgentHandler) GetAllAgents(c *gin.Context) {
 if _, ok := tenantIDFromCtx(c); !ok {
  respondMissingTenant(c)
  return
 }
 dtos, err := h.svc.List(c.Request.Context())
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"agents": toAgentResponses(dtos)})
}

func (h *AgentHandler) GetAgent(c *gin.Context) {
 if _, ok := tenantIDFromCtx(c); !ok {
  respondMissingTenant(c)
  return
 }
 dto, err := h.svc.Get(c.Request.Context(), c.Param("id"))
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, toAgentResponse(dto))
}

func (h *AgentHandler) CreateAgent(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req CreateAgentRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 dto, err := h.svc.Create(c.Request.Context(), agent.CreateAgentInput{
  TenantID: tenantID, Name: req.Name, Type: req.Type, Description: req.Description,
  Persona: req.Persona, SystemPrompt: req.SystemPrompt, LLMModel: req.LLMModel,
  EmbedModel: req.EmbedModel, MaxIterations: req.MaxIterations,
  MaxContextTokens: req.MaxContextTokens, AllowedSkills: req.AllowedSkills,
  MCPServerIDs: req.MCPServerIDs, KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
 })
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusCreated, toAgentResponse(dto))
}

func (h *AgentHandler) UpdateAgent(c *gin.Context) {
 if _, ok := tenantIDFromCtx(c); !ok {
  respondMissingTenant(c)
  return
 }
 var req UpdateAgentRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 dto, err := h.svc.Update(c.Request.Context(), c.Param("id"), agent.UpdateAgentInput{
  Name: req.Name, Type: req.Type, Description: req.Description, Persona: req.Persona,
  SystemPrompt: req.SystemPrompt, LLMModel: req.LLMModel,
  MaxIterations: req.MaxIterations, MaxContextTokens: req.MaxContextTokens,
  AllowedSkills: req.AllowedSkills, MCPServerIDs: req.MCPServerIDs,
  KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
 })
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, toAgentResponse(dto))
}

func (h *AgentHandler) DeleteAgent(c *gin.Context) {
 if _, ok := tenantIDFromCtx(c); !ok {
  respondMissingTenant(c)
  return
 }
 if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"message": "agent deleted successfully"})
}

func (h *AgentHandler) ListExecutions(c *gin.Context) {
 if _, ok := tenantIDFromCtx(c); !ok {
  respondMissingTenant(c)
  return
 }
 page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
 pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
 dtos, total, err := h.svc.ListExecutions(c.Request.Context(), page, pageSize)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"executions": dtos, "total": total})
}

func toAgentResponse(dto agent.AgentDTO) AgentResponse { /* 字段一一映射 */ }
func toAgentResponses(dtos []agent.AgentDTO) []AgentResponse { /* loop */ }
```

- [ ] **Step 5: 重写 agent_exec_handler.go(SSE flusher 留下,业务调 svc.ExecuteStream)**

```go
// api/http/handler/agent_exec_handler.go
package handler

import (
 "encoding/json"
 "errors"
 "fmt"
 "net/http"
 "time"

 "github.com/byteBuilderX/stratum/api/middleware"
 agent "github.com/byteBuilderX/stratum/internal/agent/application"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req ExecuteAgentRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 userID, _ := userIDFromCtx(c)
 id := c.Param("id")

 result, err := h.svc.Execute(c.Request.Context(), id, req.Query, agent.ExecOptions{
  TenantID: tenantID, UserID: userID, TraceID: middleware.GetTraceID(c),
  ConversationID: req.ConversationID,
  MaxStepsOverride: optMaxSteps(req.Options),
  TimeoutOverride:  optTimeout(req.Options),
 })
 if err != nil {
  h.logger.Error("agent execution failed", zap.String("agentId", id), zap.Error(err))
  c.JSON(http.StatusOK, AgentExecutionResult{
   AgentID: id, Input: req.Query, Error: err.Error(),
  })
  return
 }
 thoughtsJSON, _ := json.Marshal(result.Thoughts)
 toolCallsJSON, _ := json.Marshal(result.ToolCalls)
 c.JSON(http.StatusOK, AgentExecutionResult{
  AgentID: id, Input: req.Query, Output: result.Output,
  Steps: result.Steps, TokensUsed: result.TokensUsed,
  Duration: result.Duration.String(),
  Thoughts: result.Thoughts, ToolCalls: result.ToolCalls,
  Metadata: map[string]interface{}{
   "thoughtsJSON":  string(thoughtsJSON),
   "toolCallsJSON": string(toolCallsJSON),
  },
 })
}

func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req ExecuteAgentRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 userID, _ := userIDFromCtx(c)

 c.Header("Content-Type", "text/event-stream")
 c.Header("Cache-Control", "no-cache")
 c.Header("Connection", "keep-alive")
 c.Header("X-Accel-Buffering", "no")
 c.Header("Transfer-Encoding", "chunked")

 flusher, hasFlusher := c.Writer.(http.Flusher)
 writeEvent := func(data string) {
  fmt.Fprintf(c.Writer, "data: %s\n\n", data) //nolint:errcheck
  if hasFlusher {
   flusher.Flush()
  }
 }
 fmt.Fprint(c.Writer, ": heartbeat\n\n") //nolint:errcheck
 if hasFlusher { flusher.Flush() }

 go func() {
  ticker := time.NewTicker(constants.SSEHeartbeatInterval)
  defer ticker.Stop()
  for {
   select {
   case <-ticker.C:
    fmt.Fprint(c.Writer, ": heartbeat\n\n") //nolint:errcheck
    if hasFlusher { flusher.Flush() }
   case <-c.Request.Context().Done():
    return
   }
  }
 }()

 tokenCb := func(token string) {
  payload, _ := json.Marshal(map[string]string{"token": token})
  writeEvent(string(payload))
 }

 result, err := h.svc.ExecuteStream(c.Request.Context(), c.Param("id"), req.Query, agent.ExecOptions{
  TenantID: tenantID, UserID: userID, TraceID: middleware.GetTraceID(c),
  ConversationID: req.ConversationID,
 }, tokenCb)
 if err != nil {
  if errors.Is(err, context.Canceled) && c.Request.Context().Err() != nil {
   return
  }
  h.logger.Error("agent stream execution failed", zap.Error(err))
  payload, _ := json.Marshal(map[string]interface{}{"error": err.Error()})
  writeEvent(string(payload))
  return
 }
 donePayload, _ := json.Marshal(map[string]interface{}{
  "done": true, "output": result.Output, "steps": result.Steps,
  "tokensUsed": result.TokensUsed, "duration": result.Duration.String(),
 })
 writeEvent(string(donePayload))
}

func optMaxSteps(opts map[string]interface{}) int {
 if v, ok := opts["maxSteps"].(float64); ok { return int(v) }
 return 0
}
func optTimeout(opts map[string]interface{}) time.Duration {
 if v, ok := opts["timeout"].(float64); ok { return time.Duration(v) * time.Second }
 return 0
}
```

- [ ] **Step 6: 修改 router.go 装配点**

```go
// api/http/router.go:135 — registerAgents
agentHandler := handler.NewAgentHandler(c.Agent.Service, c.Logger)
```

- [ ] **Step 7: 跑全部测试 + 编译**

```bash
go build ./... && go test ./api/http/handler/... ./internal/agent/... ./api/wiring/... -race
```

预期: build OK + tests PASS

- [ ] **Step 8: 验证 import 黑名单已清**

```bash
grep -E "tenantdb|pgxpool|pgx/v5|infrastructure" \
     api/http/handler/agent_handler.go \
     api/http/handler/agent_crud_handler.go \
     api/http/handler/agent_exec_handler.go
```

预期: 无输出

- [ ] **Step 9: Commit**

```bash
git add api/http/handler/agent_handler.go \
        api/http/handler/agent_crud_handler.go \
        api/http/handler/agent_exec_handler.go \
        api/http/handler/handler_test.go \
        api/http/router.go
git commit -m "refactor(handler): collapse agent handlers to transport-only via AgentService"
```

---

### Task 6: depguard 锁链 + tenantdb 引用全消验证

**Files:**

- Modify: `.golangci.yml`(handler-no-infra 规则扩展)

**Interfaces:**

- Consumes: 全部前置 task 完成
- Produces: lint 时 handler/* 引入黑名单包将 fail CI

- [ ] **Step 1: 现状基线 lint**

```bash
cd /home/yang/go-projects/stratum
golangci-lint run --enable-only=depguard ./api/http/handler/... 2>&1 | tee /tmp/depguard_before.log
```

记录现有违规计数(应该已经为 0,因 Task 5 完成后)。

- [ ] **Step 2: 扩展 handler-no-infra 规则**

```yaml
# .golangci.yml — depguard.rules.handler-no-infra
handler-no-infra:
  files:
    - '**/api/http/handler/**'
  deny:
    - pkg: 'github.com/byteBuilderX/stratum/internal/*/infrastructure'
      desc: 'handler 不得直接 import infrastructure，使用 application 服务'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/tenantdb'
      desc: 'handler 不得直接使用 tenantdb，从 middleware 取 tenantID'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/storage/postgres'
      desc: 'handler 不得直接持有 pg pool'
    - pkg: 'github.com/byteBuilderX/stratum/pkg/crypto'
      desc: 'handler 不得做加解密'
    - pkg: 'github.com/jackc/pgx/v5'
    - pkg: 'github.com/jackc/pgx/v5/pgxpool'
    - pkg: 'github.com/jackc/pgx/v5/pgconn'
    - pkg: 'github.com/redis/go-redis/v9'
    - pkg: 'github.com/milvus-io/milvus-sdk-go/v2'
```

- [ ] **Step 3: 重跑 lint 验证仍 0 违规**

```bash
golangci-lint run --enable-only=depguard ./api/http/handler/... 2>&1 | tee /tmp/depguard_after.log
diff /tmp/depguard_before.log /tmp/depguard_after.log
```

预期: 双方均为 0 违规

- [ ] **Step 4: 跑全量 lint**

```bash
golangci-lint run ./...
```

预期: 全绿(允许已存在 warning 不变)

- [ ] **Step 5: Commit**

```bash
git add .golangci.yml
git commit -m "chore(lint): tighten handler-no-infra depguard rules to block tenantdb/pgx/crypto"
```

---

### Task 7: 集成回归 + Contract Test 验证

**Files:**

- Run: 全量测试套
- Verify: `api/http/contract_test.go` + `testdata/contracts/*.golden.json` 不变

- [ ] **Step 1: 跑全量 -race 测试**

```bash
go test -race -timeout 60s ./...
```

预期: 全部 PASS

- [ ] **Step 2: 跑 contract 测试**

```bash
go test ./api/http/... -run TestContract -v
```

预期: golden 比对全 PASS,响应 shape 不变

- [ ] **Step 3: 检查 handler 文件行数达标**

```bash
wc -l api/http/handler/agent_handler.go \
      api/http/handler/agent_crud_handler.go \
      api/http/handler/agent_exec_handler.go
```

预期:

- agent_handler.go ≤ 80
- agent_crud_handler.go ≤ 140
- agent_exec_handler.go ≤ 150

- [ ] **Step 4: 启动服务做端到端冒烟**

```bash
make infra-up
go run ./cmd/server &
SERVER_PID=$!
sleep 3

# 创建 agent
curl -X POST http://localhost:8080/agents \
  -H "Authorization: Bearer ${TEST_JWT}" \
  -d '{"name":"test","llmModel":"qwen-turbo","maxIterations":5}'

# 执行 agent
curl -X POST http://localhost:8080/agents/${AID}/execute \
  -H "Authorization: Bearer ${TEST_JWT}" \
  -d '{"query":"hello"}'

kill $SERVER_PID
```

预期: 创建 + 执行均返回 200,响应 shape 与重构前一致

- [ ] **Step 5: Commit final marker**

```bash
git commit --allow-empty -m "refactor(agent): handler DDD refactor complete - phase 1 closed"
```

---

### Task 8: 收尾 — spec 状态更新 + 进入下一阶段

**Files:**

- Modify: `docs/superpowers/specs/2026-06-18-agent-handler-ddd-refactor-design.md`(P2 阶段标记完成)

- [ ] **Step 1: 在 spec 文末追加完成标记**

```markdown
## 实施记录

- 2026-06-18 P1+P2 完成:agent ctx 三 handler 全部 transport-only,新增 AgentService + 3 ports + 1 adapter
- handler 行数:172/283/282 → 实际值 (按 Task 7 Step 3 填入)
- 后续:P3-P8 按 spec §9 顺序推进
```

- [ ] **Step 2: Commit spec 更新**

```bash
git add docs/superpowers/specs/2026-06-18-agent-handler-ddd-refactor-design.md
git commit -m "docs(spec): mark agent ctx refactor (P1+P2) complete"
```

---

## 验证矩阵(完成后逐项确认)

| 项 | 命令 | 预期 |
|---|---|---|
| 编译 | `go build ./...` | OK |
| 单测 | `go test -race ./...` | PASS |
| Lint | `golangci-lint run ./...` | 全绿 |
| handler tenantdb | `grep tenantdb api/http/handler/agent*.go` | 无输出 |
| handler pgxpool | `grep pgxpool api/http/handler/agent*.go` | 无输出 |
| handler infra import | `grep -E 'internal/.*/infrastructure' api/http/handler/agent*.go` | 无输出 |
| contract | `go test -run TestContract ./api/http/...` | PASS |
| 行数 agent_handler | `wc -l api/http/handler/agent_handler.go` | ≤ 80 |
| 行数 agent_crud | `wc -l api/http/handler/agent_crud_handler.go` | ≤ 140 |
| 行数 agent_exec | `wc -l api/http/handler/agent_exec_handler.go` | ≤ 150 |

---

## Out of Scope(本计划不做)

- iam ctx auth_*_handler 重构(Plan #2)
- memory_*_handler 重构(Plan #3)
- chat_handler 跨 ctx 拆分(Plan #4)
- mcp/rag handler 微调(Plan #5)
- 前端任何变更(响应 shape 冻结)
- `pkg/tenantdb` 整包删除(Plan #N,所有 ctx 清完后)
- `internal/agent/infrastructure/*` 内部实现重构(只做错误翻译)
