# 审计日志重构：租户级资源变更审计

日期：2026-08-14
状态：Draft（待用户 review）

## 背景与问题

当前系统存在两套审计数据源，职责重叠且展示错位：

1. **`public.audit_events`（平台级 HTTP 请求审计）**
   - 由 `api/middleware/audit.go` 对每个非 GET 请求写一条，`resource_type` 恒为 `http_request`，action 为 `METHOD + path`。
   - 前端 `/audit` 页面当前展示的是它；菜单挂在「平台管理」分组下，前端路由 + 后端 `RequireGlobalAdmin` 双拦截，仅 `global_admin` 可见。
   - 属于平台运维视角，租户内的业务管理员无法看到本租户的资源变更。

2. **`resource_change_audits`（租户级资源变更审计）**
   - 记录 tenant-managed 资源的语义 CRUD（before/after 安全投影），与业务写在同一事务。
   - 当前覆盖 agent / skill / mcp / knowledge 四类；**workflow 完全没有写入**；evaluation 仅在「发布到生产」时记一条（`promoteChangeAuditTx`），非全量生命周期。
   - 无读取 repository、无 HTTP API、无前端展示。

### 问题归纳

- `/audit` 展示的是平台级 HTTP 审计，租户 admin/owner 看不到本租户业务资源的变更记录。
- 资源变更审计覆盖不全（缺 workflow；evaluation 只记发布）。
- 筛选项（action / risk_level / outcome）面向 HTTP 审计语义，与业务资源审计不匹配。

## 目标

1. 审计独立为**租户级功能**：租户内 `admin`、`owner` 角色可见本租户审计数据（不再限定 `global_admin`）。
2. 租户内资源 CRUD 全量记录：**agent、skill、mcp、知识库（knowledge）、工作流（workflow）、评测（evaluation）** 六类。
3. 筛选项简化为：**时间范围、人员（操作者）、资源类型**。
4. 废弃平台级 HTTP 审计：停写（移除 AuditMiddleware）、删除 `public.audit_events` 表及配套代码。

## 架构决策

| 决策点 | 结论 |
|---|---|
| 唯一审计数据源 | `resource_change_audits`（tenant schema） |
| 写入方式 | 业务事务内，复用既有 `newChangeAudit` + `insertChangeAudit` 模式 |
| 读取边界 | 新建查询 port + repo，走 `execTenant`（tenant schema 隔离） |
| 访问权限 | `middleware.RequireTenantRole("admin")`（owner 自动通过） |
| DTO 契约 | 走 `proto/` → `make proto-gen`（符合仓库 HTTP 参数契约规范） |
| 操作者展示 | 映射 `users.github_login` / `display_name`，不展示 UUID |

## 改动明细

### A. 共享枚举扩展（`internal/audit/domain/change_audit.go`）

```go
const (
    ResourceKindAgent      = "agent"
    ResourceKindSkill      = "skill"
    ResourceKindMCP        = "mcp"
    ResourceKindKnowledge  = "knowledge"
    ResourceKindWorkflow   = "workflow"    // 新增
    ResourceKindEvaluation = "evaluation"  // 新增
)
```

`resource_change_audits.resource_kind` 列无枚举约束，扩展无需 schema 变更。

### B. workflow 写入（`internal/workflow`）

`DefinitionService.Create / Update / Delete`（`internal/workflow/application/service.go`）构造 `ResourceChangeAuditEvent`，在对应 repository 的事务内执行 `insertChangeAudit`。投影字段为无凭证安全投影（name、description 等核心元数据）。

workflow 另有 `Publish`（版本发布），属于 workflow 生命周期，本次一并记录（operation=`publish`），与 evaluation 的 `promote` 语义一致。

### C. evaluation 写入（`internal/evaluation`）

`ExperimentService`（`internal/evaluation/application/experiment_service.go`）生命周期映射：

| 服务方法 | operation |
|---|---|
| `Create` | `create`（新增） |
| `Promote` | `promote`（已有，保留） |
| `Rollback` | `rollback`（新增） |
| `Reject` | `reject`（新增） |
| `Pause` | `pause`（新增） |

评测实验没有 update/delete 操作，operation 直接记录状态流转语义，不再局限 `create/update/delete` 三值。`resource_change_audits.operation` 列无约束。

### D. 读取端（`internal/audit`）

