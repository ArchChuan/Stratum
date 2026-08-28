# 平台管理增强（手动添加模型 · 平台审计日志 · 参数草稿）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现三个平台管理侧需求：①厂商管理可手动添加模型（`POST /admin/models`）；②平台管理下的租户 CRUD 与平台管理员变更补写平台审计 + 前端「平台审计日志」页 + 操作者显示增强；③平台参数有版本分组时「保存草稿」与「发布」分离。

**Architecture:** 需求 1 复用 llmgateway 现有 `UpdatePlatform` 的 public 事务 + 平台审计模式，新增 `CreatePlatform`（16 列 INSERT）与 `ModelMgmtService.Create`（校验 + 审计，通过 `.WithProviderRepo` 注入 provider repo 校验厂商存在）。需求 2 先把 llmgateway 私有 `insertPlatformAuditTx` 提取为 audit 包导出函数 `InsertPlatformAuditTx`，再把 iam 的 `AdminTenantRepo`/`AdminUserRepo` 从裸 Exec 事务化为 BeginTx + 审计；before 投影由 service 在 `Get` 后构造（infra 不依赖 application helper）。需求 3 纯前端：把 PlatformTabPanel 的 patch 构造提取为 `buildPatch`，有分组走 `createDraft`，无分组保留立即保存。

**Tech Stack:** Go 1.25.12（gin v1.9.1 / pgx v5.9.2 / pgxmock v2 / testify）、React 18.3（Vite 6.4 / Ant Design 5.20 / React Router 7 / TypeScript / Zod）、PostgreSQL 多租户（public 平台目录 + 租户 schema）。

## Global Constraints

- 所有 tenant-scoped repository 方法必须通过 `execTenant(ctx, tenantID, fn)`；本计划涉及的 public 平台目录（models/providers/platform_resource_change_audits/tenants/users）不走租户边界，但 SQL 必须 schema-qualified（`public.`）。
- Go 行宽 ≤120；import 按 stdlib → third-party → internal 分组；错误逐层 `fmt.Errorf("operation: %w", err)` 包装；日志只用 Zap。
- 新函数圈复杂度 ≤10、认知复杂度 ≤15、长度 ≤120 行、嵌套 ≤4。
- 平台审计投影必须脱敏：provider 投影显式排除 apiKey；租户投影仅 id/name/slug/plan/status。
- 平台级操作 actor_tenant_id 传空 → NULL（`PlatformChangeAuditInsertSQL` 语义）。
- 禁止输出 token、cookie、密钥、密码或原始 API key（测试与浏览器对账证据只展示非敏感字段）。
- HTTP 状态映射：客户端输入错误映射 4xx（`MapErrorToStatus`）；新增 sentinel 必须登记。
- 修改 port 后立即搜索并同步所有 test mock/stub。
- `go vet && go test -short ./...` 快速验证；PR 前 `go test -v -race -timeout 30s ./...`；前端 `make fe-lint && make fe-build`。
- 提交信息以 `Co-Authored-By: Claude <noreply@anthropic.com>` 结尾；提交/PR 标题 `[type](scope): description`。

## 规范偏差修正（以代码为准，覆盖 spec 原文）

1. **模型契约不走 proto**：spec 3.1 声称 HTTP 参数契约走 `proto/` + `make proto-gen`。实际 `model_mgmt_handler.go` 直接 bind 带 json tag 的 application 类型（先例 `UpdateModelInput`）。本计划 `CreateModelInput` 显式带 json tags 直接绑定，**不修改 proto、不运行 make proto-gen**。
2. **ListPlatform 不用 LEFT JOIN**：spec 4.4 声称 `LEFT JOIN public.users`。实际本文件已有 `loadActorNames`/`actorDisplayName`（tx 内批量映射 display_name/github_login，system actor 兜底 actor_id），复用它们；`buildPlatformAuditWhere` 的 actor_name 过滤从 `actor_id ILIKE` 升级为与 `buildChangeAuditWhere` 相同的 `public.users` EXISTS 模式。
3. **before 投影在 service 层构造**：spec 4.3 声称 UpdatePatch/HardDelete 在事务内 SELECT 旧值构造 before。实际为遵守 DDD 分层（infra 不 import application helper），改为 service 先 `Get` 构造 before/after 投影，repo 事务内只做 BEGIN → 变更 → `InsertPlatformAuditTx` → COMMIT，不再重复 SELECT。

---

### Task 1: 审计枚举扩展 + 提取共享 `InsertPlatformAuditTx`

**Files:**

- Modify: `internal/audit/domain/change_audit.go:11-23`（ResourceKind 主常量块加两值）
- Create: `internal/audit/infrastructure/persistence/insert_platform_audit.go`
- Modify: `internal/llmgateway/infrastructure/platform_audit.go` → 删除文件
- Modify: `internal/llmgateway/infrastructure/provider_repo.go:289`、`internal/llmgateway/infrastructure/model_repo.go:238`（调用点改共享函数）
- Create: `internal/audit/infrastructure/persistence/insert_platform_audit_test.go`
- Test: `internal/audit/infrastructure/persistence/change_audit_insert_test.go`（既有，确认不破坏）

**Interfaces:**

- Produces: `auditdomain.ResourceKindTenant = "tenant"`、`auditdomain.ResourceKindAdmin = "admin"`；`auditpersistence.InsertPlatformAuditTx(ctx context.Context, tx pgx.Tx, actorTenantID string, ev *auditdomain.ResourceChangeAuditEvent) error`（包 `internal/audit/infrastructure/persistence`，import 别名 `auditpersistence`）。Task 4 依赖两者。

- [ ] **Step 1: 新增 ResourceKind 常量（先改 domain，无测试直接进）**

`internal/audit/domain/change_audit.go` 主常量块（11-23 行）在 `ResourceKindProvider` 之后追加两行：

```go
 ResourceKindProvider   = "provider"   // 新增：LLM provider 配置
 ResourceKindTenant     = "tenant"     // 新增：租户生命周期（创建/更新/删除）
 ResourceKindAdmin      = "admin"      // 新增：平台管理员角色变更
```

- [ ] **Step 2: 创建共享 `InsertPlatformAuditTx`（纯搬移 + 导出）**

新建 `internal/audit/infrastructure/persistence/insert_platform_audit.go`：

```go
package persistence

import (
 "context"
 "fmt"

 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// InsertPlatformAuditTx writes a public-catalog audit row in the same
// transaction as the public provider/model/tenant mutation. It is the shared
// execution scaffold for platform_resource_change_audits: llmgateway and iam
// both call it so the INSERT column contract stays in lockstep with
// PlatformChangeAuditInsertSQL (asserted by change_audit_insert_test.go).
func InsertPlatformAuditTx(
 ctx context.Context,
 tx pgx.Tx,
 actorTenantID string,
 ev *auditdomain.ResourceChangeAuditEvent,
) error {
 if ev == nil {
  return nil
 }
 ev = ev.Normalized()
 eventID := ev.EventID
 if eventID == "" {
  eventID = uuid.Must(uuid.NewV7()).String()
 }
 var tenant any
 if actorTenantID != "" {
  tenant = actorTenantID
 }
 _, err := tx.Exec(ctx, auditdomain.PlatformChangeAuditInsertSQL,
  eventID, ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, tenant,
  ev.ActorType, ev.Source, ev.ProposalID, ev.Before, ev.After)
 if err != nil {
  return fmt.Errorf("insert platform audit: %w", err)
 }
 return nil
}
```

- [ ] **Step 3: 更新 llmgateway 两个调用点 + 删除私有副本**

`internal/llmgateway/infrastructure/provider_repo.go` 与 `model_repo.go`：

a) 各自新增 import（在已有 `auditdomain` import 行后追加）：

```go
 auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
```

b) `provider_repo.go:289` 与 `model_repo.go:238` 的调用点改为：

```go
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
```

c) 删除文件 `internal/llmgateway/infrastructure/platform_audit.go`（其唯一导出符号已迁出；如不再被引用则一并清理 `model_repo.go`/`provider_repo.go` 中不再使用的 import——`model_repo.go` 仍用 uuid/pgx，`provider_repo.go` 仍用 pgx）。

- [ ] **Step 4: 为共享函数写 pgxmock 单测**

新建 `internal/audit/infrastructure/persistence/insert_platform_audit_test.go`：

```go
package persistence

import (
 "context"
 "regexp"
 "testing"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/jackc/pgx/v5"
 "github.com/pashagolub/pgxmock/v2"
 "github.com/stretchr/testify/require"
)

func TestInsertPlatformAuditTx_WritesRow(t *testing.T) {
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(mock.Close)
 tx, err := mock.Begin(context.Background())
 require.NoError(t, err)

 ev := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindTenant,
  ResourceID:   "tid-1",
  Operation:    auditdomain.ChangeOpCreate,
  ActorID:      "actor-1",
  Before:       []byte(`{}`),
  After:        []byte(`{"id":"tid-1"}`),
 }
 mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
  WithArgs(pgxmock.AnyArg(), "tenant", "tid-1", "create", "actor-1", nil,
   "user", "api", "", []byte(`{}`), []byte(`{"id":"tid-1"}`)).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))

 err = InsertPlatformAuditTx(context.Background(), tx, "", ev)
 require.NoError(t, err)
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertPlatformAuditTx_WithActorTenant(t *testing.T) {
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(mock.Close)
 tx, err := mock.Begin(context.Background())
 require.NoError(t, err)

 ev := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindAdmin,
  ResourceID:   "u-9",
  Operation:    auditdomain.ChangeOpCreate,
  ActorID:      "actor-1",
 }
 mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
  WithArgs(pgxmock.AnyArg(), "admin", "u-9", "create", "actor-1", "tenant-abc",
   "user", "api", "", []byte(`{}`), []byte(`{}`)).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))

 err = InsertPlatformAuditTx(context.Background(), tx, "tenant-abc", ev)
 require.NoError(t, err)
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertPlatformAuditTx_NilEventNoop(t *testing.T) {
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(mock.Close)
 tx, err := mock.Begin(context.Background())
 require.NoError(t, err)

 require.NoError(t, InsertPlatformAuditTx(context.Background(), tx, "", nil))
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertPlatformAuditTx_ErrorWrapped(t *testing.T) {
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(mock.Close)
 tx, err := mock.Begin(context.Background())
 require.NoError(t, err)

 ev := &auditdomain.ResourceChangeAuditEvent{ResourceKind: "model", ResourceID: "m-1", Operation: "update", ActorID: "a"}
 mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.platform_resource_change_audits`)).
  WillReturnError(errAny)

 err = InsertPlatformAuditTx(context.Background(), tx, "", ev)
 require.ErrorContains(t, err, "insert platform audit")
}
```

- [ ] **Step 5: 运行测试**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go test ./internal/audit/infrastructure/persistence/... ./internal/llmgateway/infrastructure/... -count=1
```

