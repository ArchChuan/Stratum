# 审计日志重构：租户级资源变更审计

日期：2026-08-14
状态：Draft（已过 4 维度并行 review：架构 / 安全 / 实现可行性 / 前端；P0/P1 修订已合入，待用户 review）

## 背景与问题

当前系统存在两套审计数据源，职责重叠且展示错位：

1. **`public.audit_events`（平台级 HTTP 请求审计）**
   - 由 `api/middleware/audit.go` 对每个非 GET 请求写一条，`resource_type` 恒为 `http_request`，action 为 `METHOD + path`。
   - 前端 `/audit` 页面当前展示的是它；菜单挂在「平台管理」分组下，前端路由 + 后端 `RequireGlobalAdmin` 双拦截，仅 `global_admin` 可见。
   - 属于平台运维视角，租户内的业务管理员无法看到本租户的资源变更。

2. **`resource_change_audits`（租户级资源变更审计）**
   - 记录 tenant-managed 资源的语义 CRUD（before/after 安全投影），与业务写在同一事务。
   - 当前覆盖 agent / skill / mcp / knowledge 四类；**workflow 完全没有写入**；evaluation 仅「发布到生产」时记一条且语义错误（见 C）。
   - 无读取 repository、无 HTTP API、无前端展示。

### 问题归纳