- **port**（`internal/audit/domain/port/`）：`ResourceChangeAuditQuery` 接口，方法 `List`（返回分页 + total）、`GetByID`。
- **repo**（`internal/audit/infrastructure/persistence/`）：`PgResourceChangeAuditRepo`
  - 查询 `resource_change_audits`，走 `execTenant` 租户边界，fail-closed（tenantID 缺失即拒绝）。
  - 筛选条件：`resource_kind`、`actor_id`、`created_at` 范围（from/to）、分页（limit/offset）。
  - `ORDER BY created_at DESC`。
  - 详情返回 `before_projection` / `after_projection`。
- **actor 映射**：List 收集 `actor_id` 集合，批量查询 `public.users` 得 `github_login`/`display_name`，组装为可读操作者名；展示优先级 `display_name` > `github_login` > 原始 actor_id 兜底（system actor 无对应 user 行，直接显示 actor_id）。

### E. HTTP API + proto 契约

- 新建 `proto/audit/audit.proto`（package `stratum.audit`）：
  - `ListResourceChangeAuditsRequest`：`resource_kind`、`actor_id`、`from`、`to`、`page`、`page_size`
  - `ResourceChangeAudit`：`id`、`resource_kind`、`resource_id`、`operation`、`actor_id`、`actor_name`、`created_at`、`before`、`after`
  - 响应：`events` + `total`
- 运行 `make proto-gen` 生成 DTO（`api/http/dto/gen/`、`web/src/services/gen/`）。
- **handler**（`api/http/handler/audit_handler.go`）：`ListEvents` / `GetEvent` 重构，绑定新 DTO。
- **路由**（`api/http/router.go` `registerAudit`）：移除 `RequireGlobalAdmin`，改用 `RequireTenantRole("admin")`。

### F. 废弃平台级 HTTP 审计

- 删除 `api/middleware/audit.go`；移除 `api/http/router.go:38` 的 `AuditMiddleware` 挂载。
- 删除 `internal/audit` 中的 HTTP 审计代码：
  - `domain/audit.go` 的 `AuditEvent` / `AuditFilter` / `AuditActor` / `ActorType` 等（保留 `change_audit.go`）。
  - `application/audit_service.go` 的 Recorder 路径。
  - `infrastructure/persistence/audit_repo.go` 的 HTTP 事件 CRUD。
- `api/wiring/audit.go` 精简为仅查询服务；删除 `AuditCleanupWorker` 及 `AuditRetentionDays` / `AuditCleanupInterval` 常量。
- **迁移**：新增 `pkg/migration/sql/NNN_drop_audit_events.up.sql`（`DROP TABLE IF EXISTS public.audit_events`）+ down（重建表，结构同 031）。破坏性迁移，单独审查与验证。

### G. 前端（`web/src/modules/audit/`）

- `AuditEventsPage`：展示资源变更审计列表（时间 / 操作者 / 资源类型 / 操作 / 资源 ID）。
- 筛选表单：**时间范围、操作者、资源类型（六类下拉）**；移除 action / risk_level / outcome。
- 详情 drawer（`AuditEventDrawer`）：before / after 安全投影 + 操作者 + 时间。
- API client / model：改用 `web/src/services/gen/` 生成类型，统一走 `client.ts`。
- **菜单**（`menu.config.tsx`）：从 `platform-admin-group` 移出，作为顶层独立菜单项置于「团队」分组之后（租户管理区），可见性 `canManageTenant`（admin/owner）；`resolveOpenKeys` 同步。

## 测试

- repo：filter（resource_kind / actor_id / 时间范围）、分页、`execTenant` 租户边界 fail-closed、actor 映射。
- handler：契约测试 + `testdata/contracts/*.golden.json` 更新。
- workflow / evaluation：service 层变更审计写入测试（含 operation 语义断言）。
- 前端：`useAuditListPage` 等 hook 测试更新。
- 系统验收：走 `stratum-e2e-development` skill（无头浏览器，admin/owner 可见性、筛选、CRUD 落库对账）。

## 风险与迁移

- **删表破坏性**：`public.audit_events` 历史数据丢失。已在需求阶段确认「废弃，停写删表」。删表前确认代码引用清零、E2E 覆盖。
- **proto-gen 影响面**：新增 proto 文件需完整 `make proto-gen`；契约 golden 同步更新。
- **存量数据**：各租户 `resource_change_audits` 已有历史数据在新页面直接可见，无需数据迁移。
- **role 语义**：`RequireTenantRole("admin")` 的层级语义（owner ≥ admin ≥ member）需与前端 `canManageTenant` 保持一致，用 `require_role_test.go` 现有测试兜底。

## 非目标

- 不保留平台级 HTTP 请求审计的任何替代入口。
- 定时任务（scheduled task）不单独作为资源类型（属 workflow 域）。
- 不做审计记录的删除、导出、或自定义保留策略。