Expected: PASS（llmgateway 平台审计 integration 回归也在此目录）。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "refactor(audit): extract shared InsertPlatformAuditTx and add tenant/admin resource kinds

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 需求 1 后端 —— 手动添加模型

**Files:**

- Modify: `internal/llmgateway/domain/model.go`（新增 sentinel）
- Modify: `api/middleware/error_mapping.go:190`（登记 400 映射）
- Modify: `internal/llmgateway/application/model_mgmt_service.go`（`CreateModelInput` + `Create` + `WithProviderRepo`）
- Modify: `internal/llmgateway/domain/port/model_repo.go`（`CreatePlatform`）
- Modify: `internal/llmgateway/infrastructure/model_repo.go`（`CreatePlatform` 实现）
- Modify: `api/http/handler/model_mgmt_handler.go`（`Create` handler）
- Modify: `api/http/router.go:698-706`（`POST /admin/models`）
- Modify: `api/wiring/llmgateway.go:131`（注入 providerRepo）
- Modify: `internal/llmgateway/application/model_mgmt_service_test.go`、`api/http/handler/model_mgmt_handler_test.go`

**Interfaces:**

- Consumes: `auditpersistence.InsertPlatformAuditTx`（Task 1）
- Produces: `llmgatewaydomain.ErrInvalidModelInput`；`CreateModelInput{ProviderID, Name, Capabilities, ContextWindow, MaxTokens}`（json tags）；`ModelMgmtService.Create(ctx, actorID, tenantID, in) (*domain.Model, error)`；`port.CreatePlatform(ctx, m *domain.Model, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error`

- [ ] **Step 1: 新增领域 sentinel（先写测试）**

`internal/llmgateway/domain/model.go` 在 `ErrInvalidFallbackCandidates`（35 行）后新增：

```go
// ErrInvalidModelInput indicates the manual model create input is invalid
// (empty name or no capabilities). It is a client-input mistake and must map
// to 4xx, never 5xx.
var ErrInvalidModelInput = errors.New("invalid model input")
```

- [ ] **Step 2: 登记 400 映射**

`api/middleware/error_mapping.go` 在 190 行 `llmgatewaydomain.ErrInvalidFallbackCandidates: http.StatusBadRequest,` 之后新增一行：

```go
 llmgatewaydomain.ErrInvalidModelInput:         http.StatusBadRequest,
```

- [ ] **Step 3: service 加 Create + WithProviderRepo（先写测试）**

a) `internal/llmgateway/application/model_mgmt_service.go` import 增加 `"strings"` 与 `"github.com/google/uuid"`（当前 imports：context, encoding/json, fmt, auditdomain, domain, port, constants）。

b) Service struct 增加字段（71-75 行）：

```go
type ModelMgmtService struct {
 repo        port.ModelRepository
 invalidator ModelCacheInvalidator
 health      port.ModelHealthProvider
 providerRepo port.ProviderRepository
}
```

c) `WithProviderRepo` option（`WithHealth` 之后）：

```go
// WithProviderRepo 注入厂商仓库，供手动添加模型时校验厂商存在。
func (s *ModelMgmtService) WithProviderRepo(p port.ProviderRepository) *ModelMgmtService {
 s.providerRepo = p
 return s
}
```

d) `CreateModelInput` 定义（`UpdateModelInput` 之前）：

```go
// CreateModelInput carries the fields for manually adding a model to a
// provider. ContextWindow/MaxTokens of 0 mean "unset, use provider default".
type CreateModelInput struct {
 ProviderID   string                   `json:"providerId"`
 Name         string                   `json:"name"`
 Capabilities []domain.ModelCapability `json:"capabilities"`
 ContextWindow int                     `json:"contextWindow"`
 MaxTokens    int                      `json:"maxTokens"`
}
```

e) `Create` 方法（放 `Update` 之后）：

```go
// Create manually adds a model to a provider's catalog. Unlike discovery the
// model is provider_managed=false, so re-discovery never disables it.
func (s *ModelMgmtService) Create(ctx context.Context, actorID, tenantID string, in CreateModelInput) (*domain.Model, error) {
 if strings.TrimSpace(in.Name) == "" || len(in.Capabilities) == 0 {
  return nil, fmt.Errorf("%w: name and at least one capability are required", domain.ErrInvalidModelInput)
 }
 if in.ProviderID == "" {
  return nil, fmt.Errorf("%w: providerId is required", domain.ErrInvalidModelInput)
 }
 if s.providerRepo == nil {
  return nil, fmt.Errorf("model mgmt: provider repository unavailable")
 }
 if _, err := s.providerRepo.Get(ctx, in.ProviderID); err != nil {
  return nil, fmt.Errorf("model mgmt: provider check: %w", err)
 }
 m := &domain.Model{
  ID:                  uuid.Must(uuid.NewV7()).String(),
  ProviderID:          in.ProviderID,
  Name:                in.Name,
  Capabilities:        in.Capabilities,
  ContextWindow:       in.ContextWindow,
  MaxTokens:           in.MaxTokens,
  Enabled:             true,
  ProviderManaged:     false,
  ContextWindowSource: domain.CapabilitySourceManualUnknown,
  MaxTokensSource:     domain.CapabilitySourceManualUnknown,
 }
 audit, err := newChangeAudit(ctx, changeAuditInput{
  Kind: auditdomain.ResourceKindModel, ResourceID: m.ID, Operation: auditdomain.ChangeOpCreate,
  ActorID: actorID, After: modelSafeProjection(m),
 })
 if err != nil {
  return nil, err
 }
 platformRepo, ok := s.repo.(port.PlatformModelRepository)
 if !ok {
  return nil, fmt.Errorf("model mgmt: platform repository unavailable")
 }
 if err := platformRepo.CreatePlatform(ctx, m, tenantID, audit); err != nil {
  return nil, fmt.Errorf("model mgmt: create: %w", err)
 }
 s.invalidate()
 return s.withHealth(m), nil
}
```

- [ ] **Step 4: port 加 CreatePlatform**

`internal/llmgateway/domain/port/model_repo.go` `PlatformModelRepository` 接口（35-37 行）加方法：

```go
type PlatformModelRepository interface {
 UpdatePlatform(ctx context.Context, m *domain.Model, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
 CreatePlatform(ctx context.Context, m *domain.Model, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
}
```

- [ ] **Step 5: infra 实现 CreatePlatform（16 列 INSERT）**

`internal/llmgateway/infrastructure/model_repo.go` 在 `UpdatePlatform`（245 行）之后新增（复用 `r.pool.Begin` + `insertPlatformAuditTx`→共享函数模式）：

```go
// CreatePlatform inserts a manually-added model into the public catalog and
// writes its audit in the same public transaction. Column list is locked to
// the 035/039/040 migrations (16 columns incl. context_window_source and
// max_tokens_source).
func (r *PgModelRepo) CreatePlatform(
 ctx context.Context,
 m *domain.Model,
 actorTenantID string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 tx, err := r.pool.Begin(ctx)
 if err != nil {
  return fmt.Errorf("create platform model: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 caps := modelCapsToStrings(m.Capabilities)
 _, err = tx.Exec(ctx,
  `INSERT INTO public.models (id, provider_id, name, display_name, capabilities,
   context_window, max_tokens, context_window_source, max_tokens_source,
   input_price, output_price, recommended, enabled, provider_managed,
   sampling_params, max_temperature)
   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
  m.ID, m.ProviderID, m.Name, m.DisplayName, caps,
  m.ContextWindow, m.MaxTokens, m.ContextWindowSource, m.MaxTokensSource,
  m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ProviderManaged,
  nil, m.MaxTemperature)
 if err != nil {
  return fmt.Errorf("create platform model: %w", err)
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("create platform model commit: %w", err)
 }
 return nil
}
```

> 说明：`sampling_params` 传 `nil`（JSONB NULL = 无配置）；`max_temperature` 传 `m.MaxTemperature`（`*float64`，手动添加时 nil）。`InputPrice`/`OutputPrice` 默认 0（字段零值）。

- [ ] **Step 6: handler 加 Create**

`api/http/handler/model_mgmt_handler.go` 在 `Update`（81 行）之后新增：

```go
// Create POST /admin/models — manually adds a model to a provider's catalog.
func (h *ModelMgmtHandler) Create(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 actorID, _ := userIDFromCtx(c)
 var input llmapp.CreateModelInput
 if err := c.ShouldBindJSON(&input); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 m, err := h.svc.Create(c.Request.Context(), actorID, tenantID, input)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusCreated, m)
}
```

- [ ] **Step 7: 路由挂 POST**

`api/http/router.go` 在 `models` 组内、`models.GET("", ...)`（700 行）之后加一行：

```go
  models.POST("", adminMW, modelMgmtH.Create)
