# Knowledge Workspace Versioning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为知识库工作区（`rag_workspaces`）提供配置版本历史、回滚与「撤销未保存编辑」能力，接入通用产品版本基座 `pkg/versioning`。

**Architecture:** 完全复用 agent 已接入的通用产品版本基座。写侧：`tenant_schema.sql` 为 `rag_workspaces` 增加 `active_version_id` 列并在 `pkg/versioning` 的 `productTables` 注册 `"knowledge"`；保存时在同一租户写事务内 `DemoteCurrentTx → InsertVersionTx → SetActiveTx` 写版本（镜像 `PgAgentRepo.writeAgentVersionTx`）。回滚：`UPDATE rag_workspaces` 写回快照 → `RollbackVersionTx → SetActiveTx`（镜像 `PgAgentRepo.Rollback`）。读侧复用 `internal/versioning` 的 `PgVersionRepo` 展示历史，`IsCurrent` 由读侧推导。快照只覆盖工作区可编辑面（名称/描述/RAG 配置），不触碰 Milvus 向量数据与文档。

**Tech Stack:** Go 1.25.12、pgx v5（pgxmock v2）、Gin、`pkg/versioning`、`internal/versioning`、React 18 + AntD 5 + Zod。

## Global Constraints

- **版本写入顺序必须是 `DemoteCurrentTx → InsertVersionTx → SetActiveTx`**（镜像 `internal/agent/infrastructure/persistence/agent_repo.go:756` 的 `writeAgentVersionTx`）。spec 原文写的是 Insert→Demote→SetActive，这是错误的：`DemoteCurrentTx` 会降级**所有** `status='published'` 行，包括刚插入的新行。必须先 Demote 再 Insert。
- 版本 ID 由调用方生成：`uuid.Must(uuid.NewV7()).String()`，同一个 ID 传给 `SetActiveTx`。`InsertVersionTx` 返回未导出的 `savedVersion`，调用方用 `if _, err := pkgversioning.InsertVersionTx(...)` 忽略返回值。
- 回滚顺序（镜像 `PgAgentRepo.Rollback`，agent_repo.go:836）：事务内重校验 editor → `UPDATE rag_workspaces` 写回快照 → `RollbackVersionTx` → `SetActiveTx` → audit。
- 回滚目标必须为 `status='deprecated'` 的历史版本，非 deprecated 目标 fail-closed 返回 `versioningdomain.ErrVersionNotFound`（404）。
- 所有 tenant-scoped 表访问必须通过 `execTenant(ctx, r.db, tenantID, func(ctx, tx))`，禁止直接调用 `r.pool.Exec/Query`。resource_versions 写入使用事务内解析出的 workspace UUID（`SELECT id FROM rag_workspaces WHERE name=$1`）。
- DDL 新增列必须 `ALTER TABLE rag_workspaces ADD COLUMN IF NOT EXISTS active_version_id TEXT;`，插入 `tenant_schema.sql` 紧跟 `rag_workspaces` 既有的 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS created_by`（第 794 行）之后；并在 `pkg/storage/postgres/tenant_schema_test.go` 的 `TestTenantSchemaContainsResourceVersionsProductHistory` 添加历史 schema 顺序守卫断言。
- 快照只含工作区可编辑面（`Name`/`Description`/`Config`），payload 用 snake_case canonical JSON（`encoding/json` map 键排序保证可哈希）。
- 版本 `CreatedBy` 使用实际操作者 `actorID`；回滚不产生新版本。
- 回滚权限沿用 `resolveUpdateActor` 语义：owner/admin 通过（editorActor 为空串），白名单 editor 通过（editorActor=actorID，repo 事务内重校验），其余 `domain.ErrForbidden`。路由层面回滚用 `RequireTenantRole("admin")`（spec：回滚入口仅 isAdmin 可见），版本历史 GET 用 member 级 `requireActive`（对齐 agent/skill，前端仅 isAdmin 渲染入口）。
- 修改 port 后立即同步全部 test mock/stub（compile-error-driven：`go build ./...` 列出损坏 mock，逐一更新签名并添加 `RollbackWorkspace` stub）。
- 每个 task 的 commit 以 `feat: ...` 开头，末尾带 `Co-Authored-By: Claude <noreply@anthropic.com>`。禁止在 `main` 分支直接提交。
- 行为数字（无新数字引入）禁止内联；本计划无新常量。
- 错误逐层 `fmt.Errorf("operation: %w", err)`；仅用 Zap 日志；JSONB 写 `string(b)`（`json.Marshal` 后传 string）。

---

### Task 1: versioning 基座注册 knowledge + tenant schema 列

在通用版本基座登记 `"knowledge"` 产品表，并为 `rag_workspaces` 增加 `active_version_id` 幂等列。

**Files:**

- Modify: `pkg/versioning/version_tx.go:30-32`（`productTables` 注册 knowledge）
- Modify: `pkg/storage/postgres/tenant_schema.sql:794`（新增 `rag_workspaces.active_version_id` 回填）
- Modify: `pkg/versioning/version_tx_test.go:52`（`TestProductTableRef` knowledge 翻转 True）
- Modify: `pkg/storage/postgres/tenant_schema_test.go:957-990`（`TestTenantSchemaContainsResourceVersionsProductHistory` 加 rag_workspaces 断言）
- Test: `pkg/versioning/version_tx_test.go`、`pkg/storage/postgres/tenant_schema_test.go`

**Interfaces:**

- Consumes: `pkg/versioning.ProductTableRef(kind) (TableRef, bool)`（已存在）
- Produces: `ProductTableRef("knowledge") == (TableRef{Table: "rag_workspaces", ActiveColumn: "active_version_id"}, true)`；`tenant_schema.sql` 含 `ALTER TABLE rag_workspaces ADD COLUMN IF NOT EXISTS active_version_id TEXT`

- [ ] **Step 1: 修改 `TestProductTableRef` 断言 knowledge 已注册（先写失败测试）**

将 `pkg/versioning/version_tx_test.go` 中 `TestProductTableRef` 里的 knowledge 断言从 False 翻转为 True，并断言返回的表名/列名：

```go
func TestProductTableRef(t *testing.T) {
 ref, ok := ProductTableRef("agent")
 require.True(t, ok)
 require.Equal(t, "agents", ref.Table)
 require.Equal(t, "active_version_id", ref.ActiveColumn)

 // knowledge 已接入：productTables 注册 rag_workspaces.active_version_id。
 ref, ok = ProductTableRef("knowledge")
 require.True(t, ok)
 require.Equal(t, "rag_workspaces", ref.Table)
 require.Equal(t, "active_version_id", ref.ActiveColumn)

 // 未接入的 kind 必须 fail-closed（读侧 is_current / 写侧 SetActiveTx 报错）。
 for _, kind := range []string{"skill", "mcp"} {
  _, ok := ProductTableRef(kind)
  require.False(t, ok)
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./pkg/versioning/ -run TestProductTableRef -v`
Expected: FAIL —— `require.True(t, ok)` 报错：`ProductTableRef("knowledge")` 返回 false。

- [ ] **Step 3: 注册 knowledge 到 productTables**

修改 `pkg/versioning/version_tx.go` 的 `productTables`：

```go
var productTables = map[string]TableRef{
 "agent":     {Table: "agents", ActiveColumn: "active_version_id"},
 "knowledge": {Table: "rag_workspaces", ActiveColumn: "active_version_id"},
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./pkg/versioning/ -run 'TestProductTableRef|TestSetActiveTx' -v`
Expected: PASS。`TestSetActiveTx` 中 `unregistered_kind_fails_closed` 用 `"skill"`（仍未注册），不受影响。

- [ ] **Step 5: tenant_schema.sql 增加 active_version_id 幂等回填**

在 `pkg/storage/postgres/tenant_schema.sql` 第 794 行 `ALTER TABLE rag_workspaces ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';` 之后紧跟一行：

```sql
ALTER TABLE rag_workspaces ADD COLUMN IF NOT EXISTS active_version_id TEXT;
```

（列保留 NULL=无版本记录，与 `agents.active_version_id` 同语义；首次保存补齐版本时写入。）

- [ ] **Step 6: 在 tenant_schema 守卫测试中添加 rag_workspaces 断言**

在 `pkg/storage/postgres/tenant_schema_test.go` 的 `TestTenantSchemaContainsResourceVersionsProductHistory` 中，`agents` 顺序断言块之后追加：

```go
 // 产品表生效版本指针:历史租户升级路径,IF NOT EXISTS 幂等。NULL=无版本记录。
 workspacesAt := strings.Index(sql, "CREATE TABLE IF NOT EXISTS rag_workspaces")
 ragActiveVersionAt := strings.Index(sql, "ALTER TABLE rag_workspaces ADD COLUMN IF NOT EXISTS active_version_id TEXT")
 require.NotEqual(t, -1, workspacesAt, "rag_workspaces table DDL must exist")
 require.NotEqual(t, -1, ragActiveVersionAt, "tenant_schema.sql missing rag_workspaces.active_version_id backfill")
 if ragActiveVersionAt < workspacesAt {
  t.Fatal("rag_workspaces.active_version_id backfill must follow table creation")
 }
```

- [ ] **Step 7: 运行 schema 测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./pkg/storage/postgres/ -run TestTenantSchemaContainsResourceVersionsProductHistory -v`
Expected: PASS。

- [ ] **Step 8: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add pkg/versioning/version_tx.go pkg/versioning/version_tx_test.go pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go
git commit -m "feat(versioning): register knowledge product table and rag_workspaces active_version_id

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: knowledge domain 版本快照（workspace_version.go）

新建 knowledge 工作区的版本快照类型与往返转换（镜像 `internal/agent/domain/agent_version.go`）。

**Files:**

- Create: `internal/knowledge/domain/workspace_version.go`
- Test: `internal/knowledge/domain/workspace_version_test.go`

**Interfaces:**

- Consumes: `domain.Workspace{ID, Name, Description string, Config WorkspaceConfig, ...}`（`internal/knowledge/domain/workspace.go:76`）
- Produces:
  - `type KnowledgeWorkspaceSnapshot struct { Name, Description string; Config WorkspaceConfig }`
  - `func SnapshotFromWorkspace(ws *Workspace) KnowledgeWorkspaceSnapshot`
  - `func (s KnowledgeWorkspaceSnapshot) Map() map[string]any`
  - `func SnapshotFromMap(payload map[string]any) (KnowledgeWorkspaceSnapshot, error)`
  - `func (s KnowledgeWorkspaceSnapshot) ToWorkspace(id string) *Workspace`

- [ ] **Step 1: 写失败测试（round-trip + 字段完整性）**

创建 `internal/knowledge/domain/workspace_version_test.go`：

```go
package domain

import (
 "testing"

 "github.com/stretchr/testify/require"
)

func TestWorkspaceSnapshotRoundTrip(t *testing.T) {
 ws := &Workspace{
  ID:          "ws-1",
  Name:        "知识库 A",
  Description: "内部文档",
  Config: WorkspaceConfig{
   EmbeddingModel:   "text-embedding-v3",
   ChunkSize:        512,
   ChunkOverlap:     64,
   QueryMode:        "hybrid",
   TopK:             10,
   ChunkingStrategy: "recursive",
   Reranking:        "provider:reranker",
   ScoreThreshold:   0.7,
   RerankTopK:       5,
   RerankModel:      "qwen-rerank",
   JudgeModel:       "qwen-judge",
  },
  CreatedBy: "u1",
 }

 snap := SnapshotFromWorkspace(ws)
 payload := snap.Map()
 // payload 必须是可哈希的 canonical JSON：无缺失、无额外键。
 got, err := SnapshotFromMap(payload)
 require.NoError(t, err)
 require.Equal(t, "知识库 A", got.Name)
 require.Equal(t, "内部文档", got.Description)
 require.Equal(t, ws.Config, got.Config)
 require.Equal(t, ws.Name, got.ToWorkspace(ws.ID).Name)
 require.Equal(t, ws.Description, got.ToWorkspace(ws.ID).Description)
 require.Equal(t, ws.Config, got.ToWorkspace(ws.ID).Config)
 require.Equal(t, ws.ID, got.ToWorkspace(ws.ID).ID)
}

func TestWorkspaceSnapshotFromMapIgnoringUnknownKeys(t *testing.T) {
 // 版本历史向前兼容：未知键忽略，缺失键回落零值。
 snap, err := SnapshotFromMap(map[string]any{"name": "N", "config": map[string]any{"chunk_size": 128}})
 require.NoError(t, err)
 require.Equal(t, "N", snap.Name)
 require.Equal(t, 128, snap.Config.ChunkSize)
 require.Zero(t, snap.Description)
 require.Equal(t, "config", "config") // config 缺失其余字段回落零值
 require.Zero(t, snap.Config.TopK)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/knowledge/domain/ -run TestWorkspaceSnapshot -v`
Expected: FAIL —— `undefined: SnapshotFromWorkspace`。

- [ ] **Step 3: 实现快照类型与转换**

创建 `internal/knowledge/domain/workspace_version.go`：

```go
package domain

import "encoding/json"

// KnowledgeWorkspaceSnapshot 是知识库工作区可编辑面的版本化快照，写入通用产品
// 版本基座 resource_versions 的 payload，供版本历史展示与回滚重建。快照只含
// 工作区可编辑面（名称/描述/RAG 配置），不含 Milvus 向量数据、文档列表与文档
// 访问白名单（回滚不触碰它们）。id/created_by 等不可变字段不进快照。
//
// WorkspaceConfig 字段无显式 json tag，以 Go 字段名（PascalCase）序列化；
// Map() 经 encoding/json 对 map 键排序，输出确定，与 versioning/domain 的
// ComputeContentHash 配套。
type KnowledgeWorkspaceSnapshot struct {
 Name        string          `json:"name"`
 Description string          `json:"description"`
 Config      WorkspaceConfig `json:"config"`
}

// SnapshotFromWorkspace 捕获 ws 的可编辑面（Update 构建完成、校验通过后调用）。
func SnapshotFromWorkspace(ws *Workspace) KnowledgeWorkspaceSnapshot {
 if ws == nil {
  return KnowledgeWorkspaceSnapshot{}
 }
 return KnowledgeWorkspaceSnapshot{
  Name:        ws.Name,
  Description: ws.Description,
  Config:      ws.Config,
 }
}

// Map 渲染为 resource_versions.payload（snake_case 键，canonical JSON 可哈希）。
func (s KnowledgeWorkspaceSnapshot) Map() map[string]any {
 encoded, err := json.Marshal(s)
 if err != nil {
  return map[string]any{}
 }
 var m map[string]any
 _ = json.Unmarshal(encoded, &m)
 return m
}

// SnapshotFromMap 从 resource_versions.payload 重建快照（回滚路径）。未知键
// 忽略（版本历史向前兼容），缺失键回落零值。
func SnapshotFromMap(payload map[string]any) (KnowledgeWorkspaceSnapshot, error) {
 encoded, err := json.Marshal(payload)
 if err != nil {
  return KnowledgeWorkspaceSnapshot{}, err
 }
 var s KnowledgeWorkspaceSnapshot
 if err := json.Unmarshal(encoded, &s); err != nil {
  return KnowledgeWorkspaceSnapshot{}, err
 }
 return s, nil
}

// ToWorkspace 从快照重建 Workspace，供回滚写入与审计投影。id 保留资源标识；
// CreatedBy 留空（UPDATE/Rollback 的 SET 列不触碰 created_by，回滚审计投影
// 的 before 来自当前行）。
func (s KnowledgeWorkspaceSnapshot) ToWorkspace(id string) *Workspace {
 return &Workspace{
  ID:          id,
  Name:        s.Name,
  Description: s.Description,
  Config:      s.Config,
 }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/knowledge/domain/ -run TestWorkspaceSnapshot -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add internal/knowledge/domain/workspace_version.go internal/knowledge/domain/workspace_version_test.go
git commit -m "feat(knowledge): add workspace version snapshot with round-trip conversion

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: port 变更（UpdateWorkspaceAll 签名 + RollbackWorkspace + ActorNameResolver）+ mock 同步

扩展 `WorkspaceRepo` port 与新增 actor 名解析 port，并同步全部 mock（compile-error-driven）。

**Files:**

- Modify: `internal/knowledge/domain/port/workspace_repo.go`
- Create: `internal/knowledge/domain/port/actor_name_resolver.go`
- Modify（6 个 mock，签名同步 + 添加 RollbackWorkspace stub）:
  - `internal/knowledge/application/workspace_service_test.go`（`deleteWorkspaceRepo`，:33）
  - `internal/knowledge/application/workspace_service_extra_test.go`（`fakeWorkspaceRepo`，:94，body 用 `snap.Config`）
  - `api/http/handler/rag_preview_handler_test.go`（`previewWorkspaceRepo`，:118）
  - `api/http/handler/rag_doc_access_handler_test.go`（`accessHandlerWorkspaceRepo`，:45）
  - `internal/knowledge/application/rag_service_test.go`（`recordingWorkspaceRepo`，:366）
  - `internal/knowledge/application/visible_doc_ids_test.go`（`errWorkspaceRepo`，:30）
- Test: 编译 + `go test ./internal/knowledge/... ./api/http/handler/...`

**Interfaces:**

- Consumes: `domain.KnowledgeWorkspaceSnapshot`（Task 2）、`auditdomain.ResourceChangeAuditEvent`
- Produces:
  - `UpdateWorkspaceAll(ctx, tenantID, name string, renameTo, description *string, snap domain.KnowledgeWorkspaceSnapshot, editorActor, actorID string, audit *auditdomain.ResourceChangeAuditEvent) error`
  - `RollbackWorkspace(ctx, tenantID, name string, snap domain.KnowledgeWorkspaceSnapshot, editorActor, targetVersionID string, audit *auditdomain.ResourceChangeAuditEvent) error`
  - `port.ActorNameResolver`（复制 agent 的 `internal/agent/domain/port/actor_name_resolver.go` 形状）

- [ ] **Step 1: 修改 WorkspaceRepo port 签名并新增 RollbackWorkspace**

将 `internal/knowledge/domain/port/workspace_repo.go` 的 `UpdateWorkspaceAll` 签名改为（`cfg domain.WorkspaceConfig` → `snap domain.KnowledgeWorkspaceSnapshot`，新增 `actorID string`），并新增 `RollbackWorkspace`：

```go
 // UpdateWorkspaceAll applies rename, description and config atomically in
 // one transaction with the audit row and a product version write (new
 // version becomes active immediately — "保存即生效"). snap is the
 // post-update editable surface snapshot stored as the version payload;
 // actorID is the version's CreatedBy (actual operator). renameTo/
 // description may be nil. editorActor, when non-empty, re-validates
 // inside the transaction that the actor still holds role admin/owner and
 // is present in resource_editors (closes the check-then-write TOCTOU
 // window).
 UpdateWorkspaceAll(ctx context.Context, tenantID, name string, renameTo, description *string, snap domain.KnowledgeWorkspaceSnapshot, editorActor, actorID string, audit *auditdomain.ResourceChangeAuditEvent) error
 // RollbackWorkspace restores a deprecated historical version in one
 // transaction: snapshot written back to the workspace row, the target
 // promoted to published, and active_version_id repointed at it. No new
 // version is created. targetVersionID must reference a deprecated
 // version (versioningdomain.ErrVersionNotFound otherwise). editorActor,
 // when non-empty, re-validates inside the transaction.
 RollbackWorkspace(ctx context.Context, tenantID, name string, snap domain.KnowledgeWorkspaceSnapshot, editorActor, targetVersionID string, audit *auditdomain.ResourceChangeAuditEvent) error
```

- [ ] **Step 2: 创建 ActorNameResolver port**

创建 `internal/knowledge/domain/port/actor_name_resolver.go`：

```go
package port

import "context"

// ActorNameResolver 批量解析 actor 的用户名（display_name > github_login >
// actor_id 兜底）。实现位于 internal/iam/infrastructure/persistence
// （public.users 全局表，跨租户），由 api/wiring 装配注入。查询失败返回错误
// （fail-closed：禁止默认名掩盖查询故障）；查不到的 actor 由实现回退 actor_id
// 原文。镜像 internal/agent/domain/port/actor_name_resolver.go。
type ActorNameResolver interface {
 ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error)
}
```

- [ ] **Step 3: 编译定位损坏 mock**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./...`
Expected: FAIL —— 6 处 mock 的 `UpdateWorkspaceAll` 签名不匹配（`cannot use ... as domain.KnowledgeWorkspaceSnapshot`）。逐步修复 Step 4-6。

- [ ] **Step 4: 同步 no-op mock（deleteWorkspaceRepo / previewWorkspaceRepo / accessHandlerWorkspaceRepo / recordingWorkspaceRepo / errWorkspaceRepo）**

逐一替换签名并新增 `RollbackWorkspace` stub：

`internal/knowledge/application/workspace_service_test.go`（`deleteWorkspaceRepo`）：

```go
func (r *deleteWorkspaceRepo) UpdateWorkspaceAll(context.Context, string, string, *string, *string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
 return nil
}
func (r *deleteWorkspaceRepo) RollbackWorkspace(context.Context, string, string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
 return nil
}
```

`api/http/handler/rag_preview_handler_test.go`（`previewWorkspaceRepo`）：

```go
func (r *previewWorkspaceRepo) UpdateWorkspaceAll(
 context.Context, string, string, *string, *string, knowledgedomain.KnowledgeWorkspaceSnapshot,
 string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
 return nil
}
func (r *previewWorkspaceRepo) RollbackWorkspace(
 context.Context, string, string, knowledgedomain.KnowledgeWorkspaceSnapshot,
 string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
 return nil
}
```

`api/http/handler/rag_doc_access_handler_test.go`（`accessHandlerWorkspaceRepo`）：

```go
func (r *accessHandlerWorkspaceRepo) UpdateWorkspaceAll(
 context.Context, string, string, *string, *string, knowledgedomain.KnowledgeWorkspaceSnapshot,
 string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
 return nil
}
func (r *accessHandlerWorkspaceRepo) RollbackWorkspace(
 context.Context, string, string, knowledgedomain.KnowledgeWorkspaceSnapshot,
 string, string, *auditdomain.ResourceChangeAuditEvent,
) error {
 return nil
}
```

`internal/knowledge/application/rag_service_test.go`（`recordingWorkspaceRepo`）：

```go
func (r *recordingWorkspaceRepo) UpdateWorkspaceAll(ctx context.Context, tenantID, name string, renameTo, description *string, snap domain.KnowledgeWorkspaceSnapshot, _ string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 return nil
}
func (r *recordingWorkspaceRepo) RollbackWorkspace(ctx context.Context, tenantID, name string, snap domain.KnowledgeWorkspaceSnapshot, _ string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 return nil
}
```

`internal/knowledge/application/visible_doc_ids_test.go`（`errWorkspaceRepo`）：

```go
func (r *errWorkspaceRepo) UpdateWorkspaceAll(context.Context, string, string, *string, *string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
 return r.err
}
func (r *errWorkspaceRepo) RollbackWorkspace(context.Context, string, string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
 return r.err
}
```

- [ ] **Step 5: 同步 fakeWorkspaceRepo（有状态，body 用 snap.Config + RollbackWorkspace 覆盖）**

`internal/knowledge/application/workspace_service_extra_test.go` 中 `fakeWorkspaceRepo`：

```go
func (f *fakeWorkspaceRepo) UpdateWorkspaceAll(_ context.Context, _, name string, renameTo, description *string, snap domain.KnowledgeWorkspaceSnapshot, _ string, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
 if audit != nil {
  f.audits = append(f.audits, audit)
 }
 if f.updateErr != nil {
  return f.updateErr
 }
 ws, ok := f.workspaces[name]
 if !ok {
  return domain.ErrWorkspaceNotFound
 }
 if renameTo != nil {
  delete(f.workspaces, name)
  ws.Name = *renameTo
  f.workspaces[*renameTo] = ws
  f.renames = append(f.renames, struct{ oldName, newName string }{name, *renameTo})
 }
 if description != nil {
  ws.Description = *description
 }
 ws.Config = snap.Config
 return nil
}

func (f *fakeWorkspaceRepo) RollbackWorkspace(_ context.Context, _ string, name string, snap domain.KnowledgeWorkspaceSnapshot, _ string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 if f.updateErr != nil {
  return f.updateErr
 }
 ws, ok := f.workspaces[name]
 if !ok {
  return domain.ErrWorkspaceNotFound
 }
 ws.Name = snap.Name
 ws.Description = snap.Description
 ws.Config = snap.Config
 return nil
}
```

- [ ] **Step 6: 重新编译并跑 knowledge + handler 测试**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./... && go test ./internal/knowledge/... ./api/http/handler/ -short`
Expected: PASS（本 Task 只做签名同步，不改行为；既有测试语义不变）。

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add internal/knowledge/domain/port/ internal/knowledge/application/*_test.go api/http/handler/*_test.go
git commit -m "feat(knowledge): extend workspace repo port with snapshot update and rollback

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: workspace_repo.go 版本写入与回滚实现 + pgxmock 测试

在 `WorkspaceRepo.UpdateWorkspaceAll` 写事务内追加版本写入（Demote→Insert→SetActive），新增 `RollbackWorkspace`。

**Files:**

- Modify: `internal/knowledge/infrastructure/persistence/workspace_repo.go`
- Test: `internal/knowledge/infrastructure/persistence/workspace_repo_test.go`（新增 pgxmock 用例）
- Test: `internal/knowledge/infrastructure/persistence/workspace_repo_rollback_test.go`（新增）

**Interfaces:**

- Consumes: `port.UpdateWorkspaceAll` / `port.RollbackWorkspace` 新签名（Task 3）、`domain.KnowledgeWorkspaceSnapshot`（Task 2）、`pkg/versioning`（`VersionRow`/`DemoteCurrentTx`/`InsertVersionTx`/`SetActiveTx`/`RollbackVersionTx`）、`execTenant`/`revalidateEditorAccess`/`insertChangeAudit`（文件内已有）
- Produces:
  - `UpdateWorkspaceAll`：事务内无条件 `SELECT id FROM rag_workspaces WHERE name=$1` 解析 wsID（`pgx.ErrNoRows` → `domain.ErrWorkspaceNotFound`），UPDATE 写回 → `writeKnowledgeVersionTx(ctx, tx, wsID, snap, actorID)` → audit
  - `writeKnowledgeVersionTx(ctx, tx, wsID string, snap domain.KnowledgeWorkspaceSnapshot, actorID string) error`（包私有，顺序 Demote→Insert→SetActive）
  - `RollbackWorkspace`：事务内解析 wsID → revalidate → UPDATE（`name=$1, description=$2, config=$3`，`WHERE id=$4::uuid`）→ `pkgversioning.RollbackVersionTx` → `pkgversioning.SetActiveTx` → audit

- [ ] **Step 1: 写失败测试（UpdateWorkspaceAll 写版本 + RollbackWorkspace）**

创建 `internal/knowledge/infrastructure/persistence/workspace_repo_rollback_test.go`：

```go
package persistence

import (
 "context"
 "errors"
 "testing"

 "github.com/pashagolub/pgxmock/v2"
 "github.com/stretchr/testify/require"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// newWorkspaceVersionMockTx 返回一个租户事务的 pgxmock（模拟 execTenant 的 SET
// LOCAL search_path + BEGIN/COMMIT）。与 workspace_repo_test.go 的既有 helper
// 同构；若该文件已有同签名 helper 则复用。
func newWorkspaceVersionMockTx(t *testing.T) (pgxmock.PgxPoolIface, pgx.Tx) {
 t.Helper()
 pool, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(func() { pool.Close() })
 pool.ExpectBegin()
 tx, err := pool.Begin(context.Background())
 require.NoError(t, err)
 return pool, tx
}
```

（Step 3 的实现依赖 `writeKnowledgeVersionTx`，该 helper 的 pgxmock expectation 顺序为 `SELECT id` → `UPDATE` → Demote → Insert → SetActive → audit INSERT。测试在 Step 4 编写完整断言；本 Step 先写针对 `RollbackWorkspace` 失败路径的最小用例以锁定签名。）

在 `workspace_repo_rollback_test.go` 追加：

```go
func TestWorkspaceRepoRollback_NonDeprecatedTargetFailsClosed(t *testing.T) {
 // 非 deprecated 目标由 RollbackVersionTx 内部拒绝（RowsAffected!=1 →
 // versioning.ErrVersionNotFound）；测试锁定该失败会整体回滚（Begin→Rollback）。
 pool, tx := newWorkspaceVersionMockTx(t)
 defer tx.Rollback(context.Background())

 snap := domain.SnapshotFromWorkspace(&domain.Workspace{Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}})
 // 事务内 SELECT id → revalidate（editorActor 为空则跳过）→ UPDATE 写回 →
 // RollbackVersionTx 内部 SELECT（查目标状态）发现非 deprecated → 返回错误。
 pool.ExpectQuery("SELECT id FROM rag_workspaces WHERE name=\\$1").
  WithArgs("kb").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
 pool.ExpectExec("UPDATE rag_workspaces").
  WithArgs("kb", "d", pgxmock.AnyArg(), "ws-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 pool.ExpectQuery("SELECT status FROM resource_versions").
  WithArgs("knowledge", "ws-1", "v9").WillReturnRows(pgxmock.NewRows([]string{"status"}).AddRow("published"))

 repo := NewWorkspaceRepo(pool)
 err := repo.RollbackWorkspace(context.Background(), "tenant-1", "kb", snap, "", "v9", nil)
 require.Error(t, err)
 require.True(t, errors.Is(err, versioning.ErrVersionNotFound))
 require.NoError(t, pool.ExpectationsWereMet())
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/knowledge/infrastructure/persistence/ -run TestWorkspaceRepoRollback -v`
Expected: FAIL —— `RollbackWorkspace` 未定义 / `versioning` 未导入。

- [ ] **Step 3: 实现 UpdateWorkspaceAll 版本写入 + RollbackWorkspace**

修改 `internal/knowledge/infrastructure/persistence/workspace_repo.go`：

先补 import（`github.com/google/uuid`、`pkgversioning "github.com/byteBuilderX/stratum/pkg/versioning"`）。

将 `UpdateWorkspaceAll` 整体替换为：

```go
func (r *WorkspaceRepo) UpdateWorkspaceAll(
 ctx context.Context, tenantID, name string,
 renameTo, description *string,
 snap domain.KnowledgeWorkspaceSnapshot,
 editorActor, actorID string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 var tag pgconn.CommandTag
 err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  // 事务内解析 workspace ID（resource_versions.resource_id 用 UUID；与
  // UPDATE 同事务，无 TOCTOU）。缺失行 fail-closed。
  var wsID string
  if err := tx.QueryRow(ctx, `SELECT id FROM rag_workspaces WHERE name=$1`, name).Scan(&wsID); err != nil {
   return err
  }
  if editorActor != "" {
   if err := revalidateEditorAccess(ctx, tx, tenantID, resourceEditorKind, wsID, editorActor); err != nil {
    return err
   }
  }
  var err error
  tag, err = tx.Exec(ctx, `UPDATE rag_workspaces
                       SET name = COALESCE($1, name),
                           description = COALESCE($2, description),
                           config = $3,
                           updated_at = NOW()
   WHERE name = $4`, renameTo, description, toJSONB(snap.Config), name)
  if err != nil {
   return err
  }
  if err := writeKnowledgeVersionTx(ctx, tx, wsID, snap, actorID); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, tenantID, audit)
 })
 if err != nil {
  var pgErr *pgconn.PgError
  if errors.Is(err, pgx.ErrNoRows) {
   return domain.ErrWorkspaceNotFound
  }
  if errors.As(err, &pgErr) && pgErr.Code == "23505" {
   return domain.ErrWorkspaceConflict
  }
  return fmt.Errorf("workspace_repo: update: %w", err)
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrWorkspaceNotFound
 }
 return nil
}

// writeKnowledgeVersionTx 在调用方的写事务内把工作区可编辑面快照写入通用产品
// 版本基座 resource_versions，并把 rag_workspaces.active_version_id 指向新版本。
// 顺序必须是 Demote→Insert→SetActive（镜像 agent 的 writeAgentVersionTx）：
// DemoteCurrentTx 会降级所有 status='published' 行，包括刚插入的新行。
func writeKnowledgeVersionTx(ctx context.Context, tx pgx.Tx, wsID string, snap domain.KnowledgeWorkspaceSnapshot, actorID string) error {
 id := uuid.Must(uuid.NewV7()).String()
 row := pkgversioning.VersionRow{
  ID:           id,
  ResourceKind: "knowledge",
  ResourceID:   wsID,
  Status:       "published",
  Source:       "manual",
  Payload:      snap.Map(),
  SafeSummary:  map[string]any{"name": snap.Name},
  CreatedBy:    actorID,
 }
 if err := pkgversioning.DemoteCurrentTx(ctx, tx, "knowledge", wsID); err != nil {
  return fmt.Errorf("workspace_repo: update %s demote current version: %w", wsID, err)
 }
 if _, err := pkgversioning.InsertVersionTx(ctx, tx, row); err != nil {
  return fmt.Errorf("workspace_repo: update %s insert version: %w", wsID, err)
 }
 if err := pkgversioning.SetActiveTx(ctx, tx, "knowledge", wsID, id); err != nil {
  return fmt.Errorf("workspace_repo: update %s set active version: %w", wsID, err)
 }
 return nil
}

// RollbackWorkspace restores a deprecated historical version in one
// transaction: the snapshot payload is written back to the workspace row, the
// target promoted to published, and active_version_id repointed at it. No new
// version is created. A non-deprecated / missing target fails closed with
// versioning.ErrVersionNotFound (from the shared RollbackVersionTx helper).
func (r *WorkspaceRepo) RollbackWorkspace(
 ctx context.Context, tenantID, name string,
 snap domain.KnowledgeWorkspaceSnapshot,
 editorActor, targetVersionID string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  var wsID string
  if err := tx.QueryRow(ctx, `SELECT id FROM rag_workspaces WHERE name=$1`, name).Scan(&wsID); err != nil {
   return err
  }
  if editorActor != "" {
   if err := revalidateEditorAccess(ctx, tx, tenantID, resourceEditorKind, wsID, editorActor); err != nil {
    return err
   }
  }
  if _, err := tx.Exec(ctx, `UPDATE rag_workspaces
                       SET name = $1, description = $2, config = $3, updated_at = NOW()
   WHERE id = $4::uuid`, snap.Name, snap.Description, toJSONB(snap.Config), wsID); err != nil {
   return err
  }
  if err := pkgversioning.RollbackVersionTx(ctx, tx, "knowledge", wsID, targetVersionID); err != nil {
   return err
  }
  if err := pkgversioning.SetActiveTx(ctx, tx, "knowledge", wsID, targetVersionID); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, tenantID, audit)
 })
 if err != nil {
  var pgErr *pgconn.PgError
  if errors.Is(err, pgx.ErrNoRows) {
   return domain.ErrWorkspaceNotFound
  }
  if errors.As(err, &pgErr) && pgErr.Code == "23505" {
   return domain.ErrWorkspaceConflict
  }
  return fmt.Errorf("workspace_repo: rollback: %w", err)
 }
 return nil
}
```

- [ ] **Step 4: 补全 pgxmock 测试（UpdateWorkspaceAll 写版本 / 回滚成功 / 回滚 audit 失败回滚）**

在 `workspace_repo_rollback_test.go` 追加（按 Step 1 的 mock 语义补全）：

```go
func TestWorkspaceRepoUpdateWritesPublishedVersion(t *testing.T) {
 pool, tx := newWorkspaceVersionMockTx(t)
 defer tx.Rollback(context.Background())

 snap := domain.SnapshotFromWorkspace(&domain.Workspace{Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}})
 pool.ExpectQuery("SELECT id FROM rag_workspaces WHERE name=\\$1").
  WithArgs("kb").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
 pool.ExpectExec("UPDATE rag_workspaces").
  WithArgs("kb", nil, pgxmock.AnyArg(), "kb").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 // 版本写入：Demote（影响 0 行不报错）→ Insert（含 SELECT MAX + SELECT parent + INSERT）→ SetActive。
 pool.ExpectExec("UPDATE resource_versions SET status = 'deprecated'").
  WithArgs("knowledge", "ws-1").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
 pool.ExpectQuery("SELECT COALESCE\\(MAX\\(revision_no\\), 0\\) \\+ 1").
  WithArgs("knowledge", "ws-1").WillReturnRows(pgxmock.NewRows([]string{"next"}).AddRow(1))
 pool.ExpectQuery("SELECT COALESCE\\(\\(SELECT id FROM resource_versions").
  WithArgs("knowledge", "ws-1", "1").WillReturnRows(pgxmock.NewRows([]string{"parent"}).AddRow(""))
 pool.ExpectExec("INSERT INTO resource_versions").
  WithArgs(pgxmock.AnyArg(), "knowledge", "ws-1", "", 1, "published", "manual", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "u1").
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 pool.ExpectExec("UPDATE rag_workspaces SET active_version_id").
  WithArgs(pgxmock.AnyArg(), "ws-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

 repo := NewWorkspaceRepo(pool)
 audit := &auditdomain.ResourceChangeAuditEvent{ResourceKind: "knowledge", ResourceID: "ws-1", Operation: "update"}
 // audit INSERT：insertChangeAudit 在 audit 非 nil 时写 resource_change_audit。
 pool.ExpectExec("INSERT INTO resource_change_audit").
  WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))

 err := repo.UpdateWorkspaceAll(context.Background(), "tenant-1", "kb", nil, nil, snap, "", "u1", audit)
 require.NoError(t, err)
 require.NoError(t, pool.ExpectationsWereMet())
}

func TestWorkspaceRepoRollback_SuccessRepointsActive(t *testing.T) {
 pool, tx := newWorkspaceVersionMockTx(t)
 defer tx.Rollback(context.Background())

 snap := domain.SnapshotFromWorkspace(&domain.Workspace{Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}})
 pool.ExpectQuery("SELECT id FROM rag_workspaces WHERE name=\\$1").
  WithArgs("kb").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("ws-1"))
 pool.ExpectExec("UPDATE rag_workspaces").
  WithArgs("old", "od", pgxmock.AnyArg(), "ws-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 // RollbackVersionTx：Demote current → UPDATE target → published。
 pool.ExpectExec("UPDATE resource_versions SET status = 'deprecated'").
  WithArgs("knowledge", "ws-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 pool.ExpectExec("UPDATE resource_versions SET status = 'published', published_at = NOW").
  WithArgs("knowledge", "ws-1", "v2").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 pool.ExpectExec("UPDATE rag_workspaces SET active_version_id").
  WithArgs("v2", "ws-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))

 repo := NewWorkspaceRepo(pool)
 err := repo.RollbackWorkspace(context.Background(), "tenant-1", "kb", snap, "", "v2", nil)
 require.NoError(t, err)
 require.NoError(t, pool.ExpectationsWereMet())
}
```

> **测试注意：** 上面的 SQL 正则以 `pkg/versioning/version_tx.go` 的实际 SQL 为准；若实际 SQL 文本与正则不符，先读 `version_tx.go` 的 `DemoteCurrentTx`/`InsertVersionTx`/`SetActiveTx`/`RollbackVersionTx` 原文，再调整正则。pgxmock 对正则做匹配，`\\.` 转义字面点。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/knowledge/infrastructure/persistence/ -run 'TestWorkspaceRepo|TestWorkspaceRepoRollback' -v`
Expected: PASS（既有用例 + 新增 3 个用例全绿）。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add internal/knowledge/infrastructure/persistence/workspace_repo.go internal/knowledge/infrastructure/persistence/workspace_repo_rollback_test.go
git commit -m "feat(knowledge): write product versions on workspace update and rollback

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: WorkspaceService 版本历史与回滚编排

在 `WorkspaceService` 注入 versioning 依赖，新增 `ListWorkspaceVersions` / `RollbackWorkspace`（镜像 `internal/agent/application/agent_version.go`），`UpdateWorkspace` 改传快照。

**Files:**

- Modify: `internal/knowledge/application/workspace_service.go`
- Create: `internal/knowledge/application/workspace_version.go`（VersionDTO + versionToDTO + ListWorkspaceVersions + resolveWorkspaceVersionNames + RollbackWorkspace）
- Test: `internal/knowledge/application/workspace_version_test.go`（新增）

**Interfaces:**

- Consumes:
  - `port.VersionRepo`（`internal/versioning/domain/port/version_repository.go`：`ListVersions(ctx, tenantID, kind, resourceID) ([]domain.Version, error)`、`GetVersion(ctx, tenantID, kind, resourceID, versionID) (domain.Version, bool, error)`）
  - `versioningdomain.ResourceKindKnowledge` / `VersionStatusDeprecated` / `ErrVersionNotFound`（`internal/versioning/domain/version.go`）
  - `domain.SnapshotFromWorkspace` / `SnapshotFromMap`（Task 2）、`port.ActorNameResolver`（Task 3）
  - `UpdateWorkspaceAll(ctx, tenantID, name, renameTo, description, snap, editorActor, actorID, audit)`（Task 3/4）
- Produces:
  - `type WorkspaceVersionDTO struct { ID string; VersionNo int; Status, Source, ContentHash, CreatedBy, CreatedByName, CreatedAt, PublishedAt string; IsCurrent bool; SafeSummary map[string]any }`
  - `type RollbackWorkspaceInput struct { ActorID, VersionID string }`
  - `func (s *WorkspaceService) SetVersionRepo(r port.VersionRepo)` / `func (s *WorkspaceService) SetActorNameResolver(r port.ActorNameResolver)`
  - `func (s *WorkspaceService) ListWorkspaceVersions(ctx, tenantID, name string) ([]WorkspaceVersionDTO, error)`（versionRepo nil → fail-closed error）
  - `func (s *WorkspaceService) RollbackWorkspace(ctx, tenantID, name string, in RollbackWorkspaceInput) (*domain.Workspace, error)`
  - `UpdateWorkspace` 改为：`s.repo.UpdateWorkspaceAll(ctx, tenantID, name, renameTo, in.Description, domain.SnapshotFromWorkspace(after), editorActor, actorID, audit)`

- [ ] **Step 1: 写失败测试（ListWorkspaceVersions + RollbackWorkspace）**

创建 `internal/knowledge/application/workspace_version_test.go`：

```go
package application

import (
 "context"
 "testing"

 "github.com/stretchr/testify/require"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/knowledge/domain"
 versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

type stubVersionRepo struct {
 versions []versioningdomain.Version
 get      func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error)
}

func (s *stubVersionRepo) ListVersions(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID string) ([]versioningdomain.Version, error) {
 return s.versions, nil
}

func (s *stubVersionRepo) GetVersion(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
 if s.get != nil {
  return s.get(ctx, tenantID, kind, resourceID, versionID)
 }
 return versioningdomain.Version{}, false, nil
}

func TestWorkspaceServiceListWorkspaceVersions(t *testing.T) {
 repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{"kb": {ID: "ws-1", Name: "kb"}}}
 svc := NewWorkspaceService(repo, nil, zap.NewNop())
 svc.SetVersionRepo(&stubVersionRepo{versions: []versioningdomain.Version{{
  ID: "v2", ResourceKind: versioningdomain.ResourceKindKnowledge,
  Status: versioningdomain.VersionStatusDeprecated, Source: versioningdomain.VersionSourceManual,
  CreatedBy: "u1", SafeSummary: map[string]any{"name": "kb"},
 }}})
 svc.SetActorNameResolver(&stubNameResolver{names: map[string]string{"u1": "张三"}})

 got, err := svc.ListWorkspaceVersions(context.Background(), "tenant-1", "kb")
 require.NoError(t, err)
 require.Len(t, got, 1)
 require.Equal(t, "v2", got[0].ID)
 require.Equal(t, "deprecated", got[0].Status)
 require.Equal(t, "张三", got[0].CreatedByName)
}

func TestWorkspaceServiceRollbackWorkspace(t *testing.T) {
 repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{
  "kb": {ID: "ws-1", Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}},
 }}
 svc := NewWorkspaceService(repo, nil, zap.NewNop())
 svc.SetVersionRepo(&stubVersionRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
  require.Equal(t, "ws-1", resourceID)
  return versioningdomain.Version{
   ID: "v1", Status: versioningdomain.VersionStatusDeprecated,
   Payload: domain.SnapshotFromWorkspace(&domain.Workspace{Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}}).Map(),
  }, true, nil
 }})

 ws, err := svc.RollbackWorkspace(context.Background(), "tenant-1", "kb", RollbackWorkspaceInput{ActorID: "u1", VersionID: "v1"})
 require.NoError(t, err)
 require.Equal(t, "old", ws.Name)
 require.Equal(t, "od", ws.Description)
 require.Equal(t, 4, ws.Config.TopK)
}

func TestWorkspaceServiceRollbackWorkspace_NonDeprecatedFailsClosed(t *testing.T) {
 repo := &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{"kb": {ID: "ws-1", Name: "kb"}}}
 svc := NewWorkspaceService(repo, nil, zap.NewNop())
 svc.SetVersionRepo(&stubVersionRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
  return versioningdomain.Version{ID: "v1", Status: versioningdomain.VersionStatusPublished}, true, nil
 }})

 _, err := svc.RollbackWorkspace(context.Background(), "tenant-1", "kb", RollbackWorkspaceInput{ActorID: "u1", VersionID: "v1"})
 require.ErrorIs(t, err, versioningdomain.ErrVersionNotFound)
}

type stubNameResolver struct {
 names map[string]string
}

func (s *stubNameResolver) ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error) {
 return s.names, nil
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/knowledge/application/ -run TestWorkspaceService.*Version -v`
Expected: FAIL —— `SetVersionRepo` / `ListWorkspaceVersions` / `RollbackWorkspace` / `WorkspaceVersionDTO` 未定义。

- [ ] **Step 3: 实现 service 字段注入 + 版本编排**

在 `internal/knowledge/application/workspace_service.go` 的 struct 增加字段（紧跟 `editorRepo` 之后）：

```go
 editorRepo   port.ResourceEditorRepo
 versionRepo  port.VersionRepo
 nameResolver port.ActorNameResolver
 modelExists  port.ModelExists
```

在 Set 方法区追加：

```go
// SetVersionRepo injects the product version repository used for version
// history and rollback. A nil repo fails list/rollback closed.
func (s *WorkspaceService) SetVersionRepo(r port.VersionRepo) { s.versionRepo = r }

// SetActorNameResolver injects actor display-name resolution for version
// history. A nil resolver skips name resolution (display falls back to id).
func (s *WorkspaceService) SetActorNameResolver(r port.ActorNameResolver) { s.nameResolver = r }
```

在 `workspace_service.go` 顶部 import 区增加 `versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"`（供 port 别名对齐；port 包名已足够）。将 `UpdateWorkspace` 中的调用改为：

```go
 if err := s.repo.UpdateWorkspaceAll(ctx, tenantID, name, renameTo, in.Description,
  domain.SnapshotFromWorkspace(after), editorActor, actorID, audit); err != nil {
  s.recordFailure(ctx, current.ID, "update", err)
  return nil, err
 }
```

创建 `internal/knowledge/application/workspace_version.go`：

```go
package application

import (
 "context"
 "fmt"

 "github.com/byteBuilderX/stratum/internal/knowledge/domain"
 versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// WorkspaceVersionDTO 是工作区版本历史的响应形状，字段与 agent 的 VersionDTO
// 对齐（前端共用 VersionHistory 组件）。
type WorkspaceVersionDTO struct {
 ID            string
 VersionNo     int
 Status        string
 Source        string
 ContentHash   string
 CreatedBy     string
 CreatedByName string
 CreatedAt     string // RFC3339
 PublishedAt   string // RFC3339；未发布为空串
 IsCurrent     bool
 SafeSummary   map[string]any
}

// RollbackWorkspaceInput carries the actor performing the rollback and the
// target version (by ID) to restore.
type RollbackWorkspaceInput struct {
 ActorID   string
 VersionID string
}

func workspaceVersionToDTO(v versioningdomain.Version) WorkspaceVersionDTO {
 return WorkspaceVersionDTO{
  ID:            v.ID,
  VersionNo:     v.RevisionNo,
  Status:        string(v.Status),
  Source:        string(v.Source),
  ContentHash:   v.ContentHash,
  CreatedBy:     v.CreatedBy,
  CreatedByName: v.CreatedByName,
  CreatedAt:     v.CreatedAt.Format(time.RFC3339),
  PublishedAt:   v.PublishedAt.Format(time.RFC3339),
  IsCurrent:     v.IsCurrent,
  SafeSummary:   v.SafeSummary,
 }
}

// ListWorkspaceVersions returns the workspace's product version history
// (newest first) with created_by display names resolved.
func (s *WorkspaceService) ListWorkspaceVersions(ctx context.Context, tenantID, name string) ([]WorkspaceVersionDTO, error) {
 if s.versionRepo == nil {
  return nil, fmt.Errorf("knowledge service list workspace versions: version repo not wired")
 }
 ws, err := s.repo.GetByName(ctx, tenantID, name)
 if err != nil {
  return nil, err
 }
 versions, err := s.versionRepo.ListVersions(ctx, tenantID, versioningdomain.ResourceKindKnowledge, ws.ID)
 if err != nil {
  return nil, err
 }
 dtos := make([]WorkspaceVersionDTO, 0, len(versions))
 for _, v := range versions {
  dtos = append(dtos, workspaceVersionToDTO(v))
 }
 if err := s.resolveWorkspaceVersionNames(ctx, tenantID, dtos); err != nil {
  return nil, err
 }
 return dtos, nil
}

// resolveWorkspaceVersionNames 批量解析版本操作者昵称（display_name >
// github_login > actor_id）。nameResolver 为 nil 时跳过（id 原文展示）。
func (s *WorkspaceService) resolveWorkspaceVersionNames(ctx context.Context, tenantID string, versions []WorkspaceVersionDTO) error {
 if s.nameResolver == nil {
  return nil
 }
 actorIDs := make([]string, 0, len(versions))
 seen := make(map[string]struct{}, len(versions))
 for _, v := range versions {
  if _, ok := seen[v.CreatedBy]; ok {
   continue
  }
  seen[v.CreatedBy] = struct{}{}
  actorIDs = append(actorIDs, v.CreatedBy)
 }
 names, err := s.nameResolver.ResolveActorNames(ctx, actorIDs)
 if err != nil {
  return err
 }
 for i := range versions {
  if n, ok := names[versions[i].CreatedBy]; ok {
   versions[i].CreatedByName = n
  }
 }
 return nil
}

// RollbackWorkspace restores a deprecated historical version. The version
// payload is rebuilt into a snapshot, applied back to the workspace row, and
// active_version_id repointed at it — all in the repo's transaction. No new
// version is created. Returns the fresh workspace (re-read after the write).
func (s *WorkspaceService) RollbackWorkspace(ctx context.Context, tenantID, name string, in RollbackWorkspaceInput) (*domain.Workspace, error) {
 if s.versionRepo == nil {
  return nil, fmt.Errorf("knowledge service rollback workspace: version repo not wired")
 }
 current, err := s.repo.GetByName(ctx, tenantID, name)
 if err != nil {
  return nil, err
 }
 target, found, err := s.versionRepo.GetVersion(ctx, tenantID, versioningdomain.ResourceKindKnowledge, current.ID, in.VersionID)
 if err != nil {
  return nil, err
 }
 // 仅 deprecated 历史版本可回滚（fail-closed，对齐 agent）。
 if !found || target.Status != versioningdomain.VersionStatusDeprecated {
  return nil, versioningdomain.ErrVersionNotFound
 }
 // 权限：沿用更新矩阵（owner/admin → editorActor="", 白名单 editor →
 // editorActor=actorID，repo 事务内重校验）。
 editorActor, err := s.resolveUpdateActor(ctx, tenantID, in.ActorID, current)
 if err != nil {
  return nil, err
 }
 snap, err := domain.SnapshotFromMap(target.Payload)
 if err != nil {
  return nil, fmt.Errorf("knowledge service rollback workspace: parse version payload: %w", err)
 }
 after := snap.ToWorkspace(current.ID)
 audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, current.ID, auditdomain.ChangeOpUpdate,
  in.ActorID, KnowledgeSafeProjection(current), KnowledgeSafeProjection(after))
 if err != nil {
  return nil, err
 }
 if err := s.repo.RollbackWorkspace(ctx, tenantID, name, snap, editorActor, in.VersionID, audit); err != nil {
  s.recordFailure(ctx, current.ID, "rollback", err)
  return nil, err
 }
 // 回滚后重读最新行（名称/描述/配置已被版本数据覆盖）。
 return s.repo.GetByID(ctx, tenantID, current.ID)
}
```

> 注意：`workspaceVersionToDTO` 用到 `time`，请在 `workspace_version.go` 顶部补 `import "time"`；`resolveWorkspaceVersionNames` 用到 `auditdomain`/`auditport` 不需要（本项目函数签名里没有），但 `RollbackWorkspace` 用到的 `auditdomain`/`ChangeOpUpdate` 需在文件顶部 import。若 `KnowledgeSafeProjection`/`newChangeAudit` 与本文件 import 分组冲突，按 `change_audit.go` 的既有 import 组组织。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./... && go test ./internal/knowledge/application/ -run 'TestWorkspaceService.*Version|TestWorkspaceServiceUpdate' -v`
Expected: PASS（含既有 `TestWorkspaceServiceUpdate*` 用例——UpdateWorkspace 传快照后语义不变）。

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add internal/knowledge/application/workspace_service.go internal/knowledge/application/workspace_version.go internal/knowledge/application/workspace_version_test.go
git commit -m "feat(knowledge): add workspace version history and rollback orchestration

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: HTTP DTO + handler + 路由 + 契约

新增 `GET /knowledge/workspaces/:name/versions` 与 `POST /knowledge/workspaces/:name/rollback`，DTO 对齐 agent 版本响应形状。

**Files:**

- Modify: `api/http/handler/rag_dto.go`（新增 `WorkspaceVersionResponse` / `WorkspaceVersionsResponse` / `RollbackWorkspaceRequest` / `workspaceVersionToResponse`）
- Modify: `api/http/handler/rag_handler.go`（新增 `ListWorkspaceVersions` / `RollbackWorkspace` handler 方法）
- Modify: `api/http/router.go`（`registerKnowledge` 内注册两条路由）
- Test: `api/http/handler/rag_version_handler_test.go`（新增）
- Test: `api/http/testdata/contracts/*.golden.json`（`make record-contracts` 记录新端点的 `default-unauth` 401）

**Interfaces:**

- Consumes: `WorkspaceService.ListWorkspaceVersions(ctx, tenantID, name string) ([]WorkspaceVersionDTO, error)`、`RollbackWorkspace(ctx, tenantID, name string, in RollbackWorkspaceInput) (*domain.Workspace, error)`（Task 5）、`toDTOConfig`（已有）
- Produces:
  - `WorkspaceVersionResponse{ID, VersionNo, Status, Source, ContentHash, CreatedBy, CreatedByName, CreatedAt, PublishedAt, IsCurrent, SafeSummary}`（json tag 同 `agent_dto.go` 的 `AgentVersionResponse`）
  - `WorkspaceVersionsResponse{Versions []WorkspaceVersionResponse}`（`json:"versions"`）
  - `RollbackWorkspaceRequest{VersionID string \`json:"versionId" binding:"required"\`}`
  - 路由：`knowledgeGroup.GET("/workspaces/:name/versions", requireActive, ragHandler.ListWorkspaceVersions)`；`knowledgeGroup.POST("/workspaces/:name/rollback", append(adminMW, requireActive, ragHandler.RollbackWorkspace)...)`

- [ ] **Step 1: 写失败测试（handler 版本历史 + 回滚）**

创建 `api/http/handler/rag_version_handler_test.go`：

```go
package handler

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/gin-gonic/gin"
 "github.com/stretchr/testify/require"
 "go.uber.org/zap"

 knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
 "github.com/byteBuilderX/stratum/internal/knowledge/domain"
 versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// versionHandlerWorkspaceRepo 满足 WorkspaceRepo port：GetByName/GetByID 返回
// 固定 workspace，其余 no-op。
type versionHandlerWorkspaceRepo struct {
 ws *domain.Workspace
}

func (r *versionHandlerWorkspaceRepo) Create(context.Context, string, *domain.Workspace, []string, interface{}) error { return nil }
func (r *versionHandlerWorkspaceRepo) GetByName(context.Context, string, string) (*domain.Workspace, error) { return r.ws, nil }
func (r *versionHandlerWorkspaceRepo) GetByID(context.Context, string, string) (*domain.Workspace, error)  { return r.ws, nil }
func (r *versionHandlerWorkspaceRepo) List(context.Context, string) ([]*domain.Workspace, error)           { return nil, nil }
func (r *versionHandlerWorkspaceRepo) UpdateWorkspaceAll(context.Context, string, string, *string, *string, domain.KnowledgeWorkspaceSnapshot, string, string, interface{}) error {
 return nil
}
func (r *versionHandlerWorkspaceRepo) RollbackWorkspace(context.Context, string, string, domain.KnowledgeWorkspaceSnapshot, string, string, interface{}) error {
 return nil
}
func (r *versionHandlerWorkspaceRepo) Delete(context.Context, string, string, interface{}) error            { return nil }
func (r *versionHandlerWorkspaceRepo) GetConfigForUpload(context.Context, string, string) (domain.WorkspaceConfig, error) {
 return r.ws.Config, nil
}
func (r *versionHandlerWorkspaceRepo) GetConfigByID(context.Context, string, string) (domain.WorkspaceConfig, error) {
 return r.ws.Config, nil
}

func TestRAGHandlerListWorkspaceVersions(t *testing.T) {
 svc := knowledge.NewWorkspaceService(&versionHandlerWorkspaceRepo{ws: &domain.Workspace{ID: "ws-1", Name: "kb"}}, nil, zap.NewNop())
 svc.SetVersionRepo(&stubVersionRepo{versions: []versioningdomain.Version{{
  ID: "v2", ResourceKind: versioningdomain.ResourceKindKnowledge,
  Status: versioningdomain.VersionStatusDeprecated, Source: versioningdomain.VersionSourceManual,
  RevisionNo: 2, CreatedBy: "u1", SafeSummary: map[string]any{"name": "kb"},
 }}})

 h := NewRAGHandler(nil, svc, zap.NewNop())
 r := newRouterWithErrorHandler()
 r.GET("/knowledge/workspaces/:name/versions", injectRAGTenant("tenant-1"), h.ListWorkspaceVersions)

 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodGet, "/knowledge/workspaces/kb/versions", nil)
 r.ServeHTTP(w, req)

 require.Equal(t, http.StatusOK, w.Code)
 var body WorkspaceVersionsResponse
 require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
 require.Len(t, body.Versions, 1)
 require.Equal(t, "v2", body.Versions[0].ID)
 require.Equal(t, 2, body.Versions[0].VersionNo)
}

func TestRAGHandlerRollbackWorkspace(t *testing.T) {
 ws := &domain.Workspace{ID: "ws-1", Name: "kb", Description: "d", Config: domain.WorkspaceConfig{TopK: 8}}
 repo := &versionHandlerWorkspaceRepo{ws: ws}
 svc := knowledge.NewWorkspaceService(repo, nil, zap.NewNop())
 svc.SetVersionRepo(&stubVersionRepo{get: func(ctx context.Context, tenantID string, kind versioningdomain.ResourceKind, resourceID, versionID string) (versioningdomain.Version, bool, error) {
  return versioningdomain.Version{ID: "v1", Status: versioningdomain.VersionStatusDeprecated,
   Payload: domain.SnapshotFromWorkspace(&domain.Workspace{Name: "old", Description: "od", Config: domain.WorkspaceConfig{TopK: 4}}).Map()}, true, nil
 }})

 h := NewRAGHandler(nil, svc, zap.NewNop())
 r := newRouterWithErrorHandler()
 r.POST("/knowledge/workspaces/:name/rollback", injectRAGTenant("tenant-1"), h.RollbackWorkspace)

 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPost, "/knowledge/workspaces/kb/rollback", strings.NewReader(`{"versionId":"v1"}`))
 req.Header.Set("Content-Type", "application/json")
 r.ServeHTTP(w, req)

 require.Equal(t, http.StatusOK, w.Code)
 var body map[string]any
 require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
 require.Equal(t, "old", body["name"])
}
```

> 注意：`injectRAGTenant` / `newRouterWithErrorHandler` 已存在于 `rag_preview_handler_test.go` 等既有测试文件；若本文件需要，直接复用（同一 package）。`stubVersionRepo` 已在 `internal/knowledge/application/workspace_version_test.go` 定义，但 handler 包是不同 package（`handler` vs `application`），需在本文件再定义一份（或提取到共享测试 helper——本仓库当前各包独立定义，遵循现状）。`interface{}` 用于 audit 参数以避开重复定义 `*auditdomain.ResourceChangeAuditEvent` 的 import 冲突；若编译要求精确类型，改为 `*auditdomain.ResourceChangeAuditEvent` 并补 import。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./api/http/handler/ -run 'TestRAGHandler.*Version' -v`
Expected: FAIL —— `ListWorkspaceVersions` / `RollbackWorkspace`（handler 方法）未定义；`WorkspaceVersionsResponse` 未定义。

- [ ] **Step 3: 添加 DTO**

在 `api/http/handler/rag_dto.go` 追加：

```go
// WorkspaceVersionResponse mirrors knowledge.WorkspaceVersionDTO for the wire.
// Field shapes are frozen by contract tests; do not rename.
type WorkspaceVersionResponse struct {
 ID            string         `json:"id"`
 VersionNo     int            `json:"versionNo"`
 Status        string         `json:"status"`
 Source        string         `json:"source"`
 ContentHash   string         `json:"contentHash"`
 CreatedBy     string         `json:"createdBy"`
 CreatedByName string         `json:"createdByName"`
 CreatedAt     string         `json:"createdAt"`
 PublishedAt   string         `json:"publishedAt"`
 IsCurrent     bool           `json:"isCurrent"`
 SafeSummary   map[string]any `json:"safeSummary"`
}

// WorkspaceVersionsResponse wraps the version history list (newest first),
// matching the agent versions response shape for frontend symmetry.
type WorkspaceVersionsResponse struct {
 Versions []WorkspaceVersionResponse `json:"versions"`
}

// RollbackWorkspaceRequest carries the target historical version to restore.
type RollbackWorkspaceRequest struct {
 VersionID string `json:"versionId" binding:"required"`
}

// workspaceVersionToResponse maps the service-side DTO to the wire shape.
func workspaceVersionToResponse(v knowledge.WorkspaceVersionDTO) WorkspaceVersionResponse {
 return WorkspaceVersionResponse{
  ID:            v.ID,
  VersionNo:     v.VersionNo,
  Status:        v.Status,
  Source:        v.Source,
  ContentHash:   v.ContentHash,
  CreatedBy:     v.CreatedBy,
  CreatedByName: v.CreatedByName,
  CreatedAt:     v.CreatedAt,
  PublishedAt:   v.PublishedAt,
  IsCurrent:     v.IsCurrent,
  SafeSummary:   v.SafeSummary,
 }
}
```

- [ ] **Step 4: 添加 handler 方法**

在 `api/http/handler/rag_handler.go` 追加（放在 `UpdateWorkspace` 之后）：

```go
// ListWorkspaceVersions returns the workspace's product version history
// (newest first) with created_by display names resolved.
func (h *RAGHandler) ListWorkspaceVersions(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 versions, err := h.wsService.ListWorkspaceVersions(c.Request.Context(), tenantID, c.Param("name"))
 if err != nil {
  _ = c.Error(err)
  return
 }
 out := make([]WorkspaceVersionResponse, 0, len(versions))
 for _, v := range versions {
  out = append(out, workspaceVersionToResponse(v))
 }
 c.JSON(http.StatusOK, WorkspaceVersionsResponse{Versions: out})
}

// RollbackWorkspace restores a deprecated historical version, repointing the
// workspace to it immediately without creating a new version. Returns the
// fresh workspace so the client can re-render in place.
func (h *RAGHandler) RollbackWorkspace(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req RollbackWorkspaceRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actorID, ok := userIDFromCtx(c)
 if !ok {
  respondMissingUser(c)
  return
 }
 ws, err := h.wsService.RollbackWorkspace(c.Request.Context(), tenantID, c.Param("name"), knowledge.RollbackWorkspaceInput{
  ActorID:   actorID,
  VersionID: req.VersionID,
 })
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"name": ws.Name, "description": ws.Description, "config": toDTOConfig(ws.Config)})
}
```

- [ ] **Step 5: 注册路由**

在 `api/http/router.go` 的 `registerKnowledge` 内，`knowledgeGroup.PATCH("/workspaces/:name", ...)` 之后追加：

```go
  // 版本历史/回滚：历史 GET member 级（对齐 agent/skill），回滚写 admin
  //（spec：入口仅 isAdmin 可见）。
  knowledgeGroup.GET("/workspaces/:name/versions", requireActive, ragHandler.ListWorkspaceVersions)
  knowledgeGroup.POST("/workspaces/:name/rollback", append(adminMW, requireActive, ragHandler.RollbackWorkspace)...)
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./... && go test ./api/http/handler/ -run 'TestRAGHandler.*Version' -v`
Expected: PASS。

- [ ] **Step 7: 记录契约 golden（新端点 401）并回归**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && make record-contracts && git diff --stat api/http/testdata/contracts/`
Expected: `make record-contracts` 成功；`git diff` 显示 golden 变更**仅新增** knowledge 两个端点（`GET /knowledge/workspaces/kb/versions` 与 `POST /knowledge/workspaces/kb/rollback`）的 `default-unauth` 401 条目，以及既有契约因端口/路由重排产生的行移位（如有）。然后：

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./api/http/ -run TestHTTPContract -v`
Expected: PASS。

> 若 `git diff` 出现与本次改动无关的大面积 golden 变更（例如同一租户场景下多条既有端点响应变化），先读 `contract_test.go` 的 stub 是否被本 Task 误改；契约 golden 只能随本次新增端点变化，禁止顺带改写其它端点。

- [ ] **Step 8: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add api/http/handler/rag_dto.go api/http/handler/rag_handler.go api/http/handler/rag_version_handler_test.go api/http/router.go api/http/testdata/contracts/
git commit -m "feat(knowledge): expose workspace version history and rollback endpoints

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: wiring 装配 versioning 依赖

在 `api/wiring/knowledge.go` 为 `WorkspaceService` 注入 `PgVersionRepo` 与 actor 名解析器（镜像 agent wiring）。

**Files:**

- Modify: `api/wiring/knowledge.go`（`buildKnowledge` 的 `db != nil` 块）

**Interfaces:**

- Consumes: `versioningpersistence.NewPgVersionRepo(db)`、`iampersistence.NewPgActorNameResolver(db)`（同 `api/wiring/agent.go:367-372` 的装配）、`WorkspaceService.SetVersionRepo` / `SetActorNameResolver`（Task 5）
- Produces: `WorkspaceService` 具备完整版本历史/回滚能力

- [ ] **Step 1: 先读 wiring 现状，确认 import 组**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && sed -n '100,135p' api/wiring/knowledge.go`
Expected: 看到 `WorkspaceService` 构造与 `SetDocRepo`/`SetVectorStore`/`SetEditorRepo`/`SetFailureAuditRecorder` 调用块。

- [ ] **Step 2: 注入版本依赖**

在 `api/wiring/knowledge.go` 的 `db != nil` 块内、`SetFailureAuditRecorder` 之后追加：

```go
 wsSvc.SetVersionRepo(versioningpersistence.NewPgVersionRepo(db))
 wsSvc.SetActorNameResolver(iampersistence.NewPgActorNameResolver(db))
```

并在文件顶部 import 区增加（分组：third-party 之后 internal）：

```go
 "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence" // 若包名冲突,用 iampersistence 别名
 "github.com/byteBuilderX/stratum/internal/versioning/infrastructure/persistence" // 若包名冲突,用 versioningpersistence 别名
```

> 若 `versioning/infrastructure/persistence` 与 `iam/infrastructure/persistence` 的包名在 `knowlede.go` 已因其它 import 冲突而需别名，则用 `versioningpersistence.` / `iampersistence.` 前缀（与 `api/wiring/agent.go:367-372` 完全一致）。

- [ ] **Step 3: 验证编译与契约**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./... && go test ./api/wiring/ ./api/http/ -short`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add api/wiring/knowledge.go
git commit -m "feat(knowledge): wire versioning repo and actor name resolver

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: 前端——版本历史 + 回滚 + 撤销未保存编辑

在 `KnowledgeDetailPage` 接入共享 `VersionHistory` 组件、回滚与「撤销」按钮（纯前端撤销）。

**Files:**

- Modify: `web/src/modules/knowledge/api/knowledge.api.ts`（新增 `listVersions` / `rollback`）
- Modify: `web/src/modules/knowledge/model/knowledge.ts`（新增 `workspaceVersionSchema`）
- Modify: `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx`（版本历史入口 + 撤销按钮）
- Modify: `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts`（版本状态 + 撤销回填逻辑）
- Modify: `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`（新增 `onUndo` 撤销按钮）

**Interfaces:**

- Consumes: `web/src/shared/ui/VersionHistory.tsx`（`VersionRow{id, versionNo?, status, isCurrent?, createdByName?, createdBy?, createdAt?, canRollback?, summary?}`，`rollback?: (row) => Promise<void>`）、`useKnowledgeDetailPage.ts` 既有 `configForm`/`lastLoadedConfig`/`fetchStats`/`handleConfigSave`、`isAdmin`（`useTenantRole` 或既有 hook）、`message`/`Modal`（AntD）
- Produces:
  - `knowledgeApi.listVersions(name): Promise<WorkspaceVersion[]>` / `knowledgeApi.rollback(name, versionId): Promise<void>`
  - `workspaceVersionSchema`（zod）
  - 版本历史 Modal（仅 `isAdmin` 渲染入口）：`rows = versions.map(v => ({ id, versionNo, status, isCurrent, createdByName, createdBy, createdAt, canRollback: v.status === 'deprecated' && isAdmin, summary }))`，`rollback` 注入 `() => { await knowledgeApi.rollback(name, row.id); await reload(); }`
  - 「撤销」按钮：`Modal.confirm` → `await reloadWorkspace()` → `configForm.setFieldsValue({ ...lastLoadedConfig })`（清空未保存编辑）

- [ ] **Step 1: 先读前端现状（锚点）**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && sed -n '80,95p' web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx && sed -n '200,220p' web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`
Expected: 看到 `{isAdmin && <WorkspaceConfigForm .../>}`（第 90-92 行）与保存按钮（第 211-214 行）。

- [ ] **Step 2: API 与模型**

`web/src/modules/knowledge/api/knowledge.api.ts` 追加：

```ts
export interface WorkspaceVersion {
  id: string;
  versionNo?: number;
  status: string;
  source?: string;
  contentHash?: string;
  createdBy?: string;
  createdByName?: string;
  createdAt?: string;
  publishedAt?: string;
  isCurrent?: boolean;
  safeSummary?: Record<string, unknown>;
}
```

在 `knowledgeApi` 对象内追加：

```ts
  listVersions: (name: string): Promise<WorkspaceVersion[]> =>
    client.get<{ versions: WorkspaceVersion[] }>(`/knowledge/workspaces/${encodeURIComponent(name)}/versions`)
      .then((res) => res.data.versions),
  rollback: (name: string, versionId: string): Promise<void> =>
    client.post(`/knowledge/workspaces/${encodeURIComponent(name)}/rollback`, { versionId }).then(() => undefined),
```

`web/src/modules/knowledge/model/knowledge.ts` 追加（放在 `workspaceConfigSchema` 之后）：

```ts
export const workspaceVersionSchema = z.object({
  id: z.string(),
  versionNo: z.number().optional(),
  status: z.string(),
  source: z.string().optional().default(''),
  contentHash: z.string().optional().default(''),
  createdBy: z.string().optional().default(''),
  createdByName: z.string().optional().default(''),
  createdAt: z.string().optional().default(''),
  publishedAt: z.string().optional().default(''),
  isCurrent: z.boolean().optional().default(false),
  safeSummary: z.record(z.unknown()).optional().default({}),
}).passthrough();
export type WorkspaceVersion = z.infer<typeof workspaceVersionSchema>;
```

- [ ] **Step 3: hook 增加版本与撤销状态**

`web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts` 增加（在既有 reload/configForm 状态旁）：

```ts
  // 版本历史（仅 isAdmin 渲染入口）
  const [versions, setVersions] = useState<WorkspaceVersion[]>([]);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [versionsLoading, setVersionsLoading] = useState(false);

  const openVersions = useCallback(async () => {
    setVersionsOpen(true);
    setVersionsLoading(true);
    try {
      setVersions(await knowledgeApi.listVersions(name));
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载版本历史失败'), duration: 3 });
      setVersionsOpen(false);
    } finally {
      setVersionsLoading(false);
    }
  }, [name]);

  const rollbackVersion = useCallback(async (row: VersionRow) => {
    await knowledgeApi.rollback(name, row.id);
    await reload();
  }, [name, reload]);

  // 撤销未保存编辑：重拉最新 workspace 数据回填表单（纯前端）。
  const undoEdits = useCallback(async () => {
    const fresh = await reload();
    if (fresh) {
      configForm.setFieldsValue({
        ...lastLoadedConfig.current,
        name: fresh.name,
        description: fresh.description,
      });
    }
  }, [reload, configForm, lastLoadedConfig]);
```

> 若 `useKnowledgeDetailPage.ts` 没有 `reload` 这样的函数名，以该文件既有的「重拉 workspace」逻辑（`fetchWorkspace`/`loadDetail` 等）为准，把 `reload` 替换成既有函数；`lastLoadedConfig` 是既有 ref（保存时记录最近一次已生效 config），撤销用它回填。`reload` 需返回最新 workspace（或改为从既有 state 读取）。若 hook 无 `reload`，先补一个返回 `Workspace | null` 的重拉函数。

- [ ] **Step 4: 页面接入版本历史 Modal 与撤销入口**

`web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx`：

- 页面顶部（标题行，`{isAdmin && ...}` 现有块旁）加「版本历史」按钮：

```tsx
  {isAdmin && (
    <Button icon={<HistoryOutlined />} onClick={openVersions}>版本历史</Button>
  )}
```

- 渲染版本历史 Modal（页面 JSX 底部）：

```tsx
  <Modal
    title="版本历史"
    open={versionsOpen}
    onCancel={() => setVersionsOpen(false)}
    footer={null}
    width={760}
  >
    <VersionHistory
      rows={versions.map((v) => ({
        id: v.id,
        versionNo: v.versionNo,
        status: v.status,
        isCurrent: v.isCurrent,
        createdByName: v.createdByName,
        createdBy: v.createdBy,
        createdAt: v.createdAt,
        canRollback: v.status === 'deprecated' && isAdmin,
        summary: v.safeSummary,
      }))}
      loading={versionsLoading}
      rollback={rollbackVersion}
    />
  </Modal>
```

> import 增加：`import { VersionHistory } from '@/shared/ui/VersionHistory';`、`import { HistoryOutlined } from '@ant-design/icons';`、`import { Modal, Button } from 'antd';`（如未导入）、`WorkspaceVersion`/`VersionRow` 类型。

- [ ] **Step 5: WorkspaceConfigForm 增加撤销按钮**

`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx` 的保存按钮（第 211-214 行）旁加撤销按钮：

```tsx
        {onUndo && (
          <Button onClick={handleUndo} disabled={loading}>
            撤销
          </Button>
        )}
```

props 接口增加 `onUndo?: () => void`，`handleUndo` 实现：

```tsx
  const handleUndo = () => {
    if (!onUndo) return;
    Modal.confirm({
      title: '撤销未保存的编辑？',
      content: '表单将重置为最近一次生效的配置。',
      okText: '撤销', cancelText: '取消',
      onOk: onUndo,
    });
  };
```

`KnowledgeDetailPage.tsx` 传 `onUndo={undoEdits}`。

- [ ] **Step 6: 前端校验**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && make fe-lint && make fe-build`
Expected: PASS（无 lint 错误、构建成功）。若 `knowledgeApi` 返回类型与 zod schema 冲突，以 `workspaceVersionSchema` 为准 `.passthrough()` 兜底。

- [ ] **Step 7: 后端全量回归（本 Task 是前端为主，跑影响面）**

Run: `cd /home/yang/go-projects/stratum-kb-version-skill-draft && go vet ./... && go test -short ./...`
Expected: PASS。

- [ ] **Step 8: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft
git add web/src/modules/knowledge/
git commit -m "feat(knowledge): workspace version history, rollback and undo UI

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 完成定义

- [ ] `go vet ./... && go test -short ./...` 全绿；`make code-quality` 无新增超标函数（圈复杂度 ≤10、认知 ≤15、行数 ≤120、嵌套 ≤4）。
- [ ] `go test ./api/http/ -run TestHTTPContract` 通过；golden 变更只含新增 knowledge 版本端点。
- [ ] 前端 `make fe-lint && make fe-build` 通过。
- [ ] 手动验收：保存工作区 → 版本历史出现新版本（`is_current` 轮换正确）；回滚 deprecated 版本 → 配置立即还原且不产生新版本；编辑后点撤销 → 表单回填最近生效配置。
- [ ] 按 `.test/verification.yaml` 风险级完成系统验收（`stratum-e2e-development` skill），R2 起含 e2e-short；涉及前后端联调与 DB 链路，不属最小验证门槛。
