# 平台管理增强：手动添加模型 · 平台审计日志 · 参数草稿能力

- 日期：2026-08-28
- 状态：已批准（待实现）
- 范围：厂商管理、平台管理审计、平台参数编辑三个平台管理侧增强

## 1. 背景与目标

三个独立的平台管理侧需求：

1. **厂商管理手动添加模型**：当前模型仅能通过「发现模型」（provider API 主动发现）进入目录，全部标记 `provider_managed=true`，可能覆盖不全。需要支持管理员为厂商**手动添加模型**。
2. **平台管理审计日志**：平台管理下的租户 CRUD、平台管理员增删等操作目前**无审计留痕**。平台级审计表 `platform_resource_change_audits` 与查询 API `/admin/audit/platform/events` 已存在，但**无前端页面**。
3. **平台参数草稿能力**：平台参数「保存」即发布生效，无草稿能力。需要类似工作流的 draft → publish 机制。

## 2. 决策记录

| 决策点 | 结论 |
|---|---|
| 手动模型字段范围 | 模型名 + 能力（必填），上下文窗口 / 最大输出可选（0=默认），其余走默认，之后可在模型管理编辑 |
| 平台审计范围 | 全部平台操作统一展示：租户创建/暂停/删除、平台管理员增删、模型/provider 目录变更 |
| 参数草稿交互 | 保存草稿 + 发布分离（发布在版本历史中操作） |
| 平台管理菜单可见性 | 分组对**所有用户**常显，普通用户点击后由路由守卫拦截渲染 403「您没有访问此页面的权限。」 |

## 3. 需求 1：厂商管理手动添加模型

### 3.1 后端

**`ModelMgmtService.Create`（新增）** — `internal/llmgateway/application/model_mgmt_service.go`

```go
type CreateModelInput struct {
    ProviderID    string
    Name          string
    Capabilities  []domain.ModelCapability
    ContextWindow int // 0 = 默认
    MaxTokens     int // 0 = 默认
}

func (s *ModelMgmtService) Create(ctx context.Context, actorID, tenantID string, in CreateModelInput) (*domain.Model, error)
```

- 构造 `domain.Model`：`ProviderManaged=false`、`ContextWindowSource/MaxTokensSource = CapabilitySourceManualUnknown`、`Enabled=true`、`Recommended=false`、能力 = 用户所选（多选非空）。
- 校验（fail-closed）：
  - `Name` 非空、`Capabilities` 非空；
  - Provider 存在（`ProviderRepository.Get`，不存在返回错误）；
  - provider 内 name 不重复（依赖 `public.models` 唯一约束，应用层不预先查询，DB 冲突错误向上传播）；
  - 窗口/最大输出仅当 >0 才写，否则 0 走默认。
- 审计：`newChangeAudit`（`internal/llmgateway/application/change_audit.go`）构造 `ResourceKind=model`、`Operation=create`、`After=modelSafeProjection`、`ActorID=actorID`，走平台审计。

**`PlatformModelRepository.CreatePlatform`（新增）** — `internal/llmgateway/domain/port/model_repo.go`

```go
CreatePlatform(ctx context.Context, m *domain.Model, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
```

实现 `internal/llmgateway/infrastructure/model_repo.go`：public 事务（`pool.Begin` → `INSERT INTO public.models` → 平台审计 → `Commit`），完整复用 `UpdatePlatform` 现有事务模式。INSERT 逐列与 `public.models` DDL 核对（含 `capabilities` 数组、`sampling_params`、`max_temperature` 等默认值）。

**Handler 与路由** — `api/http/handler/model_mgmt_handler.go` + `api/http/router.go`

- 新增 `ModelMgmtHandler.Create`：bind `CreateModelDTO` → 获取 tenant → 调 service → 渲染；`c.Error(err)` 统一错误中间件。
- 路由 `POST /admin/models` 挂 `adminMW := middleware.RequireGlobalAdmin()`（与现有 models mutation 路由一致，`router.go:676-705`）。
- HTTP JSON 参数契约：改 `proto/` 下对应 .proto → `make proto-gen` 生成 DTO。

### 3.2 前端

- `web/src/modules/llm/api/llm.api.ts` 新增 `createModel(payload)`。
- `web/src/modules/llm/pages/ProviderListPage.tsx` 每行新增「添加模型」按钮 → `AddModelModal`（Modal 命名 `createOpen`/`createLoading`）：
  - 表单：厂商（预置当前行，只读）、模型名（必填）、能力（多选 Checkbox/Select）、上下文窗口（可选 InputNumber）、最大输出（可选 InputNumber）。
  - 提交：`createModel` → `message.success` 刷新模型目录。
- 兼容性：再次 discover 不会禁用手动模型——`UpsertDiscovered` 的 disable phase 只作用于 `provider_managed=true`（已有行为，无改动）。

## 4. 需求 2：平台管理审计日志

### 4.1 审计枚举扩展 — `internal/audit/domain/change_audit.go`

新增 ResourceKind：