```

`adminMW` 即同文件已定义的 `middleware.RequireGlobalAdmin()`；`POST` 不加 `requireActive`（与 PUT 对齐，路由组前缀 `protectedTenantMiddleware` 已保证 tenant 上下文）。

- [ ] **Step 8: wiring 注入 providerRepo**

`api/wiring/llmgateway.go:131`：

```go
 mgmtSvc := llmapp.NewModelMgmtService(modelRepo, registry).WithHealth(health).WithProviderRepo(providerRepo)
```

- [ ] **Step 9: service 单测（mock repo 扩展 + provider stub）**

`internal/llmgateway/application/model_mgmt_service_test.go`：

a) `modelMgmtRepo` 加字段与方法（让 stub 实现 `PlatformModelRepository`）：

```go
type modelMgmtRepo struct {
 model   domain.Model
 models  []domain.Model
 err     error
 listErr error
 updated *domain.Model
 created *domain.Model
}

func (r *modelMgmtRepo) UpdatePlatform(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 r.updated = m
 return r.err
}
func (r *modelMgmtRepo) CreatePlatform(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 r.created = m
 return r.err
}
```

b) 新增 provider stub 与 Create 测试（追加到文件末尾）：

```go
// providerRepoStub 实现 port.ProviderRepository 的只读面（Create 校验只用 Get）。
type providerRepoStub struct {
 provider *domain.Provider
 err      error
}

func (p *providerRepoStub) Create(context.Context, *domain.Provider) (*domain.Provider, error) {
 return nil, nil
}
func (p *providerRepoStub) Get(_ context.Context, _ string) (*domain.Provider, error) {
 if p.err != nil {
  return nil, p.err
 }
 if p.provider == nil {
  return nil, nil
 }
 return p.provider, nil
}
func (p *providerRepoStub) GetMeta(context.Context, string) (*domain.Provider, error) {
 return nil, nil
}
func (p *providerRepoStub) List(context.Context) ([]domain.Provider, error) {
 return nil, nil
}
func (p *providerRepoStub) Update(context.Context, *domain.Provider) error { return nil }
func (p *providerRepoStub) Delete(context.Context, string) error           { return nil }

func TestModelMgmtService_Create(t *testing.T) {
 t.Run("rejects empty name", func(t *testing.T) {
  svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
  _, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
   ProviderID: "p-1", Capabilities: []domain.ModelCapability{domain.CapChat},
  })
  require.ErrorIs(t, err, domain.ErrInvalidModelInput)
 })

 t.Run("rejects no capabilities", func(t *testing.T) {
  svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
  _, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
   ProviderID: "p-1", Name: "gpt-x",
  })
  require.ErrorIs(t, err, domain.ErrInvalidModelInput)
 })

 t.Run("rejects missing providerId", func(t *testing.T) {
  svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
  _, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
   Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat},
  })
  require.ErrorIs(t, err, domain.ErrInvalidModelInput)
 })

 t.Run("propagates provider lookup failure", func(t *testing.T) {
  svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{
   err: errors.New("get provider: failed"),
  })
  _, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
   ProviderID: "ghost", Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat},
  })
  if !strings.Contains(err.Error(), "provider") {
   t.Fatalf("error %q does not mention provider", err.Error())
  }
 })

 t.Run("inserts manual model with defaults and audit", func(t *testing.T) {
  repo := &modelMgmtRepo{}
  invalidated := false
  svc := NewModelMgmtService(repo, invalidatorFunc(func() { invalidated = true })).
   WithProviderRepo(&providerRepoStub{provider: &domain.Provider{ID: "p-1"}})
  m, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
   ProviderID: "p-1", Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning},
   ContextWindow: 128000, MaxTokens: 4096,
  })
  require.NoError(t, err)
  require.Equal(t, "gpt-x", m.Name)
  require.False(t, m.ProviderManaged)
  require.True(t, m.Enabled)
  require.Equal(t, domain.CapabilitySourceManualUnknown, m.ContextWindowSource)
  require.Equal(t, 128000, m.ContextWindow)
  if !invalidated {
   t.Fatal("expected registry invalidation")
  }
 })
}
```

> 注意：`require` 已 import（文件顶部有 `github.com/stretchr/testify/require` 吗？当前文件用的是 `t.Fatal`。检查该文件 import —— 若无 require，则在测试里改用 `if err == nil { t.Fatal(...) }` + `errors.Is` 断言，或补 import。为稳妥，测试断言统一使用已有风格：`if err == nil { t.Fatal(...) }`；需要 `errors.Is` 时 `errors` 已 import。

- [ ] **Step 10: handler 单测**

`api/http/handler/model_mgmt_handler_test.go`（若不存在则新建，package handler）追加：

```go
package handler

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/gin-gonic/gin"

 "github.com/byteBuilderX/stratum/api/middleware"
 llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
 "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
 "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
 "github.com/byteBuilderX/stratum/pkg/reqctx"
)

// stubCreateModelRepo 实现 Create + CreatePlatform 的最小面。
type stubCreateModelRepo struct {
 created *domain.Model
}

func (r *stubCreateModelRepo) Create(context.Context, *domain.Model) error { return nil }
func (r *stubCreateModelRepo) CreatePlatform(_ context.Context, m *domain.Model, _ string, _ interface{ String() string }) error {
 _ = m
 return nil
}
```

> 说明：避免为 handler 测试引入 auditdomain 类型，`CreatePlatform` 的 audit 参数用最小接口。若编译报类型不符，改用真实签名 `*auditdomain.ResourceChangeAuditEvent` 并补 import。

再写 `TestModelMgmtHandlerCreate`：`gin.CreateTestContext` + `c.Set(middleware.ContextKeySub, "actor-1")` + `reqctx.WithTenantID`（tenant 注入方式以 `tenantIDFromCtx` 读取的 key 为准——`api/middleware` 或 `reqctx` 提供的 helper，先查 handler 现有测试如何注入 tenant）。HTTP POST `/admin/models`，body 含 `{"providerId":"p-1","name":"gpt-x","capabilities":["chat"]}`，断言 201 与响应 JSON 含 `"providerManaged":false`。

- [ ] **Step 11: 运行测试**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go vet ./internal/llmgateway/... ./api/http/... ./api/middleware/...
go test -short ./internal/llmgateway/... ./api/http/handler/... -count=1
```

Expected: PASS。若 `contract_test.go` 因 `PlatformModelRepository` 新增方法而无法用现有 `contractModRepo` 实现，需给该 stub 补 `CreatePlatform` 方法（返回 nil）。**不加 POST contract golden**——用 handler 单测覆盖即可。

- [ ] **Step 12: 提交**

```bash
git add -A
git commit -m "feat(llmgateway): support manually adding models to a provider

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 需求 1 前端 —— 厂商管理「添加模型」

**Files:**

- Modify: `web/src/modules/llm/model/llm.ts`（`CreateModelInput`）
- Modify: `web/src/modules/llm/api/llm.api.ts`（`createModel`）
- Create: `web/src/modules/llm/components/AddModelModal.tsx`
- Modify: `web/src/modules/llm/pages/ProviderListPage.tsx`
- Modify: `web/src/modules/llm/pages/ModelManagementPage.tsx`
- Modify: `web/src/modules/llm/pages/ModelListPage.tsx`
- Test: `make fe-lint && make fe-build`

**Interfaces:**

- Consumes: `llmApi.createModel(data: CreateModelInput): Promise<Model>`；`ModelCapability`（现有，注意前端类型无 `rerank`）
- Produces: `ProviderListPage` 新增 prop `onModelCreated?: () => void`；`ModelListPage` 新增 prop `refreshTick?: number`

- [ ] **Step 1: 定义 CreateModelInput 类型**

`web/src/modules/llm/model/llm.ts` 在 `UpdateModelInput`（62 行）之前新增：

```ts
export interface CreateModelInput {
  providerId: string;
  name: string;
  capabilities: ModelCapability[];
  contextWindow: number;
  maxTokens: number;
}
```

- [ ] **Step 2: api 新增 createModel**

`web/src/modules/llm/api/llm.api.ts`：import 行加 `CreateModelInput`；`listModels` 之后新增：

```ts
  createModel: async (data: CreateModelInput): Promise<Model> => {
    const res = await api.post<Model>('/admin/models', data);
    return res.data;
  },
```

- [ ] **Step 3: 新建 AddModelModal**

`web/src/modules/llm/components/AddModelModal.tsx`：

```tsx
import { Form, Input, InputNumber, Modal, Select } from 'antd';
import type { ModelCapability } from '../model/llm';

const CAPABILITY_OPTIONS: { label: string; value: ModelCapability }[] = [
  { label: '对话', value: 'chat' },
  { label: '嵌入', value: 'embedding' },
  { label: '视觉', value: 'vision' },
  { label: '工具调用', value: 'tool_use' },
  { label: '推理', value: 'reasoning' },
];

export interface AddModelFormValues {
  name: string;
  capabilities: ModelCapability[];
  contextWindow?: number;
  maxTokens?: number;
}

