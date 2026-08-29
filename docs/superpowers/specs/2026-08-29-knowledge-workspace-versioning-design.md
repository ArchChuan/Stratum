# 知识库工作区版本历史与回滚设计

## 目标

为知识库工作区（`rag_workspaces`）提供配置版本历史与回滚能力：

- 每次保存工作区（名称 / 描述 / RAG 配置）产生一个新版本并立即生效（保持现有「保存即生效」语义）。
- 历史版本可回滚，回滚不产生新版本（对齐 agent / skill 的版本语义）。
- 编辑页提供「撤销未保存编辑」操作，撤销后自动用最新生效版本数据回填前端表单。
- 版本只覆盖工作区配置，不触碰 Milvus 向量数据。

## 非目标

- 不版本化 Milvus 向量数据、文档列表、文档访问白名单。
- 不引入草稿 → 发布流程（知识库保持保存即生效；草稿能力属于 skill 子项目）。
- 不改变 agent 与知识库的绑定语义：agent 版本快照继续记录「知识库绑定 ID 列表」，agent 回滚只恢复绑定；知识库自身拥有独立版本历史与回滚入口，两者解耦。
- 不引入 content_hash 乐观锁（知识库更新现状无并发基线，本次不在范围内）。

## 方案：接入通用版本基座 `pkg/versioning`

复用现有通用产品版本基座（agent 已接入），避免自建版本表。`pkg/versioning` 的 `productTables` 注释已预留「后续阶段接入 knowledge/mcp/skill 时在此登记」。

### 数据模型

1. `pkg/storage/postgres/tenant_schema.sql`：`rag_workspaces` 增加 `active_version_id TEXT` 列，紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 以幂等升级历史租户。存量行为 NULL（无版本），首次保存时补齐。
2. `pkg/versioning/version_tx.go`：`productTables` 注册：

   ```go
   var productTables = map[string]TableRef{
       "agent":     {Table: "agents", ActiveColumn: "active_version_id"},
       "knowledge": {Table: "rag_workspaces", ActiveColumn: "active_version_id"},
   }
   ```

   读侧 `internal/versioning` 的 `ResourceKind` 已含 `ResourceKindKnowledge = "knowledge"`，无需改动。

### 版本快照

新建 `internal/knowledge/domain/workspace_version.go`：

- `KnowledgeWorkspaceSnapshot`：`Name`、`Description`、`Config`（完整 `WorkspaceConfig`，序列化为 JSON map 作为版本 payload）。
- `SnapshotFromWorkspace(*Workspace) KnowledgeWorkspaceSnapshot`：保存时生成快照。
- `WorkspaceFromSnapshot(snap) *Workspace`：回滚时从版本 payload 重建配置。

快照明确只含工作区可编辑面（名称 / 描述 / RAG 配置），不含向量数据与文档。

### 保存流程（保存即生效 + 版本写入）

改造 `WorkspaceRepo.UpdateWorkspaceAll` 的现有租户写事务（`execTenant(ctx, r.db, tenantID, func(ctx, tx))`），事务内追加：

1. 事务内 `SELECT id FROM rag_workspaces WHERE name=$1` 解析 workspace ID（`resource_versions.resource_id` 使用；与 UPDATE 同事务，无 TOCTOU）。
2. `pkgversioning.InsertVersionTx(ctx, tx, VersionRow{ResourceKind: "knowledge", ResourceID: wsID, Status: "published", Source: "manual", Payload: 快照, SafeSummary: {name: ...}, CreatedBy: actor})`，回填 `revision_no` / `content_hash`。
3. `pkgversioning.DemoteCurrentTx(ctx, tx, "knowledge", wsID)`（首次保存无 published 时影响 0 行，不视为错误）。
4. `pkgversioning.SetActiveTx(ctx, tx, "knowledge", wsID, newVersionID)`。
5. 原有 `insertChangeAudit`。

事务原子性：任一版本写入失败整体回滚，不产生半写状态。模式完全对齐 agent repo 的 `writeAgentVersionTx` / `Rollback`。

### 回滚

新增接口 `POST /knowledge/workspaces/:name/rollback`，请求体 `{versionId}`：

1. `GetByName` 取当前 workspace；权限校验沿用 `checkOwnership`（tenant owner / admin / 创建者）。
2. 只读 `internal/versioning` 的 `GetVersion(tenantID, "knowledge", wsID, versionId)` 校验版本存在且 `status == deprecated`；非 deprecated 目标 fail-closed 拒绝（对齐 agent：仅 deprecated 可回滚）。
3. `WorkspaceFromSnapshot` 从版本 payload 重建名称 / 描述 / 配置。
4. 新增 `RollbackWorkspace` repo 方法，事务内：UPDATE `rag_workspaces`（名称 / 描述 / 配置全量写回）→ `RollbackVersionTx(ctx, tx, "knowledge", wsID, versionId)`（降级当前 + 提升目标）→ `SetActiveTx` → `insertChangeAudit`。
5. 回滚不产生新版本。

### 只读历史

新增接口 `GET /knowledge/workspaces/:name/versions`：复用 `internal/versioning` 的 `PgVersionRepo.ListVersions(tenantID, "knowledge", wsID)`（`IsCurrent` 已由读侧推导），DTO 结构对齐 agent 的 `AgentVersionResponse`。

### 前端（`KnowledgeDetailPage`）

页面为单页布局（无 Tabs），新增：

- 头部「版本历史」入口按钮：打开 Modal/Drawer 渲染共享 `VersionHistory` 组件；`canRollback: status === 'deprecated' && isAdmin`。
- 回滚成功：重拉 workspace → 回填 `configForm` 与名称 / 描述（对齐 agent 的 `reloadAgent` 模式）。
- **撤销（未保存编辑）**：配置表单「保存」旁新增「撤销」按钮，`Modal.confirm` 确认后重拉最新 workspace 数据回填 `configForm`（纯前端，零后端改动）；名称 / 描述内联编辑的撤销 = 取消编辑并 reset。
- 版本历史与撤销入口仅 `isAdmin` 可见。

### 错误处理

- 版本写入失败 → 事务回滚，返回 500，不产生半写状态。
- 目标版本不存在或非 deprecated → 404。
- 未注册 kind → fail-closed（本次已注册，理论不可达）。

### 测试

- repo 单测：`UpdateWorkspaceAll` 保存产生版本、`is_current` 轮换正确、首次保存（无历史）行为；`RollbackWorkspace` 原子性（audit 失败整体回滚）、非 deprecated 目标拒绝。
- domain 单测：`SnapshotFromWorkspace` / `WorkspaceFromSnapshot` 往返一致。
- handler 契约：`versions` / `rollback` 响应结构。
- 历史 schema 顺序测试：`active_version_id` 列幂等 backfill。
- 前端：撤销回填、回滚回填。

## 风险与边界

- 存量 workspace 无 `active_version_id`：首次保存补齐版本并设置指针；在首次保存前版本历史为空列表。
- `UpdateWorkspaceAll` 现在按 name 定位，新增的版本写入需在同一事务内解析 ID；重命名（`renameTo`）与该事务并发时，版本 `resource_id` 仍指向同一行，不受 name 变更影响。