- `/audit` 展示的是平台级 HTTP 审计，租户 admin/owner 看不到本租户业务资源的变更记录。
- 资源变更审计覆盖不全（缺 workflow；evaluation 只记发布、operation/actor/kind 语义均错）。
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
| 写入方式 | 业务事务内，复用既有 `newChangeAudit` + `insertChangeAudit` 模式；写入边界统一 `safetext.RedactCredentials` 兜底 |
| 读取边界 | 新建查询 port + repo，走 `execTenant`（tenant schema 隔离），空 tenantID 一律 fail-closed |
| 访问权限 | `middleware.RequireTenantRole("admin")`（owner 自动通过） |
| DTO 契约 | 走 `proto/` → `make proto-gen`（符合仓库 HTTP 参数契约规范） |
| 操作者展示 | 映射 `users.github_login` / `display_name`，不展示 UUID；system actor 展示 actor_id 原文 |
| 操作者筛选 | 参数为 `actor_name` 子串模糊匹配（服务端 JOIN `public.users`），不做 UUID 输入 |

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

    ChangeOpCreate   = "create"
    ChangeOpUpdate   = "update"
    ChangeOpDelete   = "delete"
    ChangeOpPublish  = "publish"   // 新增：workflow 版本发布
    ChangeOpPromote  = "promote"   // 新增：evaluation 发布
    ChangeOpRollback = "rollback"  // 新增：evaluation 回滚
    ChangeOpReject   = "reject"    // 新增：evaluation 拒绝
    ChangeOpPause    = "pause"     // 新增：evaluation 暂停
    ChangeOpActivate = "activate"  // 新增：evaluation 激活 pending 实验
)
```

`resource_change_audits.resource_kind` / `.operation` 列均无枚举约束，扩展无需 schema 变更。前端资源类型下拉与操作 label 引用同一组值。

### B. workflow 写入（`internal/workflow`）

`DefinitionService.Create / Update / Delete / Publish`（`internal/workflow/application/service.go`）在既有事务内构造 `ResourceChangeAuditEvent` 并执行 `insertChangeAudit`，审计写入与业务写同事务（失败不落审计）。

- **actor 来源**：四方法签名显式加 `actorID string`，由 handler 层 `workflowActor(c)`（`api/http/handler/workflow_handler.go:343-354`，取 `auth.sub`+role）传入；**禁止从 command/request body 读取 actor**。
- **投影白名单**：仅 `id`、`name`、`description` 核心元数据；**禁止整体序列化 `Definition.Spec` / `InputSchema` / 节点配置**（其中可嵌第三方密钥）。
- **Delete**：service 层先 `GetDefinition` 加载得到 before 投影，再与 `DeleteDefinition` 同事务写审计；加载失败或删除失败则不落审计（`workflow_versions` FK 为 `ON DELETE RESTRICT`，有版本的 definition 删除会在事务内失败并整体回滚）。
- `Publish` 记录 `operation=publish`（版本发布，属 workflow 生命周期），与 evaluation 的 `promote` 语义对齐。
- **port 级联**：`DefinitionRepository` / `VersionRepository`（`internal/workflow/domain/port/repositories.go`）的写入方法加 audit 事件参数；同步更新全部 test mock 与 `api/http/contract_test.go` 的 contract stub。

### C. evaluation 写入（`internal/evaluation`）

`ExperimentService`（`internal/evaluation/application/experiment_service.go`）生命周期映射，**审计行的 resource_kind 恒为 `"evaluation"`、resource_id 为 `experiment.ID`**（评测操作本身是审计对象，不按被测资源类型归类）；投影仅 `resource_id`、被测资源 kind/id、status 流转（不携带 revision 载荷、评测指标明细）：

| 服务方法 | operation | 说明 |
|---|---|---|
| `Create` | `create` | 新增写入；`CreateExperimentInput` 加 `ActorID` |
| `Enqueue` | `create` | 新增写入（创建 pending 实验）；`CreateExperimentInput` 加 `ActorID` |
| `Activate` | `activate` | 新增写入（pending→running） |
| `Promote` | `promote` | **改造既有写入点，非保留**（见下） |
| `Rollback` | `rollback` | 新增写入 |
| `Reject` | `reject` | 新增写入 |
| `Pause` | `pause` | 新增写入 |

- **`promoteChangeAuditTx` 是待改造点**（`internal/evaluation/infrastructure/persistence/experiment_repository.go:448-466`）：
  - operation `update` → `promote`；
  - actor 硬编码 `"evaluation-worker"`（system）→ 取命令 `input.ActorID`（`ExperimentCommandInput.ActorID` 已存在，Promote/Rollback/Reject/Pause/Activate 命令路径复用），system worker 场景显式传 `reqctx.SystemActorFromContext` 类型；
  - resource_kind 按被测资源类型映射（skill→skill 其余落 agent）→ **统一 `auditdomain.ResourceKindEvaluation` + `experiment.ID`**（消除 mechanism 等归类错误）；
  - before/after 恒 `{}` → 给出安全投影。
- **actor 来源**：命令路径（Promote/Rollback/Reject/Pause/Activate）复用 `ExperimentCommandInput.ActorID`（handler 从 `userIDFromCtx`/`auth.sub` 填充）；`Create`/`Enqueue` 给 `CreateExperimentInput` 加 `ActorID` 字段并同源填充。**禁止从 request body 读取 actor**。
- 状态流转写入在 `applyCommand` 事务内完成（`experiment_repository.ApplyCommand` 同事务），失败不落审计。

### D. 读取端（`internal/audit`）

- **port**（`internal/audit/domain/port/`）：`ResourceChangeAuditQuery` 接口，方法 `List`（分页 + total）、`GetByID`。**List 与 GetByID 的 tenant_id 谓词必须恒存在，禁止条件性追加**。
- **repo**（`internal/audit/infrastructure/persistence/`）：`PgResourceChangeAuditRepo`
  - 查询 `resource_change_audits`，走 `execTenant` 租户边界。
  - **空 tenantID 一律 fail-closed**：`List`/`Count`/`GetByID` 入口第一行 `if tenantID == "" { return error }`（照抄 `audit_repo.go:194-197` 既有先例）；禁止复制旧 `buildAuditFilter` 的「空租户省略谓词」模式——那是跨租户泄露面。
  - 筛选条件：`resource_kind`、`actor_name`（子串模糊匹配，见下）、`created_at` 范围（from/to）、分页（limit/offset）；`ORDER BY created_at DESC`。
  - 详情返回 `before_projection` / `after_projection`。
- **actor 映射与筛选**：
  - List 收集**当前分页返回行**的 `actor_id` 集合，批量查询 `SELECT id, COALESCE(display_name,''), COALESCE(github_login,'') FROM public.users WHERE id = ANY($1)`；**schema-qualified `public.users`**（`execTenant` 内 search_path 含 public，显式限定防未来 shadow）；**只取 display_name / github_login 两列，不返回 email**。
  - 展示优先级 `display_name` > `github_login` > 原始 actor_id 兜底（system actor 无对应 user 行，直接显示 actor_id，如 `evaluation-worker`）。
  - `actor_name` 筛选在 SQL 层做子串匹配：`EXISTS (SELECT 1 FROM public.users u WHERE u.id = r.actor_id AND (u.display_name ILIKE '%'||$n||'%' OR u.github_login ILIKE '%'||$n||'%')) OR r.actor_id ILIKE '%'||$n||'%'`（覆盖 system actor 原文匹配）。**禁止对请求参数直接做 `public.users` 存在性探测**。
- **handler 层**：租户获取改用 `tenantIDFromCtx`（空字符串返回 false，`api/http/handler/tenant.go:12-18`），**废弃宽容版 `tenantIDFromGinKey`**。

### E. HTTP API + proto 契约

- 新建 `proto/audit/audit.proto`（package `stratum.audit`）：
  - `ListResourceChangeAuditsRequest`：`resourceKind`、`actorName`、`from`、`to`、`page`、`pageSize`
  - `ResourceChangeAudit`：`id`、`resourceKind`、`resourceId`、`operation`、`actorId`、`actorName`、`createdAt`、`before`、`after`
  - 响应：`events` + `total`
  - 字段命名采用 camelCase（对齐 `proto/skill/skill.proto` 风格，`protoc-gen-ginstruct` 以 proto 字段名原文生成，无大小写转换）。
- 运行 `make proto-gen` 生成 DTO（`api/http/dto/gen/`、`web/src/services/gen/`）。
- **handler**（`api/http/handler/audit_handler.go`）：`ListEvents` / `GetEvent` 重构，绑定新 DTO，删旧 `parseAuditFilter`。
- **路由**（`api/http/router.go` `registerAudit`）：移除 `RequireGlobalAdmin`，改用 `RequireTenantRole("admin")`。
- **契约测试**：**新增** audit List/GetByID 契约用例 + 生成 `testdata/contracts/*.golden.json`（当前无 audit golden，非更新）。

### F. 废弃平台级 HTTP 审计

- 删除 `api/middleware/audit.go`（含 `audit_test.go`）；移除 `api/http/router.go:38` 的 `AuditMiddleware` 挂载。
- 删除 `internal/audit` 中的 HTTP 审计代码：
  - `domain/audit.go` 的 `AuditEvent` / `AuditFilter` / `AuditActor` / `ActorType` 等（保留 `change_audit.go`）。
  - `application/audit_service.go` 的 Recorder 路径。
  - `infrastructure/persistence/audit_repo.go` 的 HTTP 事件 CRUD（含 `audit_repo_mock_test.go`）。
- `api/wiring/audit.go` 精简为仅查询服务；删除 `AuditCleanupWorker` 及 `AuditRetentionDays` / `AuditCleanupInterval` 常量。
- **引用清理清单**（破坏性迁移前必须全部清零）：
  - `cmd/server/runtime.go:191` `registerAuditCleanup` 启动 AuditCleanupWorker；
  - `api/http/contract_test.go:155-156` wiring `Audit.Recorder` + `contractAuditRepo`；
  - `scripts/record-contracts.go` 同款旧接线；
  - `api/http/audit_smoke_test.go`（smokeAuditRepo + 冒烟 AuditMiddleware）；
  - `api/wiring/audit_test.go`（stubAuditRepo + `TestNewAuditCleanupWorker_Construction`）；
  - `api/http/handler/audit_handler_test.go`（旧 `parseAuditFilter` 测试）；
  - `web/e2e/stateful/packs/audit.ts`（查询 `public.audit_events`，改为查 tenant schema 的 `resource_change_audits` + 新筛选断言）；
  - `web/src/services/e2e-surface.json`（列 `/audit`，改路由语义后确认）。
- **迁移**：新增 `pkg/migration/sql/036_drop_audit_events.up.sql`（`DROP TABLE IF EXISTS public.audit_events`）+ down（按 031 结构重建**空表**）。**历史数据不可回滚**——纯 DROP，需在 PR 描述写明「已确认无合规保留需求」；如有归档需求先 `pg_dump` 或 rename 为 archive 表再评。破坏性迁移，单独审查与验证。

### G. 前端（`web/src/modules/audit/`）

- **路由守卫**：`routes.tsx` 改 `requiredTenantRole="admin"`（owner 自动通过，对齐后端 `RequireTenantRole` 层级语义），移除 `requiredRole="global_admin"`；`PrivateRoute` 若尚不支持 tenant-role 需补（复用 `/approvals` 的既有机制）。
- **菜单**（`menu.config.tsx`）：从 `platform-admin-group` 移出，作为顶层独立菜单项置于「团队」分组之后（租户管理区），可见性 `canManageTenant`（admin/owner）；`resolveOpenKeys('/audit')` 返回 `[]`。同步更新 `menu.config.test.tsx`（新增「租户 admin/owner 可见、member 不可见、global_admin 以 member 角色不可见」用例）。
- `AuditEventsPage`：展示资源变更审计列表（时间 / 操作者 / 资源类型 / 操作 / 资源 ID）。
- 筛选表单：**时间范围、操作者（纯文本输入框，服务端子串匹配）、资源类型（六类下拉）**；移除 action / risk_level / outcome。
- **常量**：新增 `RESOURCE_KIND_OPTIONS` 到 `web/src/constants/index.ts`（六类），页面引用；operation label 映射表同步。
- 详情 drawer（`AuditEventDrawer`）：before / after 安全投影 + 操作者 + 时间；`DiffBlock` 复用（JSONB 投影 gen 类型为 `unknown`，无需改造）。
- API client / model：`model/audit.ts` **全量重写**（删旧 `actor/action/resource_type/risk_level/outcome` 字段），以 `web/src/services/gen/` 生成类型为响应类型 + zod 运行时校验（对齐 `skill.api.ts` 模式）；统一走 `client.ts`。
- 测试：`useAuditListPage.test.ts`、`audit.api.test.ts` 按新契约重写。

## 测试

- repo：filter（resource_kind / actor_name / 时间范围）、分页、`execTenant` 租户边界、**空 tenantID 的 List/Count/GetByID 报错用例**、actor 映射（display_name/github_login 优先级、system actor 兜底）。
- handler：契约测试 + 新增 `testdata/contracts/*.golden.json`。
- workflow / evaluation：service 层变更审计写入测试（含 operation 语义断言、actor 来源、投影白名单）。
- 前端：`useAuditListPage`、`audit.api`、`menu.config` 测试更新。
- 系统验收：走 `stratum-e2e-development` skill（无头浏览器，admin/owner 可见性、筛选、CRUD 落库对账，`web/e2e/stateful/packs/audit.ts` 重写为查 `resource_change_audits`）。

## 风险与迁移

- **删表破坏性**：`public.audit_events` 历史数据丢失且不可回滚。已在需求阶段确认「废弃，停写删表」。删表前引用清零（F 清单）、E2E 覆盖。
- **写端 actor 级联**：workflow 四方法 + evaluation Create/Enqueue 加 `actorID` 参数、port 与全部 mock/contract stub 同步，是本次最大工作量。
- **proto-gen 影响面**：新增 proto 文件需完整 `make proto-gen`；契约 golden 新增。
- **存量数据**：各租户 `resource_change_audits` 已有历史数据在新页面直接可见，无需数据迁移。
- **role 语义**：`RequireTenantRole("admin")` 的层级语义（owner ≥ admin ≥ member）需与前端 `requiredTenantRole` 保持一致；JWT role 快照最长 72h（`AccessTokenTTL`），降权/移除用户直到 token 过期仍持读权限，属既有系统性问题的沿用，本设计文档化该边界。
- **PII 边界**：租户 admin 可见「曾操作本租户资源者的全局 display_name / github_login」，含已退租用户；审计归因所需，属预期，代码不返回 email。
- **skill 投影含 instructions 全文**（既有面，非本次新增）：instructions 是租户自写内容，若含密钥会随审计暴露给同租户 admin；写入边界统一加 `safetext.RedactCredentials` 兜底。

## 非目标

- 不保留平台级 HTTP 请求审计的任何替代入口。
- 定时任务（scheduled task）不单独作为资源类型（属 workflow 域）。
- 不做审计记录的删除、导出、或自定义保留策略。
- 不新增审计记录的自定义索引（`(tenant_id, actor_id)` 组合索引暂不加，actor_name 筛选经 `public.users` JOIN + 租户表内 actor_id 扫描，数据量可控；如后续查询慢再加）。