export function AddModelModal({
  open,
  providerName,
  loading,
  onCancel,
  onSubmit,
}: {
  open: boolean;
  providerName: string;
  loading: boolean;
  onCancel: () => void;
  onSubmit: (values: AddModelFormValues) => void;
}) {
  const [form] = Form.useForm<AddModelFormValues>();
  return (
    <Modal
      title={`为厂商「${providerName}」添加模型`}
      open={open}
      okText="添加"
      confirmLoading={loading}
      onOk={() => form.submit()}
      onCancel={() => {
        form.resetFields();
        onCancel();
      }}
    >
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <Form.Item
          label="模型名"
          name="name"
          rules={[{ required: true, message: '请输入模型名' }]}
          extra="与厂商 API 返回的模型标识一致（如 gpt-4o）"
        >
          <Input placeholder="如 gpt-4o" />
        </Form.Item>
        <Form.Item
          label="能力"
          name="capabilities"
          rules={[{ required: true, message: '请至少选择一项能力' }]}
        >
          <Select mode="multiple" placeholder="选择能力" options={CAPABILITY_OPTIONS} />
        </Form.Item>
        <Form.Item label="上下文窗口" name="contextWindow">
          <InputNumber min={0} style={{ width: '100%' }} placeholder="0 = 使用厂商默认" />
        </Form.Item>
        <Form.Item label="最大输出" name="maxTokens">
          <InputNumber min={0} style={{ width: '100%' }} placeholder="0 = 使用厂商默认" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
```

- [ ] **Step 4: ProviderListPage 接入**

`web/src/modules/llm/pages/ProviderListPage.tsx`：

a) import 增加：`AddModelModal`（含 `AddModelFormValues`）、`CreateModelInput`、`ModelCapability`、`message`。

b) 组件签名加 prop + 新增 state：

```tsx
export function ProviderListPage({ onModelCreated }: { onModelCreated?: () => void }) {
  ...
  const [addModelOpen, setAddModelOpen] = useState(false);
  const [addModelProvider, setAddModelProvider] = useState<Provider | null>(null);
  const [addModelLoading, setAddModelLoading] = useState(false);
```

c) 新增提交 handler（`handleDiscover` 之后）：

```tsx
  const handleAddModel = useCallback(
    async (values: AddModelFormValues) => {
      if (!addModelProvider) return;
      setAddModelLoading(true);
      try {
        const input: CreateModelInput = {
          providerId: addModelProvider.id,
          name: values.name.trim(),
          capabilities: values.capabilities,
          contextWindow: values.contextWindow ?? 0,
          maxTokens: values.maxTokens ?? 0,
        };
        await llmApi.createModel(input);
        message.success({ content: '模型已添加，可在模型目录编辑', duration: 2 });
        setAddModelOpen(false);
        setAddModelProvider(null);
        onModelCreated?.();
      } catch (err) {
        message.error({ content: extractErrorMessage(err, '添加模型失败'), duration: 3 });
      } finally {
        setAddModelLoading(false);
      }
    },
    [addModelProvider, onModelCreated],
  );
```

d) 操作列：width 300 → 360；在「发现模型」前加按钮：

```tsx
          <Button
            size="small"
            type="primary"
            ghost
            onClick={() => {
              setAddModelProvider(record);
              setAddModelOpen(true);
            }}
          >
            添加模型
          </Button>
```

e) 渲染 Modal（`DiscoverResultModal` 之前）：

```tsx
      <AddModelModal
        open={addModelOpen}
        providerName={addModelProvider?.name ?? ''}
        loading={addModelLoading}
        onCancel={() => {
          setAddModelOpen(false);
          setAddModelProvider(null);
        }}
        onSubmit={handleAddModel}
      />
```

- [ ] **Step 5: ModelManagementPage 中转 modelTick**

`web/src/modules/llm/pages/ModelManagementPage.tsx`：

```tsx
import { Tabs, Typography } from 'antd';
import { useState } from 'react';

import { ModelListPage } from './ModelListPage';
import { ProviderListPage } from './ProviderListPage';

const { Title } = Typography;

export function ModelManagementPage() {
  const [modelTick, setModelTick] = useState(0);
  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 20 }}>
        模型管理
      </Title>
      <Tabs
        defaultActiveKey="providers"
        items={[
          { key: 'providers', label: '厂商管理', children: <ProviderListPage onModelCreated={() => setModelTick((t) => t + 1)} /> },
          { key: 'models', label: '模型目录', children: <ModelListPage refreshTick={modelTick} /> },
        ]}
      />
    </div>
  );
}
```

- [ ] **Step 6: ModelListPage 加 refreshTick**

`web/src/modules/llm/pages/ModelListPage.tsx`：import 加 `useEffect`；签名与 effect：

```tsx
export function ModelListPage({ refreshTick }: { refreshTick?: number }) {
  const { models, loading, refresh, ... } = useModels();
  ...
  useEffect(() => {
    if (refreshTick && refreshTick > 0) void refresh();
  }, [refreshTick, refresh]);
```

> 首次渲染 refreshTick=0 不触发（useModels 自带首次 fetch），避免重复加载。

- [ ] **Step 7: 前端验证**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance/web
npm run lint
npm run build
```

Expected: 无 lint 错误、build 成功。

- [ ] **Step 8: 提交**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
git add -A
git commit -m "feat(web): add manual model creation to provider management

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 需求 2 后端 —— iam 事务化 + 补平台审计

**Files:**

- Modify: `internal/iam/domain/port/admin_tenant_repo.go`
- Modify: `internal/iam/domain/port/admin_user_repo.go`
- Modify: `internal/iam/infrastructure/persistence/admin_tenant_repo.go`
- Modify: `internal/iam/infrastructure/persistence/admin_user_repo.go`
- Modify: `internal/iam/application/admin_service.go`
- Modify: `api/http/handler/admin_handler.go`
- Modify: `api/http/handler/tenant_handler.go:298`
- Modify: `cmd/server/runtime.go:419-439`
- Modify: `internal/iam/application/admin_service_test.go`
- Modify: `api/http/handler/admin_handler_test.go`
- Modify: `internal/iam/infrastructure/persistence/admin_tenant_repo_internal_test.go`
- Modify: `internal/iam/infrastructure/persistence/admin_user_repo_test.go`

**Interfaces:**

- Consumes: `auditpersistence.InsertPlatformAuditTx`（Task 1）；`reqctx.SystemActorFromContext`、`reqctx.ChangeSourceFromContext`、`reqctx.WithSystemActor`
- Produces: 新签名 `AdminTenantRepo.Create/UpdatePatch/HardDelete`、`AdminUserRepo.SetAdminRole/RemoveAdminRole`（均带 `actorTenantID string` + `audit *auditdomain.ResourceChangeAuditEvent`）；`AdminService.CreateTenant(ctx, actorID, name, slug, plan, status)`、`UpdateTenant(ctx, actorID, id, patch)`、`DeleteTenant(ctx, actorID, id)`；helper `tenantProjection`、`marshalProjection`、`newPlatformAuditEvent`

- [ ] **Step 1: port 签名变更**

`internal/iam/domain/port/admin_tenant_repo.go`：

```go
package port

import (
 "context"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
)

// AdminTenantRepo handles platform-admin tenant CRUD against public.tenants.
// Distinct from TenantRepo (which is per-tenant member/settings work).
type AdminTenantRepo interface {
 Count(ctx context.Context, filter domain.TenantFilter) (int, error)
 List(ctx context.Context, filter domain.TenantFilter) ([]domain.Tenant, error)
 Get(ctx context.Context, id string) (*domain.Tenant, error)
 Create(ctx context.Context, t domain.Tenant, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
 UpdatePatch(ctx context.Context, id string, patch domain.TenantPatch, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
 HardDelete(ctx context.Context, id string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
 ProvisionSchema(ctx context.Context, tenantID string) error
}
```

`internal/iam/domain/port/admin_user_repo.go`（`AdminUser` 不变）：

```go
 // SetAdminRole promotes userID to system_admin. ErrUserNotFound if absent.
 SetAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
 // RemoveAdminRole demotes userID back to user. ErrUserNotFound if absent.
 RemoveAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
```

补 import `auditdomain`。

- [ ] **Step 2: admin_tenant_repo 事务化**

`internal/iam/infrastructure/persistence/admin_tenant_repo.go`：import 增加 `"fmt"`、`auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"`、`auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"`。

`Create`（97-103）改为：

```go
func (r *AdminTenantRepo) Create(ctx context.Context, t domain.Tenant, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
 if err != nil {
  return fmt.Errorf("admin create tenant: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 _, err = tx.Exec(ctx,
  "INSERT INTO public.tenants(id, name, slug, plan, status, created_at) VALUES($1,$2,$3,$4,$5,$6)",
  t.ID, t.Name, t.Slug, t.Plan, t.Status, t.CreatedAt,
 )
 if err != nil {
  return fmt.Errorf("admin create tenant: %w", err)
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("admin create tenant: commit: %w", err)
 }
 return nil
}
```

`UpdatePatch`（105-117）改为：

```go
func (r *AdminTenantRepo) UpdatePatch(ctx context.Context, id string, patch domain.TenantPatch, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
 if err != nil {
  return fmt.Errorf("admin update tenant: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 tag, err := tx.Exec(ctx,
  "UPDATE public.tenants SET plan=COALESCE(NULLIF($1,''), plan), status=COALESCE(NULLIF($2,''), status) WHERE id=$3 AND deleted_at IS NULL",
  patch.Plan, patch.Status, id,
 )
 if err != nil {
  return fmt.Errorf("admin update tenant: %w", err)
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrTenantNotFound
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("admin update tenant: commit: %w", err)
 }
 return nil
}
```

`HardDelete`（119-141）改为（事务内保留 `SELECT is_default` 检查）：

```go
func (r *AdminTenantRepo) HardDelete(ctx context.Context, id string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
 if err != nil {
  return fmt.Errorf("admin delete tenant: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 var isDefault bool
 err = tx.QueryRow(ctx,
  "SELECT is_default FROM public.tenants WHERE id=$1", id,
 ).Scan(&isDefault)
 if err != nil {
  if errors.Is(err, pgx.ErrNoRows) {
   return domain.ErrTenantNotFound
  }
  return err
 }
 if isDefault {
  return domain.ErrDefaultTenantDelete
 }
 tag, err := tx.Exec(ctx, "DELETE FROM public.tenants WHERE id=$1", id)
 if err != nil {
  return err
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrTenantNotFound
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("admin delete tenant: commit: %w", err)
 }
 return nil
}
```

> `Count/List/Get/ProvisionSchema/ActivateTenant/MarkProvisioningFailed` 不变。

- [ ] **Step 3: admin_user_repo 事务化**

`internal/iam/infrastructure/persistence/admin_user_repo.go`：import 增加 `"fmt"`、`auditdomain`、`auditpersistence`。

`SetAdminRole`（74-85）与 `RemoveAdminRole`（87-98）改为同模式：

```go
func (r *AdminUserRepo) SetAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
 if err != nil {
  return fmt.Errorf("set admin role: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 tag, err := tx.Exec(ctx,
  "UPDATE public.users SET global_role = 'system_admin', updated_at = NOW() WHERE id = $1 AND is_guest = false",
  userID)
 if err != nil {
  return err
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrUserNotFound
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("set admin role: commit: %w", err)
 }
 return nil
}

func (r *AdminUserRepo) RemoveAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
 if err != nil {
  return fmt.Errorf("remove admin role: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 tag, err := tx.Exec(ctx,
  "UPDATE public.users SET global_role = 'user', updated_at = NOW() WHERE id = $1",
  userID)
 if err != nil {
  return err
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrUserNotFound
 }
 if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
  return err
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("remove admin role: commit: %w", err)
 }
 return nil
}
```

- [ ] **Step 4: admin_service 加 actorID + 审计 helper**

`internal/iam/application/admin_service.go`：import 增加 `"encoding/json"`、`auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"`、`"github.com/byteBuilderX/stratum/pkg/reqctx"`。

新增 helper（放在 `normaliseFilter` 之前）：

```go
// tenantProjection 是租户的脱敏投影（仅公开字段，无凭据）。
func tenantProjection(t *domain.Tenant) map[string]any {
 if t == nil {
  return map[string]any{}
 }
 return map[string]any{
  "id": t.ID, "name": t.Name, "slug": t.Slug, "plan": t.Plan, "status": t.Status,
 }
}

// marshalProjection 序列化投影；nil 返回 nil（审计事件 Before/After 为 nil 时
// Normalized() 填 {}）。
func marshalProjection(v any) (json.RawMessage, error) {
 if v == nil {
  return nil, nil
 }
 b, err := json.Marshal(v)
 if err != nil {
  return nil, fmt.Errorf("admin audit: marshal projection: %w", err)
 }
 return b, nil
}

// newPlatformAuditEvent 构造平台审计事件：系统 actor（guest-reaper 等）覆盖为
// system/optimization；否则 actor_type=user、source 从 ctx（缺省 api）。
func newPlatformAuditEvent(ctx context.Context, kind, resourceID, operation, actorID string, before, after any) (*auditdomain.ResourceChangeAuditEvent, error) {
 ev := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: kind, ResourceID: resourceID, Operation: operation,
  ActorID: actorID, ActorType: auditdomain.ChangeActorUser,
 }
 if sysActor := reqctx.SystemActorFromContext(ctx); sysActor != "" {
  ev.ActorID = sysActor
  ev.ActorType = auditdomain.ChangeActorSystem
  ev.Source = auditdomain.ChangeSourceOptimization
 } else {
  ev.Source = reqctx.ChangeSourceFromContext(ctx)
  if ev.Source == "" {
   ev.Source = auditdomain.ChangeSourceAPI
  }
 }
 var err error
 if ev.Before, err = marshalProjection(before); err != nil {
  return nil, err
 }
 if ev.After, err = marshalProjection(after); err != nil {
  return nil, err
 }
 return ev, nil
}
```

> 注意：`reqctx.ChangeSourceFromContext` 返回 `(string, string)`（source, proposalID）。上面只用 source；如需 proposalID 语义，改为 `source, _ := reqctx.ChangeSourceFromContext(ctx)`。以 `pkg/reqctx/change_source.go:32-37` 实际签名为准。

改造三个方法：

```go
// CreateTenant inserts a new tenant row and provisions its schema.
func (s *AdminService) CreateTenant(ctx context.Context, actorID, name, slug, plan, status string) (*domain.Tenant, error) {
 t := domain.Tenant{
  ID:        uuid.Must(uuid.NewV7()).String(),
  Name:      name,
  Slug:      slug,
  Plan:      plan,
  Status:    status,
  CreatedAt: time.Now().UTC(),
 }
 audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, t.ID, auditdomain.ChangeOpCreate, actorID, nil, tenantProjection(&t))
 if err != nil {
  return nil, err
 }
 if err := s.repo.Create(ctx, t, "", audit); err != nil {
  return nil, err
 }
 if err := s.repo.ProvisionSchema(ctx, t.ID); err != nil {
  return nil, err
 }
 if s.tenantProvisionedHook != nil {
  s.tenantProvisionedHook(ctx, t.ID)
 }
 return &t, nil
}

// UpdateTenant patches plan/status fields.
func (s *AdminService) UpdateTenant(ctx context.Context, actorID, id string, patch domain.TenantPatch) error {
 t, err := s.repo.Get(ctx, id)
 if err != nil {
  return err
 }
 after := domain.Tenant{
  ID:     t.ID,
  Name:   t.Name,
  Slug:   t.Slug,
  Plan:   nonZeroOr(patch.Plan, t.Plan),
  Status: nonZeroOr(patch.Status, t.Status),
 }
 audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, id, auditdomain.ChangeOpUpdate, actorID, tenantProjection(t), tenantProjection(&after))
 if err != nil {
  return err
 }
 return s.repo.UpdatePatch(ctx, id, patch, "", audit)
}

// DeleteTenant hard-deletes the tenant row (cascades to all public-schema FK tables)
// then drops the tenant PG schema and Milvus collections.
// Storage cleanup failures are logged as warnings; the public row deletion is authoritative.
func (s *AdminService) DeleteTenant(ctx context.Context, actorID, id string) error {
 t, err := s.repo.Get(ctx, id)
 if err != nil {
  return err
 }
 audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindTenant, id, auditdomain.ChangeOpDelete, actorID, tenantProjection(t), nil)
 if err != nil {
  return err
 }
 if err := s.repo.HardDelete(ctx, id, "", audit); err != nil {
  return err
 }
 // Vector cleaner must run before schema drop — it queries tenant schema for RAG workspace names.
 if s.vectorCleaner != nil {
  if err := s.vectorCleaner.DropTenantCollections(ctx, id); err != nil {
   s.logger.Warn("failed to drop tenant vector collections", zap.String("tenant_id", id), zap.Error(err))
  }
 }
 if s.objectCleaner != nil {
  if err := s.objectCleaner.DropTenantObjects(ctx, id); err != nil {
   s.logger.Warn("failed to drop tenant objects", zap.String("tenant_id", id), zap.Error(err))
  }
 }
 if s.schemaCleaner != nil {
  if err := s.schemaCleaner.DropTenantSchema(ctx, id); err != nil {
   s.logger.Warn("failed to drop tenant schema", zap.String("tenant_id", id), zap.Error(err))
  }
 }
 if s.cacheInvalidator != nil {
  s.cacheInvalidator.Invalidate(id)
 }
 return nil
}
```

新增 `nonZeroOr`：

```go
func nonZeroOr(v, fallback string) string {
 if v != "" {
  return v
 }
 return fallback
}
```

`SetAdminRole`/`RemoveAdminRole` 构造审计（在各自 `userRepo.SetAdminRole`/`RemoveAdminRole` 调用前）：

```go
 audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindAdmin, userID, auditdomain.ChangeOpCreate, actorID, nil,
  map[string]any{"userID": userID, "role": domain.GlobalRoleSystemAdmin})
 if err != nil {
  return err
 }
 return s.userRepo.SetAdminRole(ctx, userID, "", audit)
```

```go
 audit, err := newPlatformAuditEvent(ctx, auditdomain.ResourceKindAdmin, userID, auditdomain.ChangeOpDelete, actorID, nil,
  map[string]any{"userID": userID, "role": domain.GlobalRoleUser})
 if err != nil {
  return err
 }
 return s.userRepo.RemoveAdminRole(ctx, userID, "", audit)
```

- [ ] **Step 5: handler 传 actorID**

`api/http/handler/admin_handler.go`：

- `CreateTenant`（71 行）前加 `actorID := c.GetString(middleware.ContextKeySub)`，调用改为 `h.svc.CreateTenant(c.Request.Context(), actorID, req.Name, req.Slug, req.Plan, req.Status)`。
- `UpdateTenant`（87 行）前加 `actorID := c.GetString(middleware.ContextKeySub)`，调用改为 `h.svc.UpdateTenant(c.Request.Context(), actorID, id, iamdomain.TenantPatch{...})`。
- `DeleteTenant`（99 行）前加 `actorID := c.GetString(middleware.ContextKeySub)`，调用改为 `h.svc.DeleteTenant(c.Request.Context(), actorID, c.Param("id"))`。

`api/http/handler/tenant_handler.go:298`（owner 自删）：`h.adminSvc.DeleteTenant(c.Request.Context(), tenantID)` → 加 `actorID := c.GetString(middleware.ContextKeySub)`（middleware 已 import），改为 `h.adminSvc.DeleteTenant(c.Request.Context(), actorID, tenantID)`。

- [ ] **Step 6: runtime.go guest-reaper 注入系统 actor**

`cmd/server/runtime.go`：import 增加 `"github.com/byteBuilderX/stratum/pkg/reqctx"`。`reapGuest`（419 行）内循环改为：

```go
 for _, tenantID := range tenantIDs {
  sysCtx := reqctx.WithSystemActor(ctx, "guest-reaper")
  if err := admin.DeleteTenant(sysCtx, "guest-reaper", tenantID); err != nil {
   logger.Warn("guest-reaper: delete tenant", zap.String("tenant_id", tenantID), zap.Error(err))
   metrics.IncReaperDeleteError("delete_tenant")
  }
 }
```

> 每次迭代内新建 sysCtx（`reqctx.WithSystemActor` 派生新的 ctx），避免复用带 system actor 标记的 ctx 泄漏到后续 `onboard.DeleteUser(ctx, userID)`。若确认 `WithSystemActor` 是派生（返回新 ctx、不改入参），可提到循环外；保守起见按迭代内处理。

- [ ] **Step 7: 同步测试 mock —— admin_service_test.go**

`internal/iam/application/admin_service_test.go`：import 加 `auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"`。`stubAdminUserRepo` 两个方法签名改为：

```go
func (s *stubAdminUserRepo) SetAdminRole(_ context.Context, id string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 if s.roles[id] == domain.GlobalRoleGlobalAdmin {
  return domain.ErrForbidden
 }
 s.roles[id] = domain.GlobalRoleSystemAdmin
 return nil
}
func (s *stubAdminUserRepo) RemoveAdminRole(_ context.Context, id string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 s.roles[id] = domain.GlobalRoleUser
 return nil
}
```

- [ ] **Step 8: 同步测试 mock —— admin_handler_test.go**

`api/http/handler/admin_handler_test.go`：import 加 `auditdomain`、`iamport`（若未 import）。`fakeAdminRepo` 方法字段与实现改签名：

```go
type fakeAdminRepo struct {
 countFn       func(context.Context, iamdomain.TenantFilter) (int, error)
 listFn        func(context.Context, iamdomain.TenantFilter) ([]iamdomain.Tenant, error)
 getFn         func(context.Context, string) (*iamdomain.Tenant, error)
 createFn      func(context.Context, iamdomain.Tenant, string, *auditdomain.ResourceChangeAuditEvent) error
 updateFn      func(context.Context, string, iamdomain.TenantPatch, string, *auditdomain.ResourceChangeAuditEvent) error
 deleteFn      func(context.Context, string, string, *auditdomain.ResourceChangeAuditEvent) error
 provisionFn   func(context.Context, string) error
 provisionCall int
}

func (f *fakeAdminRepo) Create(ctx context.Context, t iamdomain.Tenant, at string, a *auditdomain.ResourceChangeAuditEvent) error {
 return f.createFn(ctx, t, at, a)
}
func (f *fakeAdminRepo) UpdatePatch(ctx context.Context, id string, patch iamdomain.TenantPatch, at string, a *auditdomain.ResourceChangeAuditEvent) error {
 return f.updateFn(ctx, id, patch, at, a)
}
func (f *fakeAdminRepo) HardDelete(ctx context.Context, id, at string, a *auditdomain.ResourceChangeAuditEvent) error {
 return f.deleteFn(ctx, id, at, a)
}
```

`TestCreateTenant_success` 的 createFn 改为 `func(_ context.Context, _ iamdomain.Tenant, _ string, _ *auditdomain.ResourceChangeAuditEvent) error { return nil }`。`TestDeleteTenant_softDelete`/`notFound` 需加 `getFn`（service 现在先 Get）：softDelete 的 `getFn` 返回 `&iamdomain.Tenant{ID: "tid1"}, nil`，deleteFn 改为 `func(_ context.Context, id, _ string, _ *auditdomain.ResourceChangeAuditEvent) error { called = id; return nil }`；notFound 的 `getFn` 返回 `nil, iamdomain.ErrTenantNotFound`，deleteFn 不再被调用。

- [ ] **Step 9: 同步测试 —— admin_tenant_repo_internal_test.go**

每个事务化方法测试改为 `mock.ExpectBegin()` + Exec/Query + audit INSERT（`pgxmock.AnyArg()`）+ `mock.ExpectCommit()`：

```go
func TestAdminTenantRepo_Create(t *testing.T) {
 repo, mock := newAdminRepo(t)
 now := time.Now()
 mock.ExpectBegin()
 mock.ExpectExec(`INSERT INTO public\.tenants\(id, name, slug, plan, status, created_at\)`).
  WithArgs("t1", "Acme", "acme", "pro", "active", now).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 mock.ExpectExec(`INSERT INTO public\.platform_resource_change_audits`).
  WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
   pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
   pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 mock.ExpectCommit()

 err := repo.Create(context.Background(), domain.Tenant{ID: "t1", Name: "Acme", Slug: "acme", Plan: "pro", Status: "active", CreatedAt: now}, "", nil)
 require.NoError(t, err)
}
```

> `Create` 传 audit=nil 时 `InsertPlatformAuditTx` 直接 return nil（不执行 SQL），测试也可传 nil 从而免去 audit ExpectExec。为验证链路完整性，推荐传真实事件或保持 nil 简化。**简化方案：Create/UpdatePatch/HardDelete 测试都传 nil audit**（`InsertPlatformAuditTx(nil)` no-op），只保留 Begin/Exec/Commit 期望；新增一个专门的「审计写入」用例传真实事件验证 audit INSERT 被调用。HardDelete 测试：Begin → Query `SELECT is_default` → Exec DELETE → (nil audit) → Commit。

`TestAdminTenantRepo_Create_Fails`：`ExpectBegin` + `ExpectExec(...).WillReturnError(errAny)` + `ExpectRollback`（默认 defer Rollback）。

`TestAdminTenantRepo_UpdatePatch*`、`TestAdminTenantRepo_HardDelete*` 同理加 `ExpectBegin()`/`ExpectCommit()` 与签名（补 `"", nil` 两个参数）。

- [ ] **Step 10: 同步测试 —— admin_user_repo_test.go**

`SetAdminRole`/`RemoveAdminRole` 系列改为：

```go
func TestAdminUserRepo_SetAdminRole(t *testing.T) {
 repo, mock := newAdminUserRepo(t)
 mock.ExpectBegin()
 mock.ExpectExec(`SET global_role = 'system_admin'`).
  WithArgs("u1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectCommit()

 require.NoError(t, repo.SetAdminRole(context.Background(), "u1", "", nil))
}
```

NotFound/Fails 用例加 `ExpectBegin()` + `ExpectRollback()`（失败路径 defer Rollback）；`RemoveAdminRole` 同理。

- [ ] **Step 11: 运行测试**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go vet ./internal/iam/... ./api/http/handler/... ./cmd/server/...
go test -short ./internal/iam/... ./api/http/handler/... -count=1
go build ./cmd/server/...
```

Expected: PASS；`cmd/server` 编译通过（runtime.go 改动）。

- [ ] **Step 12: 提交**

```bash
git add -A
git commit -m "feat(iam): write platform audit for tenant and admin-role changes

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 需求 2 —— 平台审计查询增强（actor 名 + EXISTS 过滤）

**Files:**

- Modify: `internal/audit/infrastructure/persistence/change_audit_repo.go`（`ListPlatform`、`GetPlatformByID`、`buildPlatformAuditWhere`）
- Modify: `internal/audit/infrastructure/persistence/change_audit_repo_test.go`

**Interfaces:**

- Produces: `ListPlatform`/`GetPlatformByID` 的 `ActorName` 用 display_name > github_login > actor_id；`buildPlatformAuditWhere` 的 actor_name 走 `public.users` EXISTS + actor_id 兜底。前端 Task 6 消费同一 API 契约（无 DTO 变更）。

- [ ] **Step 1: 先写失败测试（平台版）**

`internal/audit/infrastructure/persistence/change_audit_repo_test.go` 追加：

```go
// 平台版无 SET LOCAL（public 表不走 execTenant）。count → list → users 映射。
func TestPgResourceChangeAuditRepo_ListPlatform(t *testing.T) {
 mock := newChangeAuditMock(t)
 created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
 mock.ExpectBegin()
 mock.ExpectQuery(`SELECT COUNT\(\*\) FROM public\.platform_resource_change_audits`).
  WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
 mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
  WithArgs(20, 0).
  WillReturnRows(pgxmock.NewRows([]string{
   "id", "resource_kind", "resource_id", "operation", "actor_id", "created_at",
   "before_projection", "after_projection",
  }).
   AddRow("p1", "tenant", "tid-1", "create", "u-1", created, []byte(`{}`), []byte(`{"id":"tid-1"}`)))
 mock.ExpectQuery(`SELECT id, COALESCE\(display_name,''\), COALESCE\(github_login,''\)\s+FROM public\.users WHERE id::text = ANY\(\$1\)`).
  WithArgs([]string{"u-1"}).
  WillReturnRows(pgxmock.NewRows([]string{"id", "display_name", "github_login"}).
   AddRow("u-1", "李雷", "lilei"))
 mock.ExpectCommit()

 repo := &PgResourceChangeAuditRepo{pool: mock}
 rows, total, err := repo.ListPlatform(context.Background(), port.ResourceChangeAuditFilter{Limit: 20})
 require.NoError(t, err)
 require.Equal(t, 1, total)
 require.Len(t, rows, 1)
 require.Equal(t, "李雷", rows[0].ActorName)
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgResourceChangeAuditRepo_GetPlatformByID(t *testing.T) {
 mock := newChangeAuditMock(t)
 created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
 mock.ExpectBegin()
 mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
  WithArgs("p1").
  WillReturnRows(pgxmock.NewRows([]string{
   "id", "resource_kind", "resource_id", "operation", "actor_id", "created_at",
   "before_projection", "after_projection",
  }).
   AddRow("p1", "admin", "u-9", "create", "super", created, []byte(`{}`), []byte(`{"userID":"u-9"}`)))
 mock.ExpectQuery(`SELECT id, COALESCE\(display_name,''\)`).
  WithArgs([]string{"super"}).
  WillReturnRows(pgxmock.NewRows([]string{"id", "display_name", "github_login"}))
 mock.ExpectCommit()

 repo := &PgResourceChangeAuditRepo{pool: mock}
 got, err := repo.GetPlatformByID(context.Background(), "p1")
 require.NoError(t, err)
 require.NotNil(t, got)
 require.Equal(t, "super", got.ActorName) // 无 users 行 → actor_id 兜底
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildPlatformAuditWhere_ActorNameUsesExists(t *testing.T) {
 where, args := buildPlatformAuditWhere(port.ResourceChangeAuditFilter{ActorName: "li"})
 require.Contains(t, where, `public.users`)
 require.Contains(t, where, `u.display_name ILIKE`)
 require.Contains(t, where, `r.actor_id ILIKE`)
 require.Len(t, args, 1)

 whereOnly, argsOnly := buildPlatformAuditWhere(port.ResourceChangeAuditFilter{})
 require.Equal(t, `WHERE scope = 'platform'`, whereOnly)
 require.Empty(t, argsOnly)
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go test ./internal/audit/infrastructure/persistence/ -run 'TestPgResourceChangeAuditRepo_ListPlatform|TestPgResourceChangeAuditRepo_GetPlatformByID|TestBuildPlatformAuditWhere' -count=1
```

Expected: FAIL（当前 `row.ActorName = row.ActorID`、`actor_id ILIKE`）。

- [ ] **Step 3: 实现增强**

`internal/audit/infrastructure/persistence/change_audit_repo.go`：

a) `ListPlatform`（142-188）：把 `row.ActorName = row.ActorID`（181 行）改为先收集 actorIDs、循环后统一 `loadActorNames`：