```go
ResourceKindTenant = "tenant" // 租户生命周期（创建/更新/删除）
ResourceKindAdmin  = "admin"  // 平台管理员角色变更
```

### 4.2 平台审计写入共享化（横切能力）

现状 `insertPlatformAuditTx` 是 `internal/llmgateway/infrastructure/platform_audit.go` 的私有函数（40 行纯函数，依赖 `auditdomain.PlatformChangeAuditInsertSQL`），provider_repo / model_repo 共用。

需求 2 需要 iam 具备相同能力。为避免复制导致 SQL 链路漂移，**提取共享实现**：

- 新增 `internal/audit/infrastructure/persistence/insert_platform_audit.go`，导出
  `InsertPlatformAuditTx(ctx, tx, actorTenantID string, ev *auditdomain.ResourceChangeAuditEvent) error`（内容即现 `insertPlatformAuditTx`，含 `Normalized()`、V7 事件 ID、actor_tenant_id NULL 处理）。
- `internal/llmgateway/infrastructure/platform_audit.go` 删除私有副本，改为调用共享函数（行为不变）。
- iam 与 llmgateway 的 infrastructure 均调用共享实现。

> 架构说明：此为「禁止 import 兄弟 context 的 infrastructure」的唯一例外。理由：审计写入是横切基础设施能力，归 audit context 所有；SQL 列契约已由 `PlatformChangeAuditInsertSQL` 单点定义，共享执行脚手架避免两份实现漂移。若评审不接受，退化方案为 iam infra 内复制同构私有函数（复用同一 SQL 常量，契约不漂移）。

### 4.3 iam 平台管理操作补审计

**port 扩展** — `internal/iam/domain/port/admin_tenant_repo.go`、`admin_user_repo.go`

- `AdminTenantRepo`：

  ```go
  Create(ctx, t domain.Tenant, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
  UpdatePatch(ctx, id string, patch domain.TenantPatch, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
  HardDelete(ctx, id string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
  ```

- `AdminUserRepo`：

  ```go
  SetAdminRole(ctx, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
  RemoveAdminRole(ctx, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
  ```

> 复用 llmgateway 先例：`domain/port` 直接使用 `auditdomain.ResourceChangeAuditEvent` 类型（`llmgateway/domain/port/model_repo.go:6,36`）。

**实现事务化** — `internal/iam/infrastructure/persistence/admin_tenant_repo.go`、`admin_user_repo.go`

- `Create`：`pool.Begin` → `INSERT INTO public.tenants` → `InsertPlatformAuditTx` → `Commit`。
- `UpdatePatch`：`pool.Begin` → `SELECT` 旧值 → `UPDATE` → 审计（before=旧投影，after=新投影）→ `Commit`。
- `HardDelete`：`pool.Begin` → `SELECT` 旧值 → `DELETE` → 审计（before=旧投影，after=nil）→ `Commit`。
- `SetAdminRole`/`RemoveAdminRole`：同模式，`ResourceKind=admin`，after 记录 `{userID, role}`。
- 租户投影脱敏：仅 `id/name/slug/plan/status`（无凭据）。actor_tenant_id：global admin 平台操作传空 → NULL（符合 `PlatformChangeAuditInsertSQL` 语义）。

**service 调整** — `internal/iam/application/admin_service.go`

- `CreateTenant(ctx, actorID, name, slug, plan, status)`、`UpdateTenant(ctx, actorID, id, patch)`、`DeleteTenant(ctx, actorID, id)` 增加 actorID 参数，构造审计事件传入 repo。`SetAdminRole`/`RemoveAdminRole` 已带 actorID，补审计构造。
- `CreateTenant` 的 `ProvisionSchema` 与 `tenantProvisionedHook` 保持在事务外（审计只覆盖 tenants 行插入，与 schema provision 解耦）。

**handler 传递 actorID** — 相应 iam handler 从 `reqctx` 取当前用户 id 传给 service。

### 4.4 查询增强 — `internal/audit/infrastructure/persistence/change_audit_repo.go`

`ListPlatform` 当前 `row.ActorName = row.ActorID`（显示 actor_id 的 uuid）。增强：

- `LEFT JOIN public.users u ON u.id = a.actor_id`，`ActorName = COALESCE(u.display_name, a.actor_id)`（system actor 无 users 行时 fallback 到 actor_id）。
- 不改变表结构与 API 契约；DTO 不变。

### 4.5 前端

- `web/src/app/layout/menu.config.tsx`：平台管理组新增「审计日志」子项 → `/admin/audit`（icon `AuditOutlined`）。
- `web/src/modules/iam/routes.tsx`：新增

  ```tsx
  <Route path="/admin/audit" element={<PrivateRoute requiredRole="system_admin"><PlatformAuditPage /></PrivateRoute>} />
  ```

