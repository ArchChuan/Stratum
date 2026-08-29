# 统一成员白名单 + 权限申请设计

- 日期：2026-08-29
- 范围：agent / skill / mcp / knowledge workspace / knowledge_doc / workflow 六类资源
- 方案：A —— 共享机制统一 + 逐资源落地

## 背景与目标

平台已存在「成员白名单（可编辑人）+ 申请权限（grant_editor 提案）」机制，但覆盖不完整、实现分散：

- 只有 agent / skill / knowledge_doc 有成员申请入口（`grantRouteResourceType` 三种路由）；
- mcp、knowledge workspace 已有白名单与强制校验，但无成员申请入口；
- workflow 完全无成员访问模型（编辑目前 admin-only，`workflow_definitions` 无 `created_by`）。

目标：**所有资源都可以设置普通成员的白名单**，**所有普通成员都有申请权限的入口**，前后端统一实现，六类资源全部落地。

## 权限模型（已确认）

**统一单级白名单 = 编辑权**：

- agent / skill / mcp / knowledge workspace / **workflow**：白名单即「可编辑人」，在白名单内可直接编辑，否则只读 + 申请。
- knowledge_doc 特殊：**编辑与查看一体**，白名单 = 可访问内容，保持其现有 `allowed_users` + `allowed_roles` 角色级模型（比共享表更丰富，不做迁移）。

审批渠道：复用现有 `operation_proposals`（`grant_editor` 提案）→ admin/owner 审批 → 批准即授予白名单（与 agent/skill/knowledge_doc 现有链路一致）。

## 总体架构

```
成员查看资源
 ├─ 白名单内 / admin / owner → 可编辑
 └─ 其他成员 → 只读 + 「申请权限」→ grant_editor 提案 → 审批 → 授予白名单
```

| 资源 | 白名单存储 | 申请语义 |
|---|---|---|
| agent | `resource_editors` kind=agent | 可编辑 |
| skill | `resource_editors` kind=skill | 可编辑 |
| mcp | `resource_editors` kind=mcp | 可编辑 |
| knowledge workspace | `resource_editors` kind=knowledge | 可编辑 |
| workflow（新增） | `resource_editors` kind=workflow | 可编辑 |
| knowledge_doc | `allowed_users`+`allowed_roles` | 可访问内容 |

## 数据模型（DDL）

1. `resource_editors.resource_kind` 为 TEXT 无 CHECK 约束，**无需改表**，新增 kind='workflow' 即可。
2. `workflow_definitions` 新增 `created_by TEXT NOT NULL DEFAULT ''`（tenant_schema.sql 幂等 backfill），支撑所有权矩阵 creator 语义；历史行空值回退为 admin/owner 可编辑，与 skill「空 created_by 兜底」一致。

## 后端改动

### 申请入口扩展

- `grantRouteResourceType`（`api/http/handler/operation_proposal_handler.go`）新增：
  - `/mcp/servers/:id/request-editor` → `mcp`
  - `/knowledge/workspaces/:name/request-editor` → `knowledge_workspace`
  - `/workflows/:id/request-editor` → `workflow`
- `ProposeGrantEditor` 服务端 resourceType 白名单扩到六类：`agent`、`skill`、`knowledge_doc`、`mcp`、`knowledge_workspace`、`workflow`。
- 各资源申请时 `payloadSummary.resourceName` 传入自然名称。

### 授予分发扩展（grantEditor 闭包，`api/wiring/agent.go`）

```go
case "mcp":                 // 新增
    return resourceEditors.AddEditorForKind(ctx, tenantID, "mcp", resourceID, editorID, "operation-gate")
case "knowledge_workspace": // 新增
    return resourceEditors.AddEditorForKind(ctx, tenantID, "knowledge", resourceID, editorID, "operation-gate")
case "workflow":            // 新增
    return resourceEditors.AddEditorForKind(ctx, tenantID, "workflow", resourceID, editorID, "operation-gate")
```

（agent/skill/knowledge_doc 分支保持不动；授予幂等，重复批准不产生重复行。）

### workflow 白名单 CRUD + 访问强制

**路由变更**（`api/http/router.go`）：workflow 编辑组从 `admin` 中间件改为 `member`，鉴权下沉到 application 层所有权矩阵（对齐 agent/skill/mcp —— 前端只读隐藏、后端 fail-closed 兜底）：

- `POST /workflows` → member
- `PUT /workflows/:id/draft` → member
- `POST /workflows/:id/publish` → member
- `DELETE /workflows/:id` 保持 admin；`validate`/`rollback` 保持 admin

**新增 `PUT /workflows/:id/editors`**（admin/owner 管理白名单，复用 agent/skill 模式），`GET /workflows/:id` 响应附带 `editors`。