```go
 actorIDs := make([]string, 0, len(result)+1)
 for dbRows.Next() {
  var row port.ResourceChangeAuditRow
  var before, after []byte
  if err := dbRows.Scan(&row.ID, &row.ResourceKind, &row.ResourceID, &row.Operation,
   &row.ActorID, &row.CreatedAt, &before, &after); err != nil {
   return nil, 0, fmt.Errorf("audit: scan platform resource change audit: %w", err)
  }
  row.Before, row.After = json.RawMessage(before), json.RawMessage(after)
  actorIDs = append(actorIDs, row.ActorID)
  result = append(result, row)
 }
 if err := dbRows.Err(); err != nil {
  return nil, 0, fmt.Errorf("audit: iterate platform resource change audits: %w", err)
 }
 if len(actorIDs) > 0 {
  names, err := loadActorNames(ctx, tx, actorIDs)
  if err != nil {
   return nil, 0, err
  }
  for i := range result {
   result[i].ActorName = actorDisplayName(result[i].ActorID, names)
  }
 }
 return result, total, nil
```

b) `GetPlatformByID`（190-214）：212 行 `row.Before, row.After, row.ActorName = json.RawMessage(before), json.RawMessage(after), row.ActorID` 改为：

```go
 row.Before, row.After = json.RawMessage(before), json.RawMessage(after)
 names, err := loadActorNames(ctx, tx, []string{row.ActorID})
 if err != nil {
  return nil, err
 }
 row.ActorName = actorDisplayName(row.ActorID, names)
 return &row, nil
```