- 新增 `web/src/modules/audit/pages/PlatformAuditPage.tsx`（参照 `AuditEventsPage.tsx`）：
  - `audit.api.ts` 新增 `listPlatformEvents(params)` 调 `/admin/audit/platform/events`（资源类型、actor、from/to 过滤）。
  - 列表：时间 / 操作者（display_name）/ 资源类型 / 操作 / 资源 ID；资源类型过滤（tenant/admin/model/provider/platform_config）。
  - 行展开或详情抽屉展示 before/after 投影（脱敏后 JSON）。
  - 空状态：Empty + 「还没有平台操作记录」。

## 5. 需求 3：平台参数草稿能力

后端无需改动（`CreateDraft`/`Publish`/`Rollback`/`Versions` 已完备）。前端改造 `web/src/modules/parameters/pages/PlatformSettingsPage.tsx`：

- `VERSION_GROUPS = new Set(['agent', 'memory', 'evaluation', 'trace'])`（已有）。
- **有版本分组**（`groupKey = category` 非 null）：表单「保存」改为「保存草稿」——调 `createDraft(groupKey, { snapshot, message })`，提示 `message.success('草稿已保存（未生效）')`；**移除「保存（立即生效）」**。发布/回滚在 `VersionHistory.tsx`（已有「发布」「回滚」按钮）操作。
- **无版本分组**（`mcp`/`rag`，`groupKey = null`）：**保留「保存（立即生效）」**——`createDraft` 对无 group 返回 `domain.ErrGroupNotFound`（`lockGroup` 查不到 `platform_config_groups` 行，`platform_repo.go:454-464`），不能走草稿。
- 草稿保存成功后刷新版本历史，draft 行出现「发布」按钮。
- 交互常量、Modal 命名、loading 命名遵循前端规范。

## 6. 平台管理菜单可见性策略（贯穿）

`web/src/app/layout/menu.config.tsx`：

- 去掉 `isPlatformAdmin` 条件，`platform-admin-group` 对**所有用户**常显。
- 去掉子项的 `isGlobalAdmin` 过滤：模型管理 / 全局租户 / 平台参数 / 平台管理员 / 审计日志全部常显。
- 保留 `filter(Boolean)` 兜底结构。
- 点击拦截由路由层 `PrivateRoute` 承担（未达标渲染 403 Result「您没有访问此页面的权限。」）：
  - `/models` → `global_admin`、`/admin/admins` → `global_admin`（已有）
  - `/admin/tenants` → `system_admin`、`/admin/settings` → `system_admin`（已有）
  - `/admin/audit` → `system_admin`（新增）
- 删除现已冗余的 `isPlatformAdmin`/`isGlobalAdmin` 计算变量（若其它位置无引用）。

> 边界：`PrivateRoute` 中「无 `current_tenant` → 重定向 `/onboarding`」先于 `requiredRole` 检查。普通用户（member）通常有 `current_tenant`，会正确看到 403；无租户的新用户仍走 onboarding，属既有行为，不在本次范围。

## 7. 测试与验证

三个需求均命中「完整测试门槛」（功能改动 + 前后端联调 + 数据库链路）。

**后端**

- 需求 1：`ModelMgmtService.Create` unit（校验分支、手动模型字段、审计构造）；`CreatePlatform` integration（public 事务同事务审计，参照 `platform_audit_integration_test.go` 的 P2 断言）；contract（`POST /admin/models`）。
- 需求 2：iam `AdminService` 事务化后 port mock 同步更新（修改 port 后立即搜索并同步所有 test mock/stub）；integration 覆盖租户 create/update/delete、admin role 变更写平台审计；`ListPlatform` join 名称断言。
- `go vet && go test -short ./...`；PR 前 `go test -v -race -timeout 30s ./...`。

**前端**

- `make fe-lint && make fe-build`。
- 菜单常显 + 403 拦截、参数草稿保存/发布为 e2e 覆盖点（无头浏览器）。

**验收**

- 创建 PR 前在 clean commit 上通过 `stratum-e2e-tester`（`stratum-e2e-development` skill）完成系统验收，`make test-verify-before-pr`；按 `.test/verification.yaml` 风险级升级。

## 8. 影响面与风险

| 项 | 影响 | 风险与对策 |
|---|---|---|
| `AdminTenantRepo`/`AdminUserRepo` port 签名变更 | 触碰 iam infra + 全部测试 mock | 修改后立即搜索同步 mock/stub；`go test ./internal/iam/...` 全绿 |
| iam 平台操作事务化 | 从独立 Exec 改为 Begin/Commit | 必须验证事务回滚与失败传播；新增覆盖失败路径测试 |
| `insertPlatformAuditTx` 提取共享 | llmgateway infra 行为不变（纯搬移） | 复用同一 SQL 常量，契约不漂移；llmgateway 平台审计 integration 回归 |
| `POST /admin/models` 新契约 | proto 契约变更 → `make proto-gen` | 生成物不入 git；`dto-residue-guard.sh` 守卫残留 |
| 平台管理菜单常显 | 普通用户侧边栏出现平台管理入口 | 路由守卫已全部就绪，点击 403，无数据泄露面 |
| 参数草稿化 | 有分组 tab 不再「保存即生效」 | 无分组 tab 保留立即保存；发布走版本历史（已有能力） |