**DefinitionService 所有权矩阵**（复用 skill `enforceOwnership` 模式，fail-closed）：

| 操作 | owner | admin | 白名单成员 | 其他成员 |
|---|---|---|---|---|
| Update / Publish | ✅ | ✅ | ✅ | ❌ 403 |
| Delete | ✅ | ✅ | ❌ | ❌ |

- `Create` 写入 `created_by = actorID`。
- 白名单校验用共享 `resource_editors` kind='workflow'，写入事务内复查（TOCTOU 关闭，对齐 skill/workspace 的 `editorActor` 模式）。

### 其余资源申请入口

- workspace：`POST /knowledge/workspaces/:name/request-editor`（复用现有 handler，确认 `:name` 路由参数取参）。
- mcp：`POST /mcp/servers/:id/request-editor`。
- 两者后端白名单强制已存在（workspace/mcp 各自 `resolveUpdateActor`），无需新增校验。

## 前端改动

### 共享「申请权限」机制

新增共享单元（`web/src/shared/`）：

- `useRequestEditorAccess(resourceType, resourceId, resourceName)`：封装发起申请，成功提示已进入审批中心，重复申请/错误统一文案。
- `<RequestEditorButton />`：按资源类型渲染按钮文案与形态，`loading` 防连点。

### 各资源接入

| 资源 | 现状 | 改动 |
|---|---|---|
| agent | EditAgentPage 手写申请逻辑 | 迁移复用共享 hook/组件 |
| skill | SkillWorkspacePage 手写申请逻辑 | 迁移复用共享 hook/组件 |
| knowledge_doc | WorkspaceDocumentsTable/DetailPage 手写 | 迁移复用共享 hook/组件（文案保持「申请查看」） |
| mcp | MCPServersPage 成员视图无入口 | 非白名单成员只读配置 + 申请编辑权限 |
| workspace | 无入口 | 文档页/工作区页非白名单成员申请编辑权限 |
| workflow | 前端 `isAdmin` 控制编辑 | 非白名单成员只读画布 + 申请编辑权限；canEdit = `isAdmin \|\| editors.includes(user.sub)` |

### workflow 白名单管理

- WorkflowDesignerPage 顶部（admin/owner 可见）新增「可编辑人」管理：多选成员 + 保存（复用 `useEditorCandidates` + `PUT /workflows/:id/editors`）。
- 后端响应带 `editors` 时回显。

### 审批中心

- `OP_TYPE_LABELS` 已有 `grant_editor: '权限申请'`；资源名走 `proposalResourceLabel`（优先 `payloadSummary.resourceName`）。
- 确认 mcp/workspace/workflow 提案类型文案与资源名正确展示（补 `resourceName` 传入，必要时补标签映射）。

## 错误处理与安全

- 所有资源申请复用现有 fail-closed：resourceType 服务端白名单、授予幂等、审批者 admin/owner。
- workflow 所有权矩阵 fail-closed：未知角色 / 空 actor / 解析失败一律拒绝。
- `created_by` 幂等 DDL + 空值兜底，历史数据无风险。
- 越权路径：非白名单成员直接调 workflow Update API → 403。

## 测试与验收

### 后端

- `ProposeGrantEditor` resourceType 白名单（六类合法/非法、重复申请 dedupe）。
- grantEditor 闭包各 kind 分发落库正确 + 幂等。
- workflow 所有权矩阵表驱动（owner/admin/白名单成员/其他成员 × Update/Delete/Publish）。
- workflow `created_by` 写入与空值兜底。
- 契约测试：新路由 + golden；proto 涉及 DTO 变更则 `make proto-gen`。
- 修改 port 后同步所有 test mock/stub；`go vet && go test -short ./...`。

### 前端

- 共享 `useRequestEditorAccess` / `RequestEditorButton` 单测。
- 各资源接入页面测试（agent/skill/doc 迁移不回归；mcp/workspace/workflow 新增）。
- workflow「可编辑人」管理组件测试。

### E2E（stratum-e2e-tester，R3 级）

1. workflow 全链路（主要新增）：admin 设白名单 → 成员只读 + 申请 → admin 审批 → 授予 → 可编辑发布。
2. mcp / workspace 申请链路：申请 → 审批 → 授予 → 可编辑。
3. agent/skill/doc 回归：现有申请链路不回归。
4. 越权：非白名单成员调用 workflow Update API → 403（fail-closed）。

## 风险与回滚

- workflow 编辑从「admin 中间件」切到「member + 应用层校验」是行为变更，E2E 必须覆盖非白名单成员 403 路径。
- `created_by` 幂等 DDL + 空值兜底，无存量数据风险。
- 不破坏 knowledge_doc 现有角色级白名单模型。