> 注意 `err` 复用：`err = tx.QueryRow(...).Scan(...)` 已存在，之后 `names, err := loadActorNames(...)` 用 `:=` 会在新作用域内合法（names 是新变量）；确保 `row.ActorName` 赋值在返回前。

c) `buildPlatformAuditWhere`（248-268）actor_name 分支（255-258）替换为与 `buildChangeAuditWhere` 同构：

```go
 if f.ActorName != "" {
  args = append(args, `%`+f.ActorName+`%`)
  idx := len(args)
  conds = append(conds, fmt.Sprintf(
   `(EXISTS (SELECT 1 FROM public.users u WHERE u.id::text = r.actor_id AND (u.display_name ILIKE $%[1]d OR u.github_login ILIKE $%[1]d)) OR r.actor_id ILIKE $%[1]d)`, idx))
 }
```

- [ ] **Step 4: 运行测试**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go test ./internal/audit/... -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "feat(audit): resolve platform audit actor names and match by display name

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 需求 2 前端 —— 平台审计日志页 + 菜单常显

**Files:**

- Modify: `web/src/app/layout/menu.config.tsx`
- Modify: `web/src/modules/iam/routes.tsx`
- Modify: `web/src/modules/audit/api/audit.api.ts`
- Modify: `web/src/modules/audit/hooks/useAuditListPage.ts`
- Create: `web/src/modules/audit/pages/PlatformAuditPage.tsx`
- Modify: `web/src/modules/audit/components/AuditEventDrawer.tsx`
- Modify: `web/src/constants/index.ts`
- Test: `make fe-lint && make fe-build`

**Interfaces:**

- Consumes: 现有 `resourceChangeAuditsPageSchema`/`resourceChangeAuditSchema`（audit model）
- Produces: `auditApi.listPlatformEvents`/`getPlatformEvent`；`useAuditListPage(fetchers?)`；`PLATFORM_RESOURCE_KIND_OPTIONS`；`AuditEventDrawer` 可选 prop `resourceKindOptions`；路由 `/admin/audit`（`system_admin`）

- [ ] **Step 1: constants 加平台资源类型选项**

`web/src/constants/index.ts`（`RESOURCE_KIND_OPTIONS` 148-156 之后）新增：

```ts
// 与 internal/audit/domain/change_audit.go 对齐：平台级审计资源类型。
export const PLATFORM_RESOURCE_KIND_OPTIONS: { value: string; label: string }[] = [
  { value: 'tenant', label: '租户' },
  { value: 'admin', label: '平台管理员' },
  { value: 'model', label: '模型' },
  { value: 'provider', label: '厂商' },
  { value: 'platform_config', label: '平台配置' },
];
```

- [ ] **Step 2: audit.api.ts 加平台查询**

`web/src/modules/audit/api/audit.api.ts` 新增：

```ts
export interface AuditFilter {
  page?: number;
  page_size?: number;
  from?: string;
  to?: string;
  resource_kind?: string;
  actor_name?: string;
}

  /** GET /admin/audit/platform/events — 平台管理级审计（全局管理员/系统管理员可见）。 */
  listPlatformEvents: async (params?: AuditFilter): Promise<{ rows: ResourceChangeAudit[]; total: number }> => {
    const res = await api.get('/admin/audit/platform/events', { params });
    return resourceChangeAuditsPageSchema.parse(res.data);
  },

  /** GET /admin/audit/platform/events/:id — 单条平台审计详情。 */
  getPlatformEvent: async (id: string): Promise<ResourceChangeAudit> => {
    const res = await api.get(`/admin/audit/platform/events/${id}`);
    return resourceChangeAuditSchema.parse(res.data);
  },
```

> 以该文件现有 schema/类型名为准（`ResourceChangeAudit`、`resourceChangeAuditSchema`、`resourceChangeAuditsPageSchema` 若命名不同则替换；本文件 30 行，含 `listEvents`/`getEvent` 租户版先例）。

- [ ] **Step 3: useAuditListPage 参数化**

`web/src/modules/audit/hooks/useAuditListPage.ts`：签名改为 `useAuditListPage(fetchers?: { listEvents?: typeof auditApi.listEvents; getEvent?: typeof auditApi.getEvent })`，内部：

```ts
const fetchersRef = useRef(fetchers);
const listEvents = fetchersRef.current?.listEvents ?? auditApi.listEvents;
const getEvent = fetchersRef.current?.getEvent ?? auditApi.getEvent;
```

把现有硬编码 `auditApi.listEvents`（35 行）/`auditApi.getEvent`（68 行）替换为局部 `listEvents`/`getEvent`，并让 `load` 依赖 `[setTotal, listEvents]`、`openDetail` 依赖 `[getEvent]`（与原文件一致即可，重点是函数引用可被覆盖）。

- [ ] **Step 4: 新建 PlatformAuditPage**

`web/src/modules/audit/pages/PlatformAuditPage.tsx`（参照 `AuditEventsPage.tsx` 结构）：

```tsx
import { AuditEventsPage } from './AuditEventsPage';
import { auditApi } from '../api/audit.api';
import { useAuditListPage } from '../hooks/useAuditListPage';

import { PLATFORM_RESOURCE_KIND_OPTIONS } from '@/constants';

export function PlatformAuditPage() {
  return (
    <AuditEventsPage
      title="平台审计日志"
      description="平台管理员对租户、管理员、模型与厂商目录的变更记录"
      emptyText="还没有平台操作记录"
      resourceKindOptions={PLATFORM_RESOURCE_KIND_OPTIONS}
      useHook={() => useAuditListPage({ listEvents: auditApi.listPlatformEvents, getEvent: auditApi.getPlatformEvent })}
    />
  );
}
```

> 若 `AuditEventsPage` 不便于参数化（硬编码较多），**最稳妥方案**：复制 `AuditEventsPage.tsx` 为 `PlatformAuditPage.tsx`，把三处 `RESOURCE_KIND_OPTIONS` 改为 `PLATFORM_RESOURCE_KIND_OPTIONS`、标题改为「平台审计日志」、说明改为「平台管理员对租户、管理员、模型与厂商目录的变更记录」、空状态改为「还没有平台操作记录」，并把 `useAuditListPage` 调用改为传 `{ listEvents: auditApi.listPlatformEvents, getEvent: auditApi.getPlatformEvent }`。优先尝试参数化复用，若改动面过大则复制。**两种方案都保留 `AuditEventsPage` 原有行为不变。**

- [ ] **Step 5: AuditEventDrawer 支持自定义资源类型选项**

`web/src/modules/audit/components/AuditEventDrawer.tsx`：props 增加 `resourceKindOptions?: Array<{ value: string; label: string }>`（默认 `RESOURCE_KIND_OPTIONS`）；56 行 `RESOURCE_KIND_OPTIONS.find(...)` 改为 `(resourceKindOptions ?? RESOURCE_KIND_OPTIONS).find(...)`。

- [ ] **Step 6: 菜单常显 + 新增审计日志子项**

`web/src/app/layout/menu.config.tsx`：

a) 删除 `PLATFORM_ROLE_RANK`（28 行）与 `platformRoleRank`/`isPlatformAdmin`/`isGlobalAdmin`（39-41 行）——先确认无其他引用（grep 后决定是否保留）。

b) 157 行 `if (isPlatformAdmin) {` 与对应右括号删除，组对象无条件 `base.push({...})`；保留 38 行 `canManageTenant` 计算与 186 行 `.filter(Boolean)`。

c) `/models`（163-167）、`/admin/admins`（181-185）的 `isGlobalAdmin ? {...} : null` 包裹移除，改为无条件 `{ key, icon, label }`。

d) children 新增审计日志项（在 `/admin/admins` 之后）：

```tsx
          { key: '/admin/audit', icon: <AuditOutlined />, label: '审计日志' },
```

`AuditOutlined` 已在 17 行 import（若无则补）。

- [ ] **Step 7: 路由注册**

`web/src/modules/iam/routes.tsx`：import `PlatformAuditPage`（`../audit/pages/PlatformAuditPage`）；新增 Route：

```tsx
      <Route
        key="admin-audit"
        path="/admin/audit"
        element={
          <PrivateRoute requiredRole="system_admin">
            <PlatformAuditPage />
          </PrivateRoute>
        }
      />
```

- [ ] **Step 8: 前端验证**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance/web
npm run lint
npm run build
```

Expected: 无 lint 错误、build 成功。

- [ ] **Step 9: 提交**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
git add -A
git commit -m "feat(web): add platform audit log page and always-visible admin menu

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 需求 3 前端 —— 平台参数草稿能力

**Files:**

- Modify: `web/src/modules/parameters/pages/PlatformSettingsPage.tsx`

**Interfaces:**

- Consumes: `parametersApi.createDraft(groupKey, snapshot, message)`（已存在，后端无改动）
- Produces: `buildPatch(defs, formValues): PlatformValues` 纯函数；`PlatformTabPanel` prop `onDraftSaved?: () => void`；有分组按钮文案「保存草稿」+ 草稿提示，无分组保留「保存X参数」立即生效

- [ ] **Step 1: 提取 buildPatch 纯函数**

`web/src/modules/parameters/pages/PlatformSettingsPage.tsx`：把 `onFinish` 内 119-133 行的 patch 构造逻辑提取为模块级纯函数（放在 `renderable` 附近）：

```ts
// buildPatch 从表单值构造平台参数 patch：跳过未设置(null/undefined)、embedding_model
// 显式清空提交空串、模型 key 等于定义默认时跳过（由 llmgateway 从目录解析默认）。
const buildPatch = (defs: ParameterDefinition[], formValues: PlatformSettingsFormValues): PlatformValues => {
  const patch: PlatformValues = {};
  for (const def of defs) {
    const v = formValues[def.key];
    if (v === undefined || v === null) continue;
    if (def.visual_hint.control === 'embedding_model') {
      patch[def.key] = v === undefined || v === null ? '' : v;
      continue;
    }
    if (def.visual_hint.control === 'model' && v === def.default) continue;
    patch[def.key] = v;
  }
  return patch;
};
```

- [ ] **Step 2: onFinish 分分支 + onDraftSaved prop**

`PlatformTabPanel` 签名（87-101 行）增加 `onDraftSaved?: () => void`。`onFinish` 改造：

```tsx
  const onFinish = useCallback(
    async (formValues: PlatformSettingsFormValues) => {
      setSaving(true);
      const patch = buildPatch(defs, formValues);
      try {
        if (groupKey) {
          // 有版本分组 → 保存草稿（未生效），发布在版本历史中操作。
          await parametersApi.createDraft(groupKey, patch, '草稿保存');
          message.success({ content: '草稿已保存（未生效）', duration: 2 });
          onDraftSaved?.();
          return;
        }
        // 无版本分组（mcp/rag）→ 立即生效。
        const effective = await parametersApi.update(patch);
        if (effective && typeof effective === 'object') {
          onEffectiveChange(effective);
        }
        message.success({ content: `${categoryLabel(category)}参数已保存`, duration: 2 });
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '保存平台参数失败'), duration: 3 });
        }
      } finally {
        setSaving(false);
      }
    },
    [category, defs, groupKey, onEffectiveChange, onDraftSaved],
  );
```

按钮文案（171-173 行）：

```tsx
          <Button type="primary" htmlType="submit" loading={saving}>
            {groupKey ? '保存草稿' : `保存${categoryLabel(category)}参数`}
          </Button>
```

- [ ] **Step 3: 父级 handleDraftSaved**

`PlatformSettingsPage`（197-200 行 `handleEffectiveChange` 之后）新增：

```tsx
  // 草稿保存只影响版本历史（draft 行出现「发布」），不改变生效值：仅递增
  // versionTick 触发各分组版本历史重拉。
  const handleDraftSaved = useCallback(() => {
    setVersionTick((t) => t + 1);
  }, []);
```

`PlatformTabPanel` 调用处加 `onDraftSaved={handleDraftSaved}`；`tabs` useMemo 依赖数组（257 行）加 `handleDraftSaved`。

- [ ] **Step 4: 前端验证**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance/web
npm run lint
npm run build
```

Expected: 无 lint 错误、build 成功。

- [ ] **Step 5: 提交**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
git add -A
git commit -m "feat(web): separate draft save from publish for versioned platform params

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: 验证与验收

**Files:** 无代码改动；运行全量验证。

- [ ] **Step 1: 后端全量测试**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
go vet ./...
go test -v -race -timeout 30s ./...
```

Expected: 全绿。

- [ ] **Step 2: 前端 lint + build**

```bash
cd /home/yang/go-projects/stratum-platform-admin-enhance
make fe-lint && make fe-build
```

Expected: 通过。

- [ ] **Step 3: 质量门禁**

```bash
make code-quality
make risk-guardrails
bash scripts/quality/dto-residue-guard.sh
```

Expected: 无新增超限函数、无风险守卫违反、无 proto 生成物残留。

- [ ] **Step 4: 系统验收（stratum-e2e-tester）**

在 clean commit 上通过 `stratum-e2e-tester`（`stratum-e2e-development` skill）执行系统验收，运行 `make test-verify-before-pr`。R3 命中项（前后端联调/数据库链路/菜单权限/参数草稿）自动按 `.test/verification.yaml` 升级。e2e 覆盖点：

- 平台管理菜单对普通用户常显，点击 /admin/audit 渲染 403；
- 厂商管理「添加模型」→ 模型目录出现新模型（provider_managed=false）；
- 平台审计日志页列出租户/管理员变更且操作者显示为 display_name；
- 参数页 agent tab「保存草稿」→ 版本历史出现 draft 行「发布」→ 发布后生效。

- [ ] **Step 5: 提交验收产物（如 skill 要求）**

如验收产出 report/attestation 文件需入仓库，一并提交；否则跳过。

```bash
git add -A
git commit -m "test(e2e): add acceptance attestations for platform admin enhancements

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage：**

- 需求 1（spec 3.1/3.2）：Task 2 后端 + Task 3 前端全覆盖。字段范围（模型名+能力必填、窗口/最大输出可选 0=默认）在 `CreateModelInput` 与 AddModelModal 落地。
- 需求 2（spec 4.1-4.5）：Task 1（枚举 + 共享写入）、Task 4（iam 补审计）、Task 5（查询增强）、Task 6（前端页 + 菜单）。spec 4.4 的 LEFT JOIN 被偏差修正 (b) 替换为 loadActorNames。
- 需求 3（spec 5）：Task 7 全覆盖（有分组保存草稿、无分组保留立即保存、发布在版本历史）。
- spec 6 菜单常显 + 403：Task 6 Step 6/7。
- spec 7 测试：Task 8。
- spec 8 影响面：port 签名同步、事务化回滚、共享提取、菜单常显均在任务中处理。

**2. Placeholder scan：** 无 TBD/TODO；每步含完整代码与命令。注意 Task 2 Step 10 与 Task 6 Step 4 标注了两处「以实际文件为准」的弹性点（handler 测试的 tenant 注入 key、AuditEventsPage 参数化 vs 复制），均给出了可执行的两条路径，非占位符。

**3. Type consistency：**

- `CreatePlatform(ctx, m, actorTenantID, audit)` 在 Task 2 Step 4（port）/Step 5（infra）签名一致，service 在 Step 3 调用一致。
- `InsertPlatformAuditTx(ctx, tx, actorTenantID, ev)` Task 1 定义，Task 2 Step 5、Task 4 Step 2/3 调用一致。
- `DeleteTenant(ctx, actorID, id)` 在 Task 4 service/handler/runtime 三处签名一致。
- 前端 `CreateModelInput`（Task 3 Step 1）与 `llmApi.createModel` 参数（Step 2）一致；`refreshTick`/`onModelCreated` prop 命名在 Step 4-6 一致。
- `PLATFORM_RESOURCE_KIND_OPTIONS` 在 Task 6 Step 1/4 一致。

## Execution Handoff

计划已写入。按用户指令「推进到 cd 部署成功」，执行方式为 **Inline Execution（executing-plans）**：本会话按 Task 1→8 顺序实现，每 Task 完成后跑对应测试并提交，最后走 PR → CI → CD 部署链路。
