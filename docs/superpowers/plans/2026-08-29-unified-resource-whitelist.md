# 统一成员白名单 + 权限申请 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agent / skill / mcp / knowledge workspace / knowledge_doc / workflow 六类资源都能设置普通成员白名单，所有普通成员都能通过统一「申请权限」入口发起 `grant_editor` 提案、由 admin/owner 审批后获得白名单（编辑权），前后端一致落地。

**Architecture:** 复用既有 `resource_editors` 共享白名单表 + `operation_proposals` 审批渠道（方案 A「共享机制 + 逐资源落地」）。后端：workflow 从 admin-only 改为 member 可编辑 + application 层所有权矩阵 fail-closed；申请入口扩到六类资源；grantEditor 闭包按 kind 分发落库。前端：新增共享 `useRequestEditorAccess` / `RequestEditorButton`，六类资源页面接入；workflow 前端 canEdit = admin/owner/白名单成员。

**Tech Stack:** Go 1.25.12（Gin、pgx v5、pgxmock v2、testify）、React 18.3 / TS / AntD 5 / Vite 6.4 / Zod、vitest + @testing-library/react、Playwright（无头 Chromium）。

## Global Constraints

- 六类资源白名单均等于「编辑权」；knowledge_doc 特殊（编辑/查看一体），保留 `allowed_users` + `allowed_roles` 角色级模型，不做迁移。
- 单级白名单：在白名单内可直接编辑，否则只读 + 申请；审批渠道统一走 `operation_proposals`（`grant_editor`）。
- `resource_editors.resource_kind` 新增 `workflow`；`workflow_definitions` 新增 `created_by TEXT NOT NULL DEFAULT ''`（幂等 backfill，历史空值回退为 admin/owner 可编辑）。
- workflow 所有权矩阵 fail-closed：owner 全放行；admin 本人为 creator 全放行、非 creator 仅 Update/Publish、禁 Delete；白名单成员仅 Update/Publish；未知角色 / 空 actor / 解析失败一律拒绝。Delete 路由保持 admin 中间件（应用层矩阵为纵深防御）。
- workflow Create 允许任何活跃 member，写 `created_by = actorID`，同一事务内自动授予 creator 为白名单成员。
- 白名单校验在写事务内复查（TOCTOU 关闭），复用 `resourceaccess.RevalidateEditorAccess`；resourceType 服务端白名单（六类）；授予幂等。
- mcp 白名单成员编辑配置仍走既有 D5 审批（`requireApprovalOrExecuteMCP` 拦截 member 写路径）——**范围外既有行为**，E2E 不得断言 mcp 白名单成员直接编辑成功。
- mcp `GET /mcp/servers/:id/config` 从 admin 改为 member（`filterMCPConfigValues` 已丢弃敏感键，Auth 只暴露 CredentialConfigured，不泄露密钥）。
- 知识库 workspace editors 以 workspace UUID 存储，申请时 `:name` 须先解析为 UUID（grant 闭包用 `GetWorkspace`）。
- 所有访问 tenant-scoped 表的 repository 方法必须通过 `execTenant` / 事务内 `tenantdb.FromContext`；port 方法显式携带 `tenantID string`。
- Go：行宽 ≤120；错误逐层 `fmt.Errorf("operation: %w", err)` wrap；日志只用 Zap；行为数字放 `defaults.go`/`pkg/constants`。
- 前端：普通 API 调用走 `web/src/services/client.ts`；错误通知 `message.error({ content: err.response?.data?.error || '操作失败', duration: 3 })`；成功 `message.success({ content: ..., duration: 2 })`；禁止 `alert()`/`console.log`；用户可见字符串中文；Bearer token 不入 Web Storage。
- 前端 canEdit = `isAdmin || isOwner || editors.includes(user.sub)`（`userSchema.sub`）。
- 测试：表驱动；mock 外部依赖不 mock 领域逻辑；修改 port 后立即同步所有 test mock/stub。
- 禁止在 `main` 分支提交；用 worktree + `gh pr create --base main`；PR 标题 `[type](scope): description`。
- 登录测试/验证必须使用无头浏览器（Playwright headless）；禁止输出 token/cookie/密钥。

---

### Task 1: tenant_schema.sql 新增 workflow_definitions.created_by + resource_editors workflow kind

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`（resource_editors 注释 193 行；workflow_definitions 1014-1031）
- Test: `pkg/storage/postgres/tenant_schema_safety_test.go`（追加一个测试函数）

**Interfaces:**

- Consumes: 现有 `resource_editors` 表结构（`resource_kind TEXT NOT NULL`，无 CHECK 约束）。
- Produces: `workflow_definitions.created_by TEXT NOT NULL DEFAULT ''` 列；`resource_editors.resource_kind` 文档注释含 `workflow`。Task 3 的 store INSERT/SELECT 依赖该列。

- [ ] **Step 1: Write the failing test**

在 `pkg/storage/postgres/tenant_schema_safety_test.go` 文件末尾追加：

```go
func TestTenantSchemaWorkflowEditorsAndCreatedBy(t *testing.T) {
 ddl, err := os.ReadFile("tenant_schema.sql")
 if err != nil {
  t.Fatal(err)
 }
 text := string(ddl)

 // workflow_definitions 必须带 created_by（所有权矩阵 creator 语义的存储基线）。
 if !strings.Contains(text, "workflow_definitions") {
  t.Fatal("tenant schema missing workflow_definitions")
 }
 if !strings.Contains(text, "created_by TEXT NOT NULL DEFAULT ''") {
  t.Fatal("workflow_definitions must carry created_by TEXT NOT NULL DEFAULT ''")
 }
 // 幂等 ALTER 用于升级历史租户。
 if !strings.Contains(text, "ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';") {
  t.Fatal("workflow_definitions must idempotently add created_by for historical tenants")
 }
 // resource_editors 注释声明 workflow kind（可申请编辑权的新资源类型）。
 if !strings.Contains(text, "agent|skill|mcp|knowledge|workflow") {
  t.Fatal("resource_editors kind comment must include workflow")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/storage/postgres/ -run TestTenantSchemaWorkflowEditorsAndCreatedBy -v`
Expected: FAIL（`workflow_definitions must carry created_by...`）。

- [ ] **Step 3: Implement the DDL change**

编辑 `pkg/storage/postgres/tenant_schema.sql`：

(1) 第 193 行注释 `resource_editors` 列说明：

```sql
CREATE TABLE IF NOT EXISTS resource_editors (
    resource_kind TEXT NOT NULL,                -- agent|skill|mcp|knowledge|workflow
```

(2) `workflow_definitions` CREATE TABLE 中 `description` 列后插入 `created_by`：

```sql
CREATE TABLE IF NOT EXISTS workflow_definitions (
    id              UUID        PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    name            TEXT        NOT NULL,
    description     TEXT        NOT NULL DEFAULT '',
    created_by      TEXT        NOT NULL DEFAULT '',   -- 创建者/creator，所有权矩阵 creator 语义
    -- 生效指针：指向 workflow_versions 当前生效版本（无 FK，与 agents.active_version_id 一致）
    active_version_id TEXT,
```

(3) 紧跟现有 ALTER 链（`description` 的 ALTER 之后）追加幂等升级语句：

```sql
ALTER TABLE workflow_definitions ADD COLUMN IF NOT EXISTS created_by TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/storage/postgres/ -run TestTenantSchemaWorkflowEditorsAndCreatedBy -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_safety_test.go
git commit -m "feat(workflow): add created_by column and workflow editor kind to tenant schema"
```

---

### Task 2: workflow domain 新增 ErrEditorNotEligible + Definition.CreatedBy/Editors + 新增 TenantRoleResolver/ResourceEditorRepo ports

**Files:**

- Modify: `internal/workflow/domain/workflow.go`（错误块 13-25；Definition struct 166-176）
- Create: `internal/workflow/domain/port/tenant_role_resolver.go`
- Create: `internal/workflow/domain/port/resource_editor.go`
- Test: `internal/workflow/domain/domain_editor_test.go`（新建）

**Interfaces:**

- Consumes: 现有 `auditdomain.ResourceChangeAuditEvent`。
- Produces: `domain.ErrEditorNotEligible`；`domain.Definition` 新增字段 `CreatedBy string \`json:"created_by,omitempty"\``、`Editors []string \`json:"editors,omitempty"\``；`port.TenantRoleResolver.ResolveTenantRole(context.Context, string, string) (string, error)`；`port.ResourceEditorRepo.ListEditors/ReplaceEditors`。Task 3/4 的 store 与 service 依赖。

- [ ] **Step 1: Write the failing test**

新建 `internal/workflow/domain/domain_editor_test.go`：

```go
package domain

import (
 "errors"
 "testing"

 "github.com/stretchr/testify/require"
)

func TestDefinitionCarriesEditorMetadata(t *testing.T) {
 def, err := NewDefinition("d1", "Research", "desc", Spec{}, InputSchema{})
 require.NoError(t, err)

 // created_by 创建时为空（Create 服务层写入）；editors 列表默认空切片。
 require.Equal(t, "", def.CreatedBy)
 require.Nil(t, def.Editors)

 def.CreatedBy = "u-1"
 def.Editors = []string{"u-1", "u-2"}
 require.Equal(t, "u-1", def.CreatedBy)
 require.Equal(t, []string{"u-1", "u-2"}, def.Editors)
}

func TestErrEditorNotEligibleExists(t *testing.T) {
 // 非白名单成员写白名单时，store 用该哨兵 wrap，errors.Is 可判定。
 require.True(t, errors.Is(ErrEditorNotEligible, ErrEditorNotEligible))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/workflow/domain/ -run 'TestDefinitionCarriesEditorMetadata|TestErrEditorNotEligibleExists' -v`
Expected: 编译失败：`ErrEditorNotEligible undefined`、`def.CreatedBy undefined`。

- [ ] **Step 3: Implement domain + ports**

(1) `internal/workflow/domain/workflow.go` 错误块追加（`ErrForbidden` 之后）：

```go
 ErrForbidden           = errors.New("workflow action forbidden")
 ErrEditorNotEligible   = errors.New("workflow editor not eligible")
```

(2) `Definition` struct 在 `Description` 后、`Revision` 前插入两字段：

```go
type Definition struct {
 ID              string      `json:"id"`
 Name            string      `json:"name"`
 Description     string      `json:"description"`
 CreatedBy       string      `json:"created_by,omitempty"`
 Editors         []string    `json:"editors,omitempty"`
 Revision        int64       `json:"revision"`
 ActiveVersionID string      `json:"active_version_id,omitempty"`
 Spec            Spec        `json:"spec"`
 InputSchema     InputSchema `json:"input_schema"`
 CreatedAt       time.Time   `json:"created_at"`
 UpdatedAt       time.Time   `json:"updated_at"`
}
```

(3) 新建 `internal/workflow/domain/port/tenant_role_resolver.go`：

```go
package port

import "context"

// TenantRoleResolver resolves a tenant member's role (owner/admin/member).
// Single source of truth injected by wiring; resolution failure fails closed
// in the ownership matrix.
type TenantRoleResolver interface {
 ResolveTenantRole(context.Context, string, string) (string, error)
}
```

(4) 新建 `internal/workflow/domain/port/resource_editor.go`：

```go
package port

import (
 "context"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// ResourceEditorRepo manages the shared resource_editors whitelist for a
// workflow resource (kind='workflow'). Whitelist members may edit the
// workflow (Update/Publish); the申请通道 approval grants membership here.
type ResourceEditorRepo interface {
 // ListEditors returns the granted editor ids, oldest grant first.
 ListEditors(context.Context, string, string) ([]string, error)
 // ReplaceEditors atomically swaps the editor set and records the change
 // audit inside the same transaction. createdBy is the acting admin/owner.
 ReplaceEditors(context.Context, string, string, []string, string, *auditdomain.ResourceChangeAuditEvent) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/workflow/domain/ -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/domain/ internal/workflow/domain/port/
git commit -m "feat(workflow): add editor metadata and role/editor ports"
```

---

### Task 3: workflow store 层 —— created_by 列 + editorActor 签名 + resource_editor.go helpers + Delete 级联 + Create 自动授予 creator

**Files:**

- Modify: `internal/workflow/domain/port/repositories.go`（`UpdateDefinition`/`CreateNextVersion` 签名 +editorActor）
- Modify: `internal/workflow/infrastructure/persistence/store.go`（`CreateDefinition`、`GetDefinition`、`UpdateDefinition`、`DeleteDefinition`、`CreateNextVersion`）
- Create: `internal/workflow/infrastructure/persistence/resource_editor.go`
- Modify: `internal/workflow/application/definition_service.go`（Update/Publish 调用点补 `""` 参数占位）
- Modify: `internal/workflow/application/service_test.go`（memoryStore.UpdateDefinition 签名）
- Modify: `internal/workflow/infrastructure/persistence/store_mock_test.go`（3 处 `CreateNextVersion` 调用补 `""`）
- Test: `internal/workflow/infrastructure/persistence/store_mock_test.go`（新增 auto-grant 测试）

**Interfaces:**

- Consumes: Task 2 的 `domain.ErrEditorNotEligible`、`port.ResourceEditorRepo`。
- Produces: `port.DefinitionRepository.UpdateDefinition(ctx, tenantID string, d *domain.Definition, expected int64, editorActor string, ev *auditdomain.ResourceChangeAuditEvent) error`（editorActor 在 expected 后、ev 前）；`port.AtomicVersionPublisher.CreateNextVersion(ctx, tenantID string, definition *domain.Definition, versionID, editorActor string, ev *auditdomain.ResourceChangeAuditEvent) (*domain.Version, error)`；`persistence.PgWorkflowResourceEditorRepo`（`NewPgWorkflowResourceEditorRepo(pool *pgxpool.Pool)`）；包内 helpers `insertEditors/revalidateEditorAccess/revalidateEditorIfActor` 与 `resourceEditorKind = "workflow"`。Task 4 的 service 所有权矩阵依赖这些签名。

- [ ] **Step 1: Write the failing store test**

在 `internal/workflow/infrastructure/persistence/store_mock_test.go` 追加（auto-grant 验证 `CreateDefinition` 同事务写入 creator 为白名单成员）：

```go
func TestPgStore_CreateDefinition_autoGrantsCreatorEditor(t *testing.T) {
 mock := newStoreMock(t)
 store := &PgStore{pool: mock}

 beginTenantTx(mock)
 // INSERT workflow_definitions（含 created_by 列，共 7 参数）。
 mock.ExpectExec("INSERT INTO workflow_definitions \\(id,name,description,created_by,draft_revision,draft_spec_json,draft_input_schema_json\\)").
  WithArgs("d1", "Research", "", "u-1", int64(1), `{"nodes":[]}`, `{"task_label":"任务","fields":[]}`).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 // insertEditors → EditorEligible 检查 creator 为租户成员。
 mock.ExpectQuery("SELECT EXISTS\\(").
  WithArgs("t1", "u-1", resourceaccess.AllowedEditorRoles()).
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 // insertEditors → INSERT resource_editors。
 mock.ExpectExec("INSERT INTO resource_editors \\(resource_kind, resource_id, editor_id, created_by\\)").
  WithArgs("workflow", "d1", "u-1", "u-1").
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 // ev=nil → 无变更审计。
 mock.ExpectCommit()

 err := store.CreateDefinition(context.Background(), "t1", &domain.Definition{
  ID: "d1", Name: "Research", CreatedBy: "u-1", Revision: 1,
  Spec: domain.Spec{}, InputSchema: domain.InputSchema{TaskLabel: "任务"},
 }, nil)
 require.NoError(t, err)
 require.NoError(t, mock.ExpectationsWereMet())
}
```

> `resourceaccess.AllowedEditorRoles()` 需确认导出。若未导出，改为硬编码 `[]string{"admin", "owner", "member"}`（`pkg/resourceaccess/resource_access.go` 第 33 行 `allowedEditorRoles`）。先 `grep -n "allowedEditorRoles\|AllowedEditorRoles" pkg/resourceaccess/resource_access.go` 确认。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/workflow/infrastructure/persistence/ -run TestPgStore_CreateDefinition_autoGrantsCreatorEditor -v`
Expected: FAIL（当前 INSERT 无 created_by 列 → mock 期望不匹配；且编译失败因 port 签名未变）。**先做 Step 3 的 port 签名 + store 实现，再回来跑测试。**

- [ ] **Step 3: Implement port signature + store changes**

(1) `internal/workflow/domain/port/repositories.go`：

```go
 UpdateDefinition(context.Context, string, *domain.Definition, int64, string, *auditdomain.ResourceChangeAuditEvent) error
```

```go
 CreateNextVersion(context.Context, string, *domain.Definition, string, string, *auditdomain.ResourceChangeAuditEvent) (*domain.Version, error)
```

(2) `internal/workflow/infrastructure/persistence/store.go` `CreateDefinition` 替换为（INSERT 加 created_by，并在同事务内自动授予 creator）：

```go
func (s *PgStore) CreateDefinition(ctx context.Context, tenantID string, d *domain.Definition, ev *auditdomain.ResourceChangeAuditEvent) error {
 spec, err := json.Marshal(d.Spec)
 if err != nil {
  return err
 }
 inputSchema, err := json.Marshal(d.InputSchema)
 if err != nil {
  return err
 }
 return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  if _, err := tx.Exec(ctx,
   `INSERT INTO workflow_definitions (id,name,description,created_by,draft_revision,draft_spec_json,draft_input_schema_json)
    VALUES ($1,$2,$3,$4,$5,$6,$7)`,
   d.ID, d.Name, d.Description, d.CreatedBy, d.Revision, string(spec), string(inputSchema),
  ); err != nil {
   return fmt.Errorf("create workflow definition: %w", err)
  }
  // 创建即把创建者授予为白名单成员：member 创建的工作流由创建者自持编辑权。
  if d.CreatedBy != "" {
   if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, d.ID, []string{d.CreatedBy}, d.CreatedBy); err != nil {
    return err
   }
  }
  return insertChangeAudit(ctx, tx, ev)
 })
}
```

(3) `store.go` `GetDefinition` SELECT 加 `created_by`：

```go
func (s *PgStore) GetDefinition(ctx context.Context, tenantID, id string) (*domain.Definition, error) {
 d := &domain.Definition{}
 err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  var spec, inputSchema []byte
  if err := tx.QueryRow(ctx,
   `SELECT id,name,description,created_by,draft_revision,COALESCE(active_version_id,''),draft_spec_json,draft_input_schema_json,created_at,updated_at
    FROM workflow_definitions WHERE id=$1`, id,
  ).Scan(&d.ID, &d.Name, &d.Description, &d.CreatedBy, &d.Revision, &d.ActiveVersionID, &spec, &inputSchema, &d.CreatedAt, &d.UpdatedAt); err != nil {
   if errors.Is(err, pgx.ErrNoRows) {
    return domain.ErrNotFound
   }
   return err
  }
  if err := json.Unmarshal(spec, &d.Spec); err != nil {
   return err
  }
  if err := json.Unmarshal(inputSchema, &d.InputSchema); err != nil {
   return err
  }
  return nil
 })
 if err != nil {
  return nil, err
 }
 return d, nil
}
```

(4) `store.go` `UpdateDefinition` 加 `editorActor` 参数 + 写事务内复查：

```go
func (s *PgStore) UpdateDefinition(ctx context.Context, tenantID string, d *domain.Definition, expected int64, editorActor string, ev *auditdomain.ResourceChangeAuditEvent) error {
 spec, err := json.Marshal(d.Spec)
 if err != nil {
  return err
 }
 inputSchema, err := json.Marshal(d.InputSchema)
 if err != nil {
  return err
 }
 return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  // 白名单成员编辑：事务内复查白名单仍在（TOCTOU 关闭）。
  if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, d.ID, editorActor); err != nil {
   return err
  }
  tag, err := tx.Exec(ctx,
   `UPDATE workflow_definitions
    SET name=$1, description=$2, draft_revision=$3, draft_spec_json=$4, draft_input_schema_json=$5, updated_at=NOW()
    WHERE id=$6 AND draft_revision=$7`,
   d.Name, d.Description, d.Revision, string(spec), string(inputSchema), d.ID, expected,
  )
  if err != nil {
   return err
  }
  if tag.RowsAffected() == 0 {
   return domain.ErrRevisionConflict
  }
  return insertChangeAudit(ctx, tx, ev)
 })
}
```

(5) `store.go` `DeleteDefinition` 同一事务内先删 editors：

```go
func (s *PgStore) DeleteDefinition(ctx context.Context, tenantID, id string, ev *auditdomain.ResourceChangeAuditEvent) error {
 return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  if _, err := tx.Exec(ctx,
   `DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
   resourceEditorKind, id,
  ); err != nil {
   return fmt.Errorf("delete workflow editors: %w", err)
  }
  tag, err := tx.Exec(ctx, `DELETE FROM workflow_definitions WHERE id=$1`, id)
  if err != nil {
   return err
  }
  if tag.RowsAffected() == 0 {
   return domain.ErrNotFound
  }
  return insertChangeAudit(ctx, tx, ev)
 })
}
```

(6) `store.go` `CreateNextVersion` 加 `editorActor` 参数 + FOR UPDATE 后复查：

```go
func (s *PgStore) CreateNextVersion(ctx context.Context, tenantID string, definition *domain.Definition, versionID, editorActor string, ev *auditdomain.ResourceChangeAuditEvent) (*domain.Version, error) {
 if err := domain.ValidateSpec(definition.Spec); err != nil {
  return nil, err
 }
 if err := domain.ValidateInputSchema(definition.InputSchema); err != nil {
  return nil, err
 }
 spec, err := json.Marshal(definition.Spec)
 if err != nil {
  return nil, err
 }
 inputSchema, err := json.Marshal(definition.InputSchema)
 if err != nil {
  return nil, err
 }
 version := &domain.Version{
  ID: versionID, DefinitionID: definition.ID, Number: 0, Name: definition.Name,
  Description: definition.Description, Spec: definition.Spec, InputSchema: definition.InputSchema,
 }
 err = s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  var dummy string
  err := tx.QueryRow(ctx, `SELECT id FROM workflow_definitions WHERE id=$1 FOR UPDATE`, definition.ID).Scan(&dummy)
  if errors.Is(err, pgx.ErrNoRows) {
   return domain.ErrNotFound
  }
  if err != nil {
   return err
  }
  // 白名单成员发布：行锁后复查白名单仍在（TOCTOU 关闭）。
  if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, definition.ID, editorActor); err != nil {
   return err
  }
  var next int64
  if err := tx.QueryRow(ctx,
   `SELECT COALESCE(MAX(version_no),0)+1 FROM workflow_versions WHERE definition_id=$1`,
   definition.ID,
  ).Scan(&next); err != nil {
   return err
  }
  version.Number = next
  if _, err := tx.Exec(ctx,
   `INSERT INTO workflow_versions (id,definition_id,version_no,name,description,spec_json,input_schema_json)
    VALUES ($1,$2,$3,$4,$5,$6,$7)`,
   version.ID, version.DefinitionID, version.Number, version.Name, version.Description, string(spec), string(inputSchema),
  ); err != nil {
   return err
  }
  if _, err := tx.Exec(ctx,
   `UPDATE workflow_definitions SET active_version_id=$1 WHERE id=$2`,
   version.ID, definition.ID,
  ); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, ev)
 })
 if err != nil {
  return nil, err
 }
 return version, nil
}
```

> 若现有 `CreateNextVersion` 的 scan/insert 列顺序与本段不同，以文件内现状为准，仅追加 `editorActor` 参数 + `revalidateEditorIfActor` 调用，其余逻辑不动。

(7) 新建 `internal/workflow/infrastructure/persistence/resource_editor.go`：

```go
package persistence

import (
 "context"
 "fmt"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/workflow/domain"
 "github.com/byteBuilderX/stratum/internal/workflow/domain/port"
 "github.com/byteBuilderX/stratum/pkg/resourceaccess"
 pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/byteBuilderX/stratum/pkg/tenantdb"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
)

// resourceEditorKind identifies workflow rows in the shared resource_editors table.
const resourceEditorKind = "workflow"

func insertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string) error {
 return resourceaccess.InsertEditors(ctx, tx, tenantID, kind, resourceID, editorIDs, createdBy, domain.ErrEditorNotEligible)
}

func revalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string) error {
 return resourceaccess.RevalidateEditorAccess(ctx, tx, tenantID, kind, resourceID, actorID, domain.ErrForbidden)
}

// revalidateEditorIfActor re-checks the whitelist inside the write transaction
// when editorActor is non-empty (a whitelist member performing the write).
// owner/admin 路径 actor 为空串，跳过复查；缺租户上下文时 fail-closed。
func revalidateEditorIfActor(ctx context.Context, tx pgx.Tx, kind, resourceID, actorID string) error {
 if actorID == "" {
  return nil
 }
 tc, ok := tenantdb.FromContext(ctx)
 if !ok || tc.TenantID == "" {
  return fmt.Errorf("workflow store: missing tenant context")
 }
 return revalidateEditorAccess(ctx, tx, tc.TenantID, kind, resourceID, actorID)
}

// PgWorkflowResourceEditorRepo manages the shared resource_editors table for
// workflow resources. Methods run through the tenant boundary encapsulation.
type PgWorkflowResourceEditorRepo struct {
 pool poolIface
}

var _ port.ResourceEditorRepo = (*PgWorkflowResourceEditorRepo)(nil)

func NewPgWorkflowResourceEditorRepo(pool *pgxpool.Pool) *PgWorkflowResourceEditorRepo {
 return &PgWorkflowResourceEditorRepo{pool: pool}
}

func (r *PgWorkflowResourceEditorRepo) ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error) {
 out := make([]string, 0)
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx,
   `SELECT editor_id FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2 ORDER BY created_at`,
   resourceEditorKind, resourceID,
  )
  if err != nil {
   return fmt.Errorf("list workflow editors: %w", err)
  }
  defer rows.Close()
  for rows.Next() {
   var id string
   if err := rows.Scan(&id); err != nil {
    return fmt.Errorf("scan workflow editor: %w", err)
   }
   out = append(out, id)
  }
  return rows.Err()
 })
 if err != nil {
  return nil, err
 }
 if out == nil {
  return []string{}, nil
 }
 return out, nil
}

func (r *PgWorkflowResourceEditorRepo) ReplaceEditors(ctx context.Context, tenantID, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
 return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  if _, err := tx.Exec(ctx,
   `DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
   resourceEditorKind, resourceID,
  ); err != nil {
   return fmt.Errorf("clear workflow editors: %w", err)
  }
  if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, resourceID, editorIDs, createdBy); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, audit)
 })
}

func (r *PgWorkflowResourceEditorRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
 tc, ok := tenantdb.FromContext(ctx)
 if ok && tc.TenantID != tenantID {
  return fmt.Errorf("workflow editor repo: tenant context mismatch")
 }
 if !ok {
  ctx = pgstore.WithTenant(ctx, &pgstore.TenantContext{TenantID: tenantID})
 }
 return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}
```

> 需确认 `pkg/tenantdb.FromContext` 与 `pgstore.WithTenant` 的精确签名/返回类型与 skill 版 `PgSkillResourceEditorRepo` 一致（`internal/skill/infrastructure/persistence/resource_editor_repo.go` 是模板）。若 `tenantdb.FromContext` 返回 `(*TenantContext, bool)`，去掉多余类型断言。

(8) `internal/workflow/application/definition_service.go` 调用点补参数（本任务仅占位空串，Task 4 替换为真实 editorActor）：

```go
 if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision, "", ev); err != nil {
```

```go
  version, err := publisher.CreateNextVersion(ctx, tenantID, definition, s.newID(), "", ev)
```

(9) `internal/workflow/application/service_test.go` memoryStore 签名：

```go
func (s *memoryStore) UpdateDefinition(_ context.Context, _ string, definition *domain.Definition, expected int64, _ string, ev *auditdomain.ResourceChangeAuditEvent) error {
```

(10) `internal/workflow/infrastructure/persistence/store_mock_test.go` 3 处 `CreateNextVersion` 调用（376/388/403 行）第 5 参数补 `""`：

```go
 created, err := store.CreateNextVersion(context.Background(), "t1", definition, "v-new", "", nil)
```

```go
 _, err := store.CreateNextVersion(context.Background(), "t1", &domain.Definition{}, "v-new", "", nil)
```

```go
 _, err := store.CreateNextVersion(context.Background(), "t1", &domain.Definition{
  ID:          "nope",
  Spec:        domain.Spec{Nodes: []domain.Node{{ID: "n1", Type: domain.NodeTypeApproval}}},
  InputSchema: domain.InputSchema{TaskLabel: "task"},
 }, "v-new", "", nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workflow/... ./pkg/resourceaccess/ -v`
Expected: 全部 PASS（含新增 auto-grant 测试）。

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/domain/port/ internal/workflow/infrastructure/persistence/ internal/workflow/application/definition_service.go internal/workflow/application/service_test.go
git commit -m "feat(workflow): store created_by, editor actor revalidation and auto-grant creator"
```

---

### Task 4: DefinitionService 所有权矩阵 + SetEditors/ListEditors + 现有测试注入 stub role resolver + 矩阵测试

**Files:**

- Create: `internal/workflow/application/ownership.go`
- Modify: `internal/workflow/application/definition_service.go`（字段、setters、Create/Update/Delete/Publish/Get/ListDefinitions 改造、SetEditors/ListEditors）
- Modify: `internal/workflow/application/change_audit.go`（`workflowSafeProjectionWithEditors`）
- Modify: `internal/workflow/application/service_test.go`（`stubTenantRole` + `newOwnerDefinitionService` helper + 构造点替换）
- Modify: `internal/workflow/application/workflow_extra_test.go`（构造点替换）
- Modify: `internal/workflow/application/service_integration_test.go`（4 处构造补 resolver）
- Create: `internal/workflow/application/ownership_test.go`（矩阵表驱动）
- Create: `internal/workflow/application/editor_repo_test.go`（SetEditors/ListEditors + 内存 editor repo）

**Interfaces:**

- Consumes: Task 2 的 `port.TenantRoleResolver`/`port.ResourceEditorRepo`；Task 3 的新 port 签名。
- Produces: `DefinitionService.SetTenantRoleResolver(port.TenantRoleResolver)`、`SetEditorRepo(port.ResourceEditorRepo)`、`SetEditors(ctx, tenantID, workflowID string, editorIDs []string, actorID string) error`、`ListEditors(ctx, tenantID, workflowID string) ([]string, error)`；包内 `enforceOwnership(role, actorID, createdBy string, editors []string, op OwnershipOp) error`、`(s *DefinitionService) checkOwnership(...)`、`resolveUpdateActor(...)`。Task 5 的 handler、Task 7 的 wiring 依赖 `SetTenantRoleResolver`/`SetEditorRepo`/`SetEditors`。

- [ ] **Step 1: Write the failing matrix tests**

新建 `internal/workflow/application/ownership_test.go`（表驱动矩阵）：

```go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/workflow/application"
 "github.com/byteBuilderX/stratum/internal/workflow/domain"
 "github.com/stretchr/testify/require"
)

// TestWorkflowOwnershipMatrix pins the fail-closed matrix for Update/Publish/Delete.
func TestWorkflowOwnershipMatrix(t *testing.T) {
 ctx := context.Background()
 newDef := func(createdBy string) *domain.Definition {
  return &domain.Definition{ID: "d1", Name: "Research", CreatedBy: createdBy,
   Revision: 1, Spec: domain.Spec{}, InputSchema: domain.InputSchema{TaskLabel: "任务"}}
 }

 cases := []struct {
  name      string
  role      string
  actor     string
  createdBy string
  editors   []string
  op        application.OwnershipOp
  wantErr   error
 }{
  {"owner update", "owner", "owner-1", "u-2", nil, application.OpEdit, nil},
  {"owner delete", "owner", "owner-1", "u-2", nil, application.OpDelete, nil},
  {"admin creator update", "admin", "u-1", "u-1", nil, application.OpEdit, nil},
  {"admin creator delete", "admin", "u-1", "u-1", nil, application.OpDelete, nil},
  {"admin non-creator update", "admin", "u-9", "u-1", nil, application.OpEdit, nil},
  {"admin non-creator delete forbidden", "admin", "u-9", "u-1", nil, application.OpDelete, domain.ErrForbidden},
  {"whitelist member update", "member", "m-1", "u-1", []string{"m-1"}, application.OpEdit, nil},
  {"whitelist member delete forbidden", "member", "m-1", "u-1", []string{"m-1"}, application.OpDelete, domain.ErrForbidden},
  {"other member update forbidden", "member", "m-2", "u-1", []string{"m-1"}, application.OpEdit, domain.ErrForbidden},
  {"other member delete forbidden", "member", "m-2", "u-1", []string{"m-1"}, application.OpDelete, domain.ErrForbidden},
  {"unknown role forbidden", "guest", "g-1", "u-1", nil, application.OpEdit, domain.ErrForbidden},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := enforceOwnershipForTest(tc.role, tc.actor, tc.createdBy, tc.editors, tc.op)
   require.ErrorIs(t, err, tc.wantErr)
  })
 }
}

func TestWorkflowServiceUpdateRequiresOwnership(t *testing.T) {
 store, idgen := newMemoryStore(), &ids{}
 svc := application.NewDefinitionService(store, store, idgen.NewID)
 svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
 svc.SetEditorRepo(newMemoryEditorRepo(map[string][]string{"d1": {"u-1"}}))
 def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
 require.NoError(t, err)

 // 非白名单成员更新 → 403；admin 角色则放行。
 _, err = svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{Name: "X", ExpectedRevision: def.Revision}, "m-9")
 require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestWorkflowServiceCreateWritesCreator(t *testing.T) {
 store, idgen := newMemoryStore(), &ids{}
 svc := newOwnerDefinitionService(store, idgen)
 def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
 require.NoError(t, err)
 require.Equal(t, "u-1", def.CreatedBy)
}
```

> `application.OwnershipOp` 与 `enforceOwnership` 在 application 包内（不导出也能被 `application_test` 用？不能——内部测试包用 `package application`）。矩阵测试必须放进内部包。**修正**：`ownership_test.go` 使用 `package application`（内部测试，与 `authorization_test.go` 一致），直接调用 `enforceOwnership`。见 Step 3 中调整后的测试代码。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workflow/application/ -run 'TestWorkflowOwnershipMatrix|TestWorkflowServiceUpdateRequiresOwnership|TestWorkflowServiceCreateWritesCreator' -v`
Expected: 编译失败（`enforceOwnership`、`stubTenantRole`、`newMemoryEditorRepo`、`newOwnerDefinitionService` undefined）。

- [ ] **Step 3: Implement ownership + service changes**

(1) 新建 `internal/workflow/application/ownership.go`（镜像 `internal/skill/application/ownership.go`，显式 tenantID 版）：

```go
package application

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/workflow/domain"
 "github.com/byteBuilderX/stratum/pkg/reqctx"
)

// OwnershipOp 区分写操作的破坏性等级：白名单成员与 admin 的 Delete 语义不同。
type OwnershipOp int

const (
 OpEdit OwnershipOp = iota
 OpAccess
 OpDelete
)

// isGrantedEditor reports whether actorID appears in the granted editor set.
func isGrantedEditor(actorID string, editors []string) bool {
 for _, id := range editors {
  if id == actorID {
   return true
  }
 }
 return false
}

// enforceOwnership applies the workflow ownership matrix. Fail closed on
// unknown role or empty actor. Delete stays with owner / creator-admin.
// 见 spec 矩阵：owner 全放行；admin 除 Delete 需 createdBy==actorID 外放行；
// member 仅白名单成员且非 Delete；其余一律 403。
func enforceOwnership(role, actorID, createdBy string, editors []string, op OwnershipOp) error {
 if actorID == "" {
  return domain.ErrForbidden
 }
 switch role {
 case "owner":
  return nil
 case "admin":
  if op == OpDelete && createdBy != actorID {
   return domain.ErrForbidden
  }
  return nil
 case "member":
  if op == OpDelete {
   return domain.ErrForbidden
  }
  if isGrantedEditor(actorID, editors) {
   return nil
  }
  return domain.ErrForbidden
 default:
  return domain.ErrForbidden
 }
}

// checkOwnership resolves the actor's tenant role and applies the matrix.
// Workflow ports carry tenantID explicitly (service boundary has no tenant
// context key), hence the parameter. SystemActorFromContext bypasses checks
// (worker/scheduler path). resolver nil, resolution failure and empty actor
// all fail closed.
func (s *DefinitionService) checkOwnership(ctx context.Context, tenantID, actorID, createdBy string, editors []string, op OwnershipOp) error {
 if reqctx.SystemActorFromContext(ctx) != "" {
  return nil
 }
 if actorID == "" || s.roles == nil {
  return domain.ErrForbidden
 }
 role, err := s.roles.ResolveTenantRole(ctx, tenantID, actorID)
 if err != nil {
  return domain.ErrForbidden
 }
 return enforceOwnership(role, actorID, createdBy, editors, op)
}

// resolveUpdateActor applies the ownership matrix on the Update/Publish path.
// owner/creator-admin pass with empty editorActor（写事务不复查白名单）；
// 白名单成员 pass with editorActor set，store 在写事务内复查关闭 TOCTOU。
// 缺 editorRepo / 白名单查询失败 / 未授权一律 fail closed。
func (s *DefinitionService) resolveUpdateActor(ctx context.Context, tenantID, actorID string, current *domain.Definition) (string, error) {
 if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, nil, OpEdit); err == nil {
  return "", nil
 }
 if s.editors == nil {
  return "", domain.ErrForbidden
 }
 editors, err := s.editors.ListEditors(ctx, tenantID, current.ID)
 if err != nil {
  return "", err
 }
 if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, editors, OpEdit); err != nil {
  return "", err
 }
 return actorID, nil
}
```

(2) `internal/workflow/application/definition_service.go` 结构体字段追加（`logger` 之后）：

```go
type DefinitionService struct {
 definitions  port.DefinitionRepository
 versions     port.VersionRepository
 newID        func() string
 failureAudit auditport.FailureAuditRecorder
 bindings     port.SkillBindingResolver
 roles        port.TenantRoleResolver
 editors      port.ResourceEditorRepo
 logger       *zap.Logger
}
```

setters（`SetLogger` 之后追加）：

```go
// SetTenantRoleResolver 注入租户角色解析器（单事实源），所有权矩阵据此判定。
// 未注入时 fail closed（禁止一切 Update/Delete/Publish）。
func (s *DefinitionService) SetTenantRoleResolver(r port.TenantRoleResolver) {
 s.roles = r
}

// SetEditorRepo 注入可编辑人白名单仓库。未注入时白名单成员路径 fail closed。
func (s *DefinitionService) SetEditorRepo(r port.ResourceEditorRepo) {
 s.editors = r
}
```

(3) `Create`：`NewDefinition` 成功后写入 `definition.CreatedBy = actorID`：

```go
 definition, err := domain.NewDefinition(s.newID(), cmd.Name, cmd.Description, cmd.Spec, normalizeInputSchema(cmd.InputSchema))
 if err != nil {
  return nil, err
 }
 definition.CreatedBy = actorID
```

(4) `Update`：GetDefinition 后先 `resolveUpdateActor`，再传入 store：

```go
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 editorActor, err := s.resolveUpdateActor(ctx, tenantID, actorID, definition)
 if err != nil {
  return nil, err
 }
 before := workflowSafeProjection(definition)
```

```go
 if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision, editorActor, ev); err != nil {
```

(5) `Delete`：GetDefinition 后加 `checkOwnership(OpDelete)`：

```go
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return err
 }
 if err := s.checkOwnership(ctx, tenantID, actorID, definition.CreatedBy, nil, OpDelete); err != nil {
  return err
 }
```

(6) `Publish`：分支前 `resolveUpdateActor`，原子路径传入 editorActor：

```go
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 editorActor, err := s.resolveUpdateActor(ctx, tenantID, actorID, definition)
 if err != nil {
  return nil, err
 }
```

```go
 if publisher, ok := s.versions.(port.AtomicVersionPublisher); ok {
  version, err := publisher.CreateNextVersion(ctx, tenantID, definition, s.newID(), editorActor, ev)
  ...
 }
```

> fallback 非原子路径（测试 mock）不传 editorActor——owner/admin 下为空串不受影响；白名单成员发布仅原子实现支持（生产真实路径）。

(7) `Get`：附加 editors（editorRepo 为 nil 时保持现状）：

```go
func (s *DefinitionService) Get(ctx context.Context, tenantID, id string) (*domain.Definition, error) {
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 if s.editors != nil {
  editors, listErr := s.editors.ListEditors(ctx, tenantID, definition.ID)
  if listErr != nil {
   return nil, listErr
  }
  definition.Editors = editors
 }
 return definition, nil
}
```

> 若 `Get` 现为内联于别处（如 `service.go`），按文件现状定位修改。`ListDefinitions` 不动（catalog 摘要不附带 editors）。

(8) 新增 `SetEditors`/`ListEditors`（追加在 `Get` 后）：

```go
// ListEditors returns the granted editor ids for a workflow. editorRepo 未装配
// 时返回空列表（只读路径不 fail-closed）。
func (s *DefinitionService) ListEditors(ctx context.Context, tenantID, workflowID string) ([]string, error) {
 if s.editors == nil {
  return []string{}, nil
 }
 return s.editors.ListEditors(ctx, tenantID, workflowID)
}

// SetEditors replaces the whitelist. 仅 owner/admin（OpAccess）可管理；
// member 一律拒绝。变更写入同事务并记录审计。
func (s *DefinitionService) SetEditors(ctx context.Context, tenantID, workflowID string, editorIDs []string, actorID string) error {
 if s.editors == nil {
  return fmt.Errorf("workflow set editors: editor repo not wired")
 }
 current, err := s.definitions.GetDefinition(ctx, tenantID, workflowID)
 if err != nil {
  return err
 }
 if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, nil, OpAccess); err != nil {
  return err
 }
 before, err := s.editors.ListEditors(ctx, tenantID, current.ID)
 if err != nil {
  return fmt.Errorf("workflow set editors: list: %w", err)
 }
 audit, err := newWorkflowChangeAudit(current.ID, auditdomain.ChangeOpUpdate, actorID,
  workflowSafeProjectionWithEditors(current, before), workflowSafeProjectionWithEditors(current, editorIDs))
 if err != nil {
  return err
 }
 if err := s.editors.ReplaceEditors(ctx, tenantID, current.ID, editorIDs, actorID, audit); err != nil {
  return err
 }
 s.logger.Info("workflow editors updated",
  zap.String("workflow", workflowID), zap.Int("editors", len(editorIDs)))
 return nil
}
```

> 需确认 `s.logger` 非 nil（`SetLogger` 未调用时可能 nil panic）。镜像 skill/workspace：若 service 有 `loggerFor(s)` 兜底或默认 `zap.NewNop()`，保持一致；否则此处日志改为条件 `if s.logger != nil`。

(9) `internal/workflow/application/change_audit.go` 追加：

```go
func workflowSafeProjectionWithEditors(d *domain.Definition, editors []string) map[string]any {
 p := workflowSafeProjection(d)
 p["editors"] = editors
 return p
}
```

(10) `internal/workflow/application/service_test.go` 追加 helper（`ids` 类型附近）：

```go
// stubTenantRole 固定角色解析器：非授权用例注入 owner 放行所有权矩阵。
type stubTenantRole struct{ role string }

func (s stubTenantRole) ResolveTenantRole(context.Context, string, string) (string, error) {
 return s.role, nil
}

// newOwnerDefinitionService 构造注入 owner 角色的服务，用于非授权相关测试。
func newOwnerDefinitionService(store *memoryStore, idgen *ids) *application.DefinitionService {
 svc := application.NewDefinitionService(store, store, idgen.NewID)
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 return svc
}
```

并将 `service_test.go` / `workflow_extra_test.go` 内所有 `application.NewDefinitionService(store, store, idgen.NewID)` 构造点替换为 `newOwnerDefinitionService(store, idgen)`（机械替换，覆盖 Update/Publish/Delete 全部现有用例）。

(11) `internal/workflow/application/service_integration_test.go`（`//go:build integration`）：4 处 `definitions := application.NewDefinitionService(store, store, uuid.NewString)` 之后各加一行：

```go
 definitions.SetTenantRoleResolver(stubTenantRole{role: "owner"})
```

(12) 新建 `internal/workflow/application/editor_repo_test.go`：

```go
package application_test

import (
 "context"
 "testing"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/workflow/application"
 "github.com/stretchr/testify/require"
)

// memoryEditorRepo 内存实现 ResourceEditorRepo，供 service 测试注入。
type memoryEditorRepo struct {
 editors map[string][]string
}

func newMemoryEditorRepo(initial map[string][]string) *memoryEditorRepo {
 if initial == nil {
  initial = map[string][]string{}
 }
 return &memoryEditorRepo{editors: initial}
}

func (r *memoryEditorRepo) ListEditors(_ context.Context, _, resourceID string) ([]string, error) {
 return append([]string(nil), r.editors[resourceID]...), nil
}

func (r *memoryEditorRepo) ReplaceEditors(_ context.Context, _, resourceID string, editorIDs []string, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
 r.editors[resourceID] = append([]string(nil), editorIDs...)
 return nil
}

func TestWorkflowServiceSetAndListEditors(t *testing.T) {
 ctx := context.Background()
 store, idgen := newMemoryStore(), &ids{}
 editorRepo := newMemoryEditorRepo(nil)
 svc := application.NewDefinitionService(store, store, idgen.NewID)
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 svc.SetEditorRepo(editorRepo)

 def, err := svc.Create(ctx, "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()}, "u-1")
 require.NoError(t, err)

 // owner 可设白名单。
 require.NoError(t, svc.SetEditors(ctx, "t1", def.ID, []string{"m-1", "m-2"}, "owner-1"))
 editors, err := svc.ListEditors(ctx, "t1", def.ID)
 require.NoError(t, err)
 require.ElementsMatch(t, []string{"m-1", "m-2"}, editors)

 // Get 响应附带 editors。
 got, err := svc.Get(ctx, "t1", def.ID)
 require.NoError(t, err)
 require.ElementsMatch(t, []string{"m-1", "m-2"}, got.Editors)

 // member 管理白名单 → 403。
 svc.SetTenantRoleResolver(stubTenantRole{role: "member"})
 err = svc.SetEditors(ctx, "t1", def.ID, []string{"m-3"}, "m-1")
 require.ErrorIs(t, err, domain.ErrForbidden)
}
```

> `ownership_test.go` 用内部包（`package application`）直接测 `enforceOwnership`，测试代码：

```go
package application

import (
 "testing"

 "github.com/byteBuilderX/stratum/internal/workflow/domain"
 "github.com/stretchr/testify/require"
)

func TestEnforceWorkflowOwnershipMatrix(t *testing.T) {
 cases := []struct {
  name      string
  role      string
  actor     string
  createdBy string
  editors   []string
  op        OwnershipOp
  wantErr   error
 }{
  {"owner update", "owner", "owner-1", "u-2", nil, OpEdit, nil},
  {"owner delete", "owner", "owner-1", "u-2", nil, OpDelete, nil},
  {"admin creator update", "admin", "u-1", "u-1", nil, OpEdit, nil},
  {"admin creator delete", "admin", "u-1", "u-1", nil, OpDelete, nil},
  {"admin non-creator update", "admin", "u-9", "u-1", nil, OpEdit, nil},
  {"admin non-creator delete forbidden", "admin", "u-9", "u-1", nil, OpDelete, domain.ErrForbidden},
  {"whitelist member update", "member", "m-1", "u-1", []string{"m-1"}, OpEdit, nil},
  {"whitelist member delete forbidden", "member", "m-1", "u-1", []string{"m-1"}, OpDelete, domain.ErrForbidden},
  {"other member update forbidden", "member", "m-2", "u-1", []string{"m-1"}, OpEdit, domain.ErrForbidden},
  {"other member delete forbidden", "member", "m-2", "u-1", []string{"m-1"}, OpDelete, domain.ErrForbidden},
  {"unknown role forbidden", "guest", "g-1", "u-1", nil, OpEdit, domain.ErrForbidden},
  {"empty actor forbidden", "owner", "", "u-1", nil, OpEdit, domain.ErrForbidden},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := enforceOwnership(tc.role, tc.actor, tc.createdBy, tc.editors, tc.op)
   require.ErrorIs(t, err, tc.wantErr)
  })
 }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workflow/... -v`
Expected: 全部 PASS（含矩阵、SetEditors、Create 写入 creator；现有 Update/Publish/Delete 用例经 owner stub 放行不回归）。

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/application/
git commit -m "feat(workflow): ownership matrix, set/list editors and creator write-through"
```

---

### Task 5: workflow handler —— SetWorkflowEditors + 接口扩展

**Files:**

- Modify: `api/http/handler/workflow_handler.go`（`workflowDefinitionService` interface + `SetWorkflowEditors` handler）

**Interfaces:**

- Consumes: Task 4 的 `DefinitionService.SetEditors(ctx, tenantID, workflowID string, editorIDs []string, actorID string) error`。
- Produces: 接口方法 `SetEditors(context.Context, string, string, []string, string) error`；`WorkflowHandler.SetWorkflowEditors(c *gin.Context)`。Task 6 路由挂载该方法。

- [ ] **Step 1: Write the failing handler test**

在 `api/http/handler/workflow_handler_test.go` 追加。**先改 fake + 加测试，不实现 handler**：

(1) `workflowDefinitionFake` 结构体（56 行）加 `setEditors` 录制字段：

```go
type workflowDefinitionFake struct {
 created    *workflowdomain.Definition
 deletedID  string
 setEditors struct {
  workflowID string
  editorIDs  []string
  actorID    string
  calls      int
 }
}
```

> 现有 `&workflowDefinitionFake{}` / `&workflowDefinitionFake{created: ...}` 字面量不受影响（命名字段）。

(2) 追加 `SetEditors` 方法（实现扩展后接口所需的第 9 个方法）：

```go
func (f *workflowDefinitionFake) SetEditors(_ context.Context, _, workflowID string, editorIDs []string, actorID string) error {
 f.setEditors.calls++
 f.setEditors.workflowID = workflowID
 f.setEditors.editorIDs = editorIDs
 f.setEditors.actorID = actorID
 return nil
}
```

(3) 文件末尾追加两个测试函数：

```go
func TestWorkflowHandlerSetEditors(t *testing.T) {
 gin.SetMode(gin.TestMode)
 definitions := &workflowDefinitionFake{created: &workflowdomain.Definition{ID: "wf-1", Name: "Research"}}
 h := handler.NewWorkflowHandler(definitions, &workflowRunFake{})
 r := gin.New()
 r.Use(func(c *gin.Context) {
  c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
  c.Set(middleware.ContextKeySub, "admin-1")
  c.Set(middleware.ContextKeyRole, "admin")
  c.Next()
 })
 r.PUT("/workflows/:id/editors", h.SetWorkflowEditors)

 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPut, "/workflows/wf-1/editors", strings.NewReader(`{"editorIds":["m-1","m-2"]}`))
 req.Header.Set("Content-Type", "application/json")
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusOK, w.Code)
 require.Equal(t, 1, definitions.setEditors.calls)
 require.Equal(t, "wf-1", definitions.setEditors.workflowID)
 require.Equal(t, []string{"m-1", "m-2"}, definitions.setEditors.editorIDs)
 require.Equal(t, "admin-1", definitions.setEditors.actorID)
}

func TestWorkflowHandlerSetEditorsRejectsMissingEditorIDs(t *testing.T) {
 gin.SetMode(gin.TestMode)
 definitions := &workflowDefinitionFake{}
 h := handler.NewWorkflowHandler(definitions, &workflowRunFake{})
 r := gin.New()
 r.Use(middleware.ErrorHandler(zap.NewNop()))
 r.Use(func(c *gin.Context) {
  c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
  c.Set(middleware.ContextKeySub, "admin-1")
  c.Set(middleware.ContextKeyRole, "admin")
  c.Next()
 })
 r.PUT("/workflows/:id/editors", h.SetWorkflowEditors)

 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPut, "/workflows/wf-1/editors", strings.NewReader(`{}`))
 req.Header.Set("Content-Type", "application/json")
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusBadRequest, w.Code)
 require.Zero(t, definitions.setEditors.calls)
}
```

> 断言三态：200 成功 / 400 缺 editorIds / error 态（`SetEditors` 返回 error 时经 `c.Error` 交给 `middleware.ErrorHandler`，由错误映射中间件转 403/500）。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./api/http/handler/ -run 'TestWorkflowHandlerSetEditors' -v`
Expected: 编译失败（`h.SetWorkflowEditors undefined`，handler 未实现）。

- [ ] **Step 3: Implement handler**

(1) `workflowDefinitionService` interface 追加方法：

```go
 SetEditors(context.Context, string, string, []string, string) error
```

(2) 追加 handler（镜像 `skill_handler.go:SetSkillEditors`）：

```go
// SetWorkflowEditors PUT /workflows/:id/editors —— admin/owner 管理白名单。
func (h *WorkflowHandler) SetWorkflowEditors(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req struct {
  EditorIDs []string `json:"editorIds" binding:"required"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actorID, ok := userIDFromCtx(c)
 if !ok {
  respondMissingUser(c)
  return
 }
 if err := h.definitions.SetEditors(c.Request.Context(), tenantID, c.Param("id"), req.EditorIDs, actorID); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"message": "editors updated"})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./api/http/handler/ -run TestWorkflowHandler_SetWorkflowEditors -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/workflow_handler.go api/http/handler/workflow_handler_test.go
git commit -m "feat(workflow): handler endpoint to manage editor whitelist"
```

---

### Task 6: router —— workflow 编辑路由 member、PUT /:id/editors、grant 组 +3、mcp config GET→write

**Files:**

- Modify: `api/http/router.go`（registerWorkflows、grant 组）
- Modify: `api/http/handler/mcp_handler.go`（RegisterRoutes 218 行 config GET）

**Interfaces:**

- Consumes: Task 5 的 `SetWorkflowEditors`；`RequestEditorAccess`（现有）。
- Produces: 路由集合（见下）。Task 7 wiring、前端 Task 10-14 依赖这些路由。

- [ ] **Step 1: Implement router changes**

(1) `api/http/router.go` `registerWorkflows` 内定义组路由段替换：

```go
 definitions.GET("", h.ListDefinitions)
 definitions.GET("/:id", h.GetDefinition)
 definitions.GET("/:id/versions", h.ListVersions)
 definitions.GET("/:id/versions/:versionID", h.GetVersion)
 // 编辑放开给 member，鉴权下沉 application 层所有权矩阵（前端只读隐藏、后端 fail-closed）。
 definitions.POST("", member...)
 definitions.POST("", requireActive, h.CreateDefinition)
 definitions.PUT("/:id/draft", member...)
 definitions.PUT("/:id/draft", requireActive, h.UpdateDefinition)
 definitions.POST("/:id/publish", member...)
 definitions.POST("/:id/publish", requireActive, h.PublishDefinition)
 // DELETE/validate/rollback 保持 admin。
 definitions.DELETE("/:id", admin, requireActive, h.DeleteDefinition)
 definitions.POST("/:id/validate", admin, requireActive, h.ValidateDefinition)
 definitions.POST("/:id/rollback", admin, requireActive, h.RollbackDefinition)
 // admin/owner 管理白名单。
 definitions.PUT("/:id/editors", admin, requireActive, h.SetWorkflowEditors)
```

> 不能同组两次 `POST("", ...)` 挂不同中间件——Gin 会注册重复路由 panic。正确写法：把 `requireActive` 合并进 member 中间件链。实际修改为：把上面 `POST`/`PUT draft`/`POST publish` 行整体替换为 `definitions.POST("", append(member, requireActive)...)` 形式，或直接 `definitions.POST("", member..., requireActive, h.CreateDefinition)`（`append` 会改底层数组，用显式新切片）。稳妥写法见 Step 3 代码块。

(2) grant 组（110-114 行）追加三行：

```go
 grant.POST("/agents/:id/request-editor", h.RequestEditorAccess)
 grant.POST("/skills/:id/request-editor", h.RequestEditorAccess)
 grant.POST("/knowledge/workspaces/:name/documents/:documentID/request-access", h.RequestEditorAccess)
 grant.POST("/mcp/servers/:id/request-editor", h.RequestEditorAccess)
 grant.POST("/knowledge/workspaces/:name/request-editor", h.RequestEditorAccess)
 grant.POST("/workflows/:id/request-editor", h.RequestEditorAccess)
```

(3) `api/http/handler/mcp_handler.go` 218 行：

```go
 v1.GET("/servers/:id/config", write(h.GetServerConfig)...)
```

- [ ] **Step 2: Write the failing router/contract test**

Run: `go test ./api/http/ -run 'Test.*Routes|Contract' -v`
> 若 `api/http/contract_test.go` 有注册路由的 smoke test（`RegisterRoutes` 后 `HandleContext`），先跑一遍确认新路由可用；否则以 Task 9 的契约测试兜底。此步先跑现有测试确认不回归。

Expected: 现有测试 PASS；新增路由在 Task 9 契约测试中断言。

- [ ] **Step 3: Implement router（修复 Step 1 重复注册问题）**

将 `registerWorkflows` 定义组段整体替换为（member 链已含 requireActive 语义——先确认 `member` 变量与 `requireActive` 的现有组合方式；若 member 不含 requireActive，用中间件组合函数）：

```go
 member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
 admin := middleware.RequireTenantRole("admin")
 definitions := r.Group("/workflows", member...)
 edit := append(append([]gin.HandlerFunc{}, member...), requireActive)
 definitions.GET("", h.ListDefinitions)
 definitions.GET("/:id", h.GetDefinition)
 definitions.GET("/:id/versions", h.ListVersions)
 definitions.GET("/:id/versions/:versionID", h.GetVersion)
 definitions.POST("", edit..., h.CreateDefinition)
 definitions.PUT("/:id/draft", edit..., h.UpdateDefinition)
 definitions.POST("/:id/publish", edit..., h.PublishDefinition)
 definitions.DELETE("/:id", admin, requireActive, h.DeleteDefinition)
 definitions.POST("/:id/validate", admin, requireActive, h.ValidateDefinition)
 definitions.POST("/:id/rollback", admin, requireActive, h.RollbackDefinition)
 definitions.PUT("/:id/editors", admin, requireActive, h.SetWorkflowEditors)
```

> `gin.HandlerFunc` 已在文件 import 中。`edit` 为新建切片避免 `append(member...)` 覆盖底层数组。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go vet ./api/http/... && go test ./api/http/ -short -v`
Expected: 编译通过 + 现有测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add api/http/router.go api/http/handler/mcp_handler.go
git commit -m "feat(workflow): member edit routes, editors endpoint and mcp config GET for members"
```

---

### Task 7: wiring —— buildWorkflow 注入 roles/editorRepo；grantEditor 闭包 +3 kind 分发

**Files:**

- Modify: `api/wiring/workflow.go`（`buildWorkflow` 212-235）
- Modify: `api/wiring/agent.go`（grantEditor 闭包 530-545）

**Interfaces:**

- Consumes: Task 3 的 `workflowpersist.NewPgWorkflowResourceEditorRepo(db)`；Task 4 的 `SetTenantRoleResolver`/`SetEditorRepo`；`c.Agent.RoleResolver`（`agentport.TenantRoleResolver`，签名与 workflow port 结构兼容）；`c.Knowledge.WorkspaceService.GetWorkspace(ctx, tenantID, name)`。
- Produces: workflow 服务注入完整依赖；grantEditor 闭包支持 `mcp`/`knowledge_workspace`/`workflow` 三种新 kind。Task 8 测试 + E2E 依赖。

- [ ] **Step 1: Implement wiring changes**

(1) `api/wiring/workflow.go` `buildWorkflow` 内 `defService` 装配追加两行（在 `SetSkillBindingResolver` 后）：

```go
 defService.SetTenantRoleResolver(c.Agent.RoleResolver)
 defService.SetEditorRepo(workflowpersist.NewPgWorkflowResourceEditorRepo(db))
```

> 若 `buildWorkflow` 中 `db` 变量名不同（如 `pool`），按现状。需确认 `workflowpersist` import 别名已存在。

(2) `api/wiring/agent.go` grantEditor 闭包 `switch` 追加 3 case（`knowledge_doc` 分支后、`default` 前）：

```go
 case "mcp": // 新增
  return resourceEditors.AddEditorForKind(ctx, tenantID, "mcp", resourceID, editorID, "operation-gate")
 case "knowledge_workspace": // 新增：workspace editors 以 UUID 存储，:name 须解析为 UUID
  workspace, err := c.Knowledge.WorkspaceService.GetWorkspace(ctx, tenantID, resourceID)
  if err != nil {
   return err
  }
  return resourceEditors.AddEditorForKind(ctx, tenantID, "knowledge", workspace.ID, editorID, "operation-gate")
 case "workflow": // 新增
  return resourceEditors.AddEditorForKind(ctx, tenantID, "workflow", resourceID, editorID, "operation-gate")
```

- [ ] **Step 2: Run tests to verify build**

Run: `go build ./api/... ./internal/workflow/... && go vet ./api/wiring/`
Expected: 编译通过。

- [ ] **Step 3: Commit**

```bash
git add api/wiring/workflow.go api/wiring/agent.go
git commit -m "feat(wiring): inject workflow role/editor deps and route grant kinds"
```

---

### Task 8: ProposeGrantEditor 六类白名单 + grantEditor 分发测试

**Files:**

- Modify: `internal/agent/application/operation_proposal_service_test.go`（`TestProposeGrantEditor` 扩展）
- Test: `internal/agent/application/operation_proposal_service_test.go`（新增 `TestApproveGrantEditorForNewKinds`）

**Interfaces:**

- Consumes: Task 6/7 的路由与闭包（此任务只测 application 层）。
- Produces: 六类 `resourceType` 的 `ProposeGrantEditor` 白名单；grantEditor 分发契约（proposal → grantEditor(resourceType, resourceID, editorID)）。Task 9 契约、Task 16 E2E 验证端到端。

- [ ] **Step 1: Write the failing tests**

在 `operation_proposal_service_test.go` 追加：

```go
func TestProposeGrantEditorAllResourceTypes(t *testing.T) {
 ctx := context.Background()
 repo := newOperationProposalRepoFake()
 service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)

 for _, rt := range []string{"agent", "skill", "knowledge_doc", "mcp", "knowledge_workspace", "workflow"} {
  t.Run(rt, func(t *testing.T) {
   err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", rt, "res-1", "Res Name")
   require.NoError(t, err)
   found := false
   for _, p := range repo.proposals {
    if p.OpType == string(port.OpGrantEditor) && p.ProposerID == "member-1" && p.Fingerprint == "grant_editor|"+rt+"|res-1|member-1" {
     found = true
    }
   }
   require.True(t, found, "expected grant_editor proposal for %s", rt)
  })
 }

 // 非法类型 fail-closed。
 err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "bogus", "res-1", "Res")
 require.ErrorIs(t, err, domain.ErrProposalInvalid)
}

func TestApproveGrantEditorForNewKinds(t *testing.T) {
 ctx := context.Background()
 repo := newOperationProposalRepoFake()
 service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)

 var got []struct{ rt, rid, eid string }
 service.WithGrantEditor(func(_ context.Context, _, rt, rid, eid string) error {
  got = append(got, struct{ rt, rid, eid string }{rt, rid, eid})
  return nil
 })

 for _, rt := range []string{"mcp", "knowledge_workspace", "workflow"} {
  require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", rt, "res-"+rt, "Res"))
 }
 // 按序审批 3 个提案，断言 grantEditor 收到正确分发参数。
 var pids []string
 for id := range repo.proposals {
  pids = append(pids, id)
 }
 require.Len(t, pids, 3)
 for _, pid := range pids {
  require.NoError(t, service.Approve(ctx, "tenant-1", "admin-1", pid, "granted"))
 }
 require.Len(t, got, 3)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/application/ -run 'TestProposeGrantEditorAllResourceTypes|TestApproveGrantEditorForNewKinds' -v`
Expected: 编译失败（`domain.ErrProposalInvalid` 若未 import）或 FAIL（mcp/workspace/workflow 被 `default` 分支拒绝 → `ErrProposalInvalid`）。

- [ ] **Step 3: Implement ProposeGrantEditor 白名单扩展**

`internal/agent/application/operation_proposal_service.go` `ProposeGrantEditor` 内 `switch resourceType` 扩为六类：

```go
 switch resourceType {
 case "agent", "skill", "knowledge_doc", "mcp", "knowledge_workspace", "workflow":
 default:
  return domain.ErrProposalInvalid
 }
```

> 若现有为 `case "agent", "skill", "knowledge_doc": default: ...`，直接替换该 switch。`payloadSummary` 已含 resourceType/resourceId/resourceName/applicant/action，无需改动。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/application/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/operation_proposal_service.go internal/agent/application/operation_proposal_service_test.go
git commit -m "feat(agent): accept six resource types in grant_editor proposals"
```

---

### Task 9: 契约/测试同步 + go vet + go test -short 全绿

**Files:**

- Modify: `api/http/contract_test.go` 与 `api/http/testdata/contracts/*.golden.json`（若新路由/响应字段影响 golden）
- Test: 全仓库 `go vet && go test -short ./...`

**Interfaces:**

- Consumes: Task 1-8 全部改动。
- Produces: 全仓库编译通过、契约测试通过、无残留 mock 未同步。

- [ ] **Step 1: 枚举并同步契约**

Run: `go test ./api/http/ -run Contract -v`
若失败，`api/http/testdata/contracts/*.golden.json` 需更新。先看差异：`git diff api/http/testdata/contracts/`。若 `GET /workflows/:id` 响应因 `editors`/`created_by` 字段新增而 golden 变化，更新对应 golden（此为预期契约变更，审查通过后提交）。

- [ ] **Step 2: 检查残留 mock/接口不同步**

Run: `grep -rn "UpdateDefinition(\|CreateNextVersion(" --include="*_test.go" internal/ | grep -v 'editorActor\|""\|"" )'`，修正所有未带 editorActor 参数的调用。

- [ ] **Step 3: 全仓库验证**

Run: `go vet ./... && go test -short ./...`
Expected: 全部 PASS。

- [ ] **Step 4: Commit**

```bash
git add api/http/ internal/ pkg/
git commit -m "test(workflow): sync contracts and mocks for editor whitelist"
```

---

### Task 10: 前端共享 useRequestEditorAccess / RequestEditorButton + operationProposal.api 六类 URL

**Files:**

- Modify: `web/src/modules/operation-gate/api/operationProposal.api.ts`（`requestEditorAccess` 类型与 URL switch 扩展 + 导出 `GrantableResourceType`）
- Create: `web/src/shared/hooks/useRequestEditorAccess.ts`
- Create: `web/src/shared/components/RequestEditorButton.tsx`
- Modify: `web/src/shared/hooks/index.ts`、`web/src/shared/components/index.ts`、`web/src/shared/index.ts`（导出）
- Test: `web/src/shared/hooks/__tests__/useRequestEditorAccess.test.tsx`、`web/src/shared/components/__tests__/RequestEditorButton.test.tsx`

**Interfaces:**

- Consumes: `@/services/client`；antd `message`。
- Produces: `GrantableResourceType = 'agent' | 'skill' | 'knowledge_doc' | 'mcp' | 'knowledge_workspace' | 'workflow'`；`useRequestEditorAccess(resourceType, resourceId, opts?) → { requesting, request }`；`<RequestEditorButton resourceType resourceId options />`。Task 11-14 各资源页面复用。

- [ ] **Step 1: Write the failing tests**

新建 `web/src/shared/hooks/__tests__/useRequestEditorAccess.test.tsx`：

```tsx
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { useRequestEditorAccess } from '../useRequestEditorAccess';

vi.mock('antd', async (importOriginal) => {
  const mod = await importOriginal<typeof import('antd')>();
  return { ...mod, message: { success: vi.fn(), error: vi.fn() } };
});
vi.mock('@/modules/operation-gate', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/modules/operation-gate')>();
  return {
    ...mod,
    operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({ status: 'pending_approval' }) },
  };
});

import { operationProposalApi } from '@/modules/operation-gate';
import { message } from 'antd';

describe('useRequestEditorAccess', () => {
  it('发起申请并提示成功', async () => {
    const { result } = renderHook(() => useRequestEditorAccess('workflow', 'wf-1', { resourceName: 'My WF' }));
    let ok = false;
    await act(async () => { ok = await result.current.request(); });
    expect(ok).toBe(true);
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalledWith('workflow', 'wf-1', { resourceName: 'My WF' });
    expect(message.success).toHaveBeenCalled();
  });

  it('请求失败返回 false 并提示错误', async () => {
    vi.mocked(operationProposalApi.requestEditorAccess).mockRejectedValueOnce({ response: { data: { error: 'boom' } } });
    const { result } = renderHook(() => useRequestEditorAccess('mcp', 'm-1'));
    let ok: boolean | null = null;
    await act(async () => { ok = await result.current.request(); });
    expect(ok).toBe(false);
    expect(message.error).toHaveBeenCalledWith({ content: 'boom', duration: 3 });
  });
});
```

新建 `web/src/shared/components/__tests__/RequestEditorButton.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RequestEditorButton } from '../RequestEditorButton';

vi.mock('antd', async (importOriginal) => {
  const mod = await importOriginal<typeof import('antd')>();
  return { ...mod, message: { success: vi.fn(), error: vi.fn() } };
});
vi.mock('@/modules/operation-gate', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/modules/operation-gate')>();
  return { ...mod, operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({}) } };
});

describe('RequestEditorButton', () => {
  it('knowledge_doc 渲染「申请查看权限」，其余渲染「申请编辑权限」', () => {
    render(<RequestEditorButton resourceType="knowledge_doc" resourceId="d1" />);
    expect(screen.getByRole('button', { name: '申请查看权限' })).toBeTruthy();
    render(<RequestEditorButton resourceType="workflow" resourceId="w1" />);
    expect(screen.getByRole('button', { name: '申请编辑权限' })).toBeTruthy();
  });

  it('点击触发申请', async () => {
    render(<RequestEditorButton resourceType="workflow" resourceId="w1" />);
    fireEvent.click(screen.getByRole('button', { name: '申请编辑权限' }));
    expect(await screen.findByRole('button', { name: '申请编辑权限' })).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/shared --reporter=verbose`
Expected: FAIL（`useRequestEditorAccess`/`RequestEditorButton` 模块不存在）。

- [ ] **Step 3: Implement**

(1) `web/src/modules/operation-gate/api/operationProposal.api.ts` 替换 `requestEditorAccess` 段并导出类型：

```ts
// 六类资源的白名单自助申请：workflow/mcp/workspace 走统一 request-editor；
// knowledge_doc 走 request-access（编辑/查看一体，保留原路由）。
export type GrantableResourceType =
  | 'agent'
  | 'skill'
  | 'knowledge_doc'
  | 'mcp'
  | 'knowledge_workspace'
  | 'workflow';

export interface RequestEditorAccessOptions {
  workspaceName?: string;
  resourceName?: string;
}

function requestAccessUrl(resourceType: GrantableResourceType, resourceId: string, workspaceName?: string): string {
  switch (resourceType) {
    case 'knowledge_doc':
      return `/knowledge/workspaces/${workspaceName ?? ''}/documents/${resourceId}/request-access`;
    case 'knowledge_workspace':
      return `/knowledge/workspaces/${resourceId}/request-editor`;
    case 'mcp':
      return `/mcp/servers/${resourceId}/request-editor`;
    default:
      return `/${resourceType}s/${resourceId}/request-editor`;
  }
}
```

```ts
  requestEditorAccess: async (
    resourceType: GrantableResourceType,
    resourceId: string,
    opts?: RequestEditorAccessOptions,
  ) => {
    const response = await api.post(requestAccessUrl(resourceType, resourceId, opts?.workspaceName), {
      resourceType,
      resourceId,
      resourceName: opts?.resourceName,
    });
    return pendingApprovalSchema.parse(response.data);
  },
```

(2) 新建 `web/src/shared/hooks/useRequestEditorAccess.ts`：

```ts
import { message } from 'antd';
import { useCallback, useState } from 'react';

import {
  operationProposalApi,
  type GrantableResourceType,
  type RequestEditorAccessOptions,
} from '@/modules/operation-gate';

// 共享「申请权限」入口：封装发起 grant_editor 提案，成功统一提示进入审批中心。
export function useRequestEditorAccess(resourceType: GrantableResourceType, resourceId: string, options?: RequestEditorAccessOptions) {
  const [requesting, setRequesting] = useState(false);
  const request = useCallback(async (): Promise<boolean> => {
    setRequesting(true);
    try {
      await operationProposalApi.requestEditorAccess(resourceType, resourceId, options);
      message.success({ content: '已提交，等待管理员审批', duration: 2 });
      return true;
    } catch (err) {
      const error = err as { response?: { data?: { error?: string } } };
      message.error({ content: error?.response?.data?.error || '操作失败', duration: 3 });
      return false;
    } finally {
      setRequesting(false);
    }
  }, [resourceType, resourceId, options]);

  return { requesting, request };
}
```

(3) 新建 `web/src/shared/components/RequestEditorButton.tsx`：

```tsx
import { Button } from 'antd';
import type { ButtonProps } from 'antd';

import { useRequestEditorAccess } from '../hooks/useRequestEditorAccess';
import type { GrantableResourceType, RequestEditorAccessOptions } from '@/modules/operation-gate';

interface RequestEditorButtonProps {
  resourceType: GrantableResourceType;
  resourceId: string;
  options?: RequestEditorAccessOptions;
  buttonProps?: ButtonProps;
}

// 统一申请入口按钮：knowledge_doc 为「申请查看权限」，其余为「申请编辑权限」。
export function RequestEditorButton({ resourceType, resourceId, options, buttonProps }: RequestEditorButtonProps) {
  const { requesting, request } = useRequestEditorAccess(resourceType, resourceId, options);
  const label = resourceType === 'knowledge_doc' ? '申请查看权限' : '申请编辑权限';
  return (
    <Button {...buttonProps} loading={requesting} onClick={() => { void request(); }}>
      {label}
    </Button>
  );
}
```

(4) 导出：`web/src/shared/hooks/index.ts` 追加 `export { useRequestEditorAccess } from './useRequestEditorAccess';`；`web/src/shared/components/index.ts` 追加 `export { RequestEditorButton } from './RequestEditorButton';`。确认 `web/src/shared/index.ts` 是否有聚合导出，有则补。

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/shared --reporter=verbose`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/operation-gate/api/operationProposal.api.ts web/src/shared/
git commit -m "feat(web): shared request-editor hook and button across six resources"
```

---

### Task 11: workflow 前端 —— model/api/routes/useWorkflowDesigner/WorkflowDesignerPage + WorkflowEditorsPanel

**Files:**

- Modify: `web/src/modules/workflow/model/workflow.ts`（`workflowDefinitionSchema` + created_by/editors）
- Modify: `web/src/modules/workflow/api/workflow.api.ts`（`setWorkflowEditors`）
- Modify: `web/src/modules/workflow/routes.tsx`（移除 admin 限制）
- Modify: `web/src/modules/workflow/hooks/useWorkflowDesigner.ts`（canEdit/申请/editors 管理）
- Create: `web/src/modules/workflow/components/WorkflowEditorsPanel.tsx`
- Modify: `web/src/modules/workflow/pages/WorkflowDesignerPage.tsx`（readOnly + 申请 + 白名单面板）
- Modify: `web/src/modules/workflow/pages/WorkflowCatalogPage.tsx`（编辑放开 member、删除保持 admin）
- Test: `web/src/modules/workflow/components/__tests__/WorkflowEditorsPanel.test.tsx`、`web/src/modules/workflow/hooks/__tests__/useWorkflowDesigner.test.tsx`

**Interfaces:**

- Consumes: Task 10 的 `useRequestEditorAccess`/`RequestEditorButton`；`@/modules/iam` 的 `useAuth`/`useTenantRole`/`useEditorCandidates`；`shared/hooks` 的 `useResponsive`。
- Produces: `workflowDefinitionSchema` 含 `created_by`/`editors`；`workflowApi.setWorkflowEditors(workflowId, editorIds)`；`useWorkflowDesigner` 扩展返回 `canEdit`/`requestEditor`/`saveEditors`。Task 12-14 与 E2E 依赖。

- [ ] **Step 1: Write the failing tests**

新建 `web/src/modules/workflow/components/__tests__/WorkflowEditorsPanel.test.tsx`（mock 风格对齐 `useEditAgentPage.test.tsx` 的 `vi.mock('@/modules/iam', ...)` 模式；`Member` 标识为 `user_id`，选项 label 为 `github_login || user_id`）：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkflowEditorsPanel } from '../WorkflowEditorsPanel';

vi.mock('@/modules/iam', () => ({
  useEditorCandidates: () => ({
    candidates: [
      { user_id: 'm-1', github_login: 'alice', role: 'member', joined_at: '2026-01-01' },
      { user_id: 'm-2', github_login: 'bob', role: 'member', joined_at: '2026-01-01' },
    ],
    loading: false,
  }),
}));

describe('WorkflowEditorsPanel', () => {
  const onSave = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => onSave.mockClear());

  it('渲染当前白名单并在保存时提交原集合', async () => {
    render(<WorkflowEditorsPanel editors={['m-1']} onSave={onSave} />);
    expect(screen.getByRole('combobox')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(['m-1']));
  });

  it('新增成员后保存新集合', async () => {
    render(<WorkflowEditorsPanel editors={['m-1']} onSave={onSave} />);
    // antd Select 多选：打开下拉并点选选项（按 title 命中 github_login）。
    fireEvent.mouseDown(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByTitle('bob'));
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(['m-1', 'm-2']));
  });
});
```

> 说明：antd Select 下拉交互以本仓库现有 Select 测试（如 `web/src/modules/knowledge/components/WorkspaceCreateModal.test.tsx`）的写法为准；若 antd 版本交互有差异，仅调整选项点击方式，断言不变。

新建 `web/src/modules/workflow/hooks/__tests__/useWorkflowDesigner.test.ts`（renderHook + iam mock，模板对齐 `useEditAgentPage.test.tsx`）：

```tsx
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../../api/workflow.api';
import type { WorkflowDefinition } from '../../model/workflow';
import { useWorkflowDesigner } from '../useWorkflowDesigner';

vi.mock('../../api/workflow.api', () => ({
  workflowApi: {
    getWorkflow: vi.fn(),
    createWorkflow: vi.fn(),
    updateWorkflowDraft: vi.fn(),
    validateWorkflow: vi.fn(),
    publishWorkflow: vi.fn(),
  },
}));
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'u-1' } }),
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
}));
// 共享申请 hook（useRequestEditorAccess）转调 operationProposalApi，mock 其 API 依赖。
vi.mock('@/modules/operation-gate', () => ({
  operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({}) },
}));

const definition = (editors: string[]): WorkflowDefinition => ({
  id: 'wf-1',
  name: 'Research',
  description: '',
  revision: 1,
  spec: { nodes: [], edges: [] },
  input_schema: { task_label: '任务', task_description: '', fields: [] },
  created_by: 'u-2',
  editors,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
});

describe('useWorkflowDesigner canEdit', () => {
  beforeEach(() => vi.clearAllMocks());

  it('白名单成员（命中 editors）可编辑', async () => {
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition(['u-1']));
    const { result } = renderHook(() => useWorkflowDesigner('wf-1'));
    await waitFor(() => expect(result.current.canEdit).toBe(true));
    expect(result.current.editors).toEqual(['u-1']);
  });

  it('非白名单普通成员只读且可申请', async () => {
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition(['u-9']));
    const { result } = renderHook(() => useWorkflowDesigner('wf-1'));
    await waitFor(() => expect(result.current.canEdit).toBe(false));
    expect(result.current.createdBy).toBe('u-2');
    await act(async () => { await result.current.requestEditor(); });
    const { operationProposalApi } = await import('@/modules/operation-gate');
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalledWith('workflow', 'wf-1', { resourceName: 'Research' });
  });

  it('新建页（无 id）恒可编辑', async () => {
    const { result } = renderHook(() => useWorkflowDesigner());
    expect(result.current.canEdit).toBe(true);
  });
});
```

> 说明：`requestEditor` 由 Task 10 的 `useRequestEditorAccess` 提供，hook 扩展处透传；`resourceName` 取 `name` state。若 hook 扩展实现细节有出入，按实现微调断言。

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/modules/workflow/components/__tests__/WorkflowEditorsPanel.test.tsx src/modules/workflow/hooks/__tests__/useWorkflowDesigner.test.ts --reporter=verbose`
Expected: FAIL（`WorkflowEditorsPanel` 不存在 / 扩展后的 `useWorkflowDesigner` 缺 `canEdit`/`requestEditor`）。

- [ ] **Step 3: Implement**

(1) `web/src/modules/workflow/model/workflow.ts` `workflowDefinitionSchema` 追加：

```ts
  created_by: z.string().optional().default(''),
  editors: z.array(z.string()).optional().default([]),
```

(2) `web/src/modules/workflow/api/workflow.api.ts` 追加：

```ts
  // 白名单管理（admin/owner）：设置工作流可编辑人。
  setWorkflowEditors: async (workflowId: string, editorIds: string[]): Promise<void> => {
    await api.put(`/workflows/${workflowId}/editors`, { editorIds });
  },
```

(3) `web/src/modules/workflow/routes.tsx` 移除两处 `requiredTenantRole="admin"`（/workflows/new 与 /workflows/:id/edit）。

(4) `web/src/modules/workflow/hooks/useWorkflowDesigner.ts` 扩展。文件顶部 import 追加：

```ts
import { useTenantRole, useAuth } from '@/modules/iam';
import { useRequestEditorAccess } from '@/shared/hooks';
```

在 `save`/`validate`/`publish` 定义之后、`return` 之前追加：

```ts
  // P2 白名单：canEdit = admin/owner/白名单成员；新建页（无 id）恒可编辑（创建即 creator）。
  const { user } = useAuth();
  const { isAdmin, isOwner } = useTenantRole();
  const editors = definition?.editors ?? [];
  const canEdit = !id || isAdmin || isOwner || editors.includes(user?.sub ?? '');
  const { requesting, request: requestEditor } = useRequestEditorAccess('workflow', id ?? '', { resourceName: name });
  const saveEditors = async (editorIds: string[]): Promise<void> => {
    if (!id) return;
    await workflowApi.setWorkflowEditors(id, editorIds);
    message.success({ content: '可编辑人已更新', duration: 2 });
  };
```

`return` 对象追加：`canEdit`、`editors`、`createdBy: definition?.created_by ?? ''`、`requesting`、`requestEditor`、`saveEditors`。

> `useRequestEditorAccess` 的 `options.resourceName` 取 `name` state（闭包引用，随输入实时更新）。hook 内新增的 `message.success` 复用已有 `message` import。

(5) 新建 `web/src/modules/workflow/components/WorkflowEditorsPanel.tsx`：

```tsx
import { Button, Option, Select, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { useEditorCandidates } from '@/modules/iam';

const { Text } = Typography;

interface WorkflowEditorsPanelProps {
  editors: string[];
  onSave: (editorIds: string[]) => Promise<void>;
}

// 工作流「可编辑人」白名单管理（admin/owner 可见）：多选成员 + 保存。
// 选项映射与 AgentFormSections「可编辑人」一致：value=user_id，label=github_login||user_id。
export function WorkflowEditorsPanel({ editors, onSave }: WorkflowEditorsPanelProps) {
  const { candidates, loading } = useEditorCandidates();
  const [editorIds, setEditorIds] = useState<string[]>(editors);
  const [saving, setSaving] = useState(false);

  useEffect(() => { setEditorIds(editors); }, [editors]);

  const save = async () => {
    setSaving(true);
    try {
      await onSave(editorIds);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="workflow-editors-panel" style={{ marginBottom: 16 }}>
      <Text strong>可编辑人</Text>
      <Select
        mode="multiple"
        style={{ width: '100%', marginTop: 8 }}
        placeholder="选择可编辑的成员"
        allowClear
        loading={loading}
        value={editorIds}
        onChange={setEditorIds}
      >
        {candidates.map((member) => (
          <Option key={member.user_id} value={member.user_id}>
            {member.github_login || member.user_id}
          </Option>
        ))}
      </Select>
      <Button type="primary" size="small" loading={saving} onClick={save} style={{ marginTop: 8 }}>
        保存
      </Button>
    </div>
  );
}
```

> `workflowId` 不进入面板 props——`onSave` 由页面闭包捕获 id（`(ids) => designer.saveEditors(ids)`）。WorkflowDesignerPage 集成：admin/owner 时在 header 区渲染 `<WorkflowEditorsPanel editors={designer.editors} onSave={(ids) => { void designer.saveEditors(ids); }} />`，保存成功后 `message.success({ content: '可编辑人已更新', duration: 2 })`（可在 saveEditors 内统一提示）。

(6) `web/src/modules/workflow/pages/WorkflowDesignerPage.tsx`：`WorkflowDesignerDesktop` 内，`if (designer.loading) return <Skeleton active />;` 后加 readOnly 分支：

```tsx
  if (designer.loading) return <Skeleton active />;
  if (!designer.canEdit) {
    return (
      <Result
        status="info"
        title="无编辑权限"
        subTitle="工作流只读。如需编辑，请提交权限申请。"
        extra={<RequestEditorButton resourceType="workflow" resourceId={id ?? ''} options={{ resourceName: designer.name }} />}
      />
    );
  }
```

> ⚠️ 新建页（无 id）不应只读——新建即创建者。修正：`!designer.canEdit && id` 才只读。且顶部白名单面板（admin/owner 可见）挂在 header 区，用 `designer.isAdmin || designer.isOwner` 控制（需从 `useTenantRole` 暴露）。

(7) `web/src/modules/workflow/pages/WorkflowCatalogPage.tsx`：Header「新建」按钮与 Table「编辑草稿」按钮放开（`canManage` 只控删除）：

```tsx
  // 编辑放开给 member（canEdit 由 designer 页判定），删除保持 admin。
  <Table
    ...
    // 「编辑草稿」onClick 不再受 canManage 门控
    // 「删除」保持 canManage={isAdmin}
  />
```

> 读该文件确认 Header/Table 的 `canManage` 用法后，仅放开编辑/新建，删除保留 `canManage={isAdmin}`。

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/modules/workflow --reporter=verbose`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/workflow/
git commit -m "feat(web): workflow member edit, editor whitelist panel and request-access"
```

---

### Task 12: mcp 前端 —— 成员申请 + readOnly 配置 + 编辑放开

**Files:**

- Modify: `web/src/modules/mcp/hooks/useEditMCPPage.ts`（canEdit/editors/申请）
- Modify: `web/src/modules/mcp/model/mcp.ts`（`mcpServerSchema` 未含 editors——若用 config 响应则无需；确认）
- Modify: `web/src/modules/mcp/pages/MCPServersPage.tsx`（编辑入口放开 member）
- Modify: `web/src/modules/mcp/pages/EditMCPPage.tsx`（readOnly + 申请按钮）
- Modify: `web/src/modules/mcp/routes.tsx`（移除 `/mcp/:id/edit` 的 admin 限制）
- Test: `web/src/modules/mcp/__tests__/*`（按现有模式）

**Interfaces:**

- Consumes: Task 10 的 `RequestEditorButton`；`useEditMCPPage` 现有返回。
- Produces: mcp 配置页成员可读 + 申请入口；编辑页 readOnly 态。Task 16 E2E 依赖。

- [ ] **Step 1: Write the failing test（追加到现有文件）**

`web/src/modules/mcp/hooks/useEditMCPPage.test.ts` 已存在且只测纯函数（`configToFormValues`/`buildMCPUpdateConfig`）。在其 import 区追加（mock 模板对齐 `useEditAgentPage.test.tsx`）：

```tsx
import { renderHook, waitFor } from '@testing-library/react';

import { mcpApi } from '../api/mcp.api';
import { useEditMCPPage } from './useEditMCPPage';

vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('../api/mcp.api', () => ({ mcpApi: { getConfig: vi.fn(), update: vi.fn() } }));
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'u-1' } }),
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
}));
```

文件末尾追加：

```tsx
const editorConfig = (editors: string[]): MCPServerConfigResponse => ({
  id: 'server-1',
  name: 'private server',
  version: '1',
  transport: 'http',
  command: '',
  args: [],
  env: {},
  url: 'https://mcp.example.com',
  capabilities: [],
  timeout: 30e9,
  editors,
});

describe('useEditMCPPage canEdit', () => {
  it('白名单成员（命中 config.editors）可编辑', async () => {
    vi.mocked(mcpApi.getConfig).mockResolvedValue(editorConfig(['u-1']));
    const { result } = renderHook(() => useEditMCPPage('server-1'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.editors).toEqual(['u-1']);
    expect(result.current.canEdit).toBe(true);
  });

  it('非白名单普通成员只读', async () => {
    vi.mocked(mcpApi.getConfig).mockResolvedValue(editorConfig([]));
    const { result } = renderHook(() => useEditMCPPage('server-1'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canEdit).toBe(false);
  });
});
```

> 前提：Task 6 将 `GET /mcp/servers/:id/config` 改为 member 后，成员能拉到含 `editors` 的 config 响应（`GetServerConfig` 已填 `response.Editors`，`filterMCPConfigValues` 只丢敏感键）。

- [ ] **Step 2: Implement**

(1) `web/src/modules/mcp/routes.tsx` 移除 `/mcp/:id/edit` 的 `requiredTenantRole="admin"`。

(2) `web/src/modules/mcp/hooks/useEditMCPPage.ts` 扩展。**注意**：`configToFormValues` 丢弃 `editors`，需单独状态承载。import 追加：

```ts
import { useTenantRole, useAuth } from '@/modules/iam';
```

`useState` 增加 `const [editors, setEditors] = useState<string[]>([]);`，load effect 内 `cfg` 就绪后 `setEditors(cfg.editors)`；末尾追加：

```ts
  const { user } = useAuth();
  const { isAdmin, isOwner } = useTenantRole();
  const canEdit = isAdmin || isOwner || editors.includes(user?.sub ?? '');
  return { loading, submitting, initialValues, handleFinish, canEdit, editors };
```

(3) `web/src/modules/mcp/pages/EditMCPPage.tsx`：`canEdit === false` 时 Form `disabled`，并在表单外显示 `<RequestEditorButton resourceType="mcp" resourceId={id ?? ''} />`。

(4) `web/src/modules/mcp/pages/MCPServersPage.tsx`：编辑入口从 `{isAdmin && (<Button ...>编辑</Button>)}` 改为成员可见（`canEdit` 由编辑页判定；此处直接放开 member 渲染编辑按钮，或按行 editors 判定）。actions column width 相应调整。

> **已核实**：mcp `GET /mcp/servers/:id/config` 响应带 `editors`（`mcp_handler.go:298-307`：`ListEditors` → `response.Editors = editors`）。前端直接用响应 `editors` 计算 canEdit。

- [ ] **Step 3: Run tests to verify they pass**

Run: `cd web && npx vitest run src/modules/mcp --reporter=verbose`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add web/src/modules/mcp/
git commit -m "feat(web): mcp member edit request and read-only config page"
```

---

### Task 13: knowledge 前端 —— workspace 申请入口 + doc 迁移共享组件

**Files:**

- Modify: `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx`（doc 迁移共享 hook + workspace 级申请）
- Modify: `web/src/modules/knowledge/components/WorkspaceDetailHeader.tsx`（申请按钮插槽）
- Modify: `web/src/modules/knowledge/components/WorkspaceDocumentsTable.tsx`（onRequestAccess 保留/复用）
- Test: `web/src/modules/knowledge/**/__tests__/*`

**Interfaces:**

- Consumes: Task 10 的 `useRequestEditorAccess`/`RequestEditorButton`。
- Produces: workspace 非白名单成员「申请编辑权限」入口；knowledge_doc 迁移共享组件（文案「申请查看权限」）。Task 16 E2E 依赖。

- [ ] **Step 1: Implement**

(1) `KnowledgeDetailPage.tsx`：现有 `handleRequestAccess`（`operationProposalApi.requestEditorAccess('knowledge_doc', doc.id, { workspaceName: name, resourceName: ... })`）替换为共享 `useRequestEditorAccess('knowledge_doc', doc.id, { workspaceName: name, resourceName:`${name}/${doc.source}`})`；`WorkspaceDetailHeader` 区对非白名单 member 渲染 `<RequestEditorButton resourceType="knowledge_workspace" resourceId={name} options={{ resourceName: name }} />`。

(2) `WorkspaceDetailHeader.tsx`：props 增加 `canRequestEditor?: boolean`，为 true 时渲染申请按钮插槽。

> **已核实**：`GetWorkspace`（= `repo.GetByName`）与 `ListWorkspaces` 均不附带 `editors`（workspace 服务仅 `ListEditors(ctx, tenantID, workspaceID)` 供 detail prefill，GET handler 未走）。因此 workspace 前端 `canRequestEditor` 用 `!isAdmin && !isOwner` 近似（申请入口对普通成员开放；重复申请由后端 dedupe 拒绝并提示「已提交」）。批准授予时的 name→UUID 解析由 grant 闭包 `GetWorkspace` 完成，与前端无 editors 字段无耦合。

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd web && npx vitest run src/modules/knowledge --reporter=verbose`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add web/src/modules/knowledge/
git commit -m "feat(web): knowledge workspace editor request and shared doc request migration"
```

---

### Task 14: agent/skill 前端迁移共享组件（不回归）

**Files:**

- Modify: `web/src/modules/agent/pages/EditAgentPage.tsx`（`handleRequestEditor` 迁移共享）
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`（申请按钮迁移共享）
- Test: `web/src/modules/agent/__tests__/*`、`web/src/modules/skill/__tests__/*` 不回归

**Interfaces:**

- Consumes: Task 10 的 `RequestEditorButton`/`useRequestEditorAccess`。
- Produces: agent/skill 申请入口统一走共享组件，行为不回归。

- [ ] **Step 1: Implement**

(1) `EditAgentPage.tsx`：`handleRequestEditor`（`requestEditorAccess('agent', agent.id, { resourceName: agent.name })` + `setRequesting` + `message.success`）替换为 `<RequestEditorButton resourceType="agent" resourceId={agent.id} options={{ resourceName: agent.name }} />`，移除手写 `requesting` 状态。

(2) `SkillWorkspacePage.tsx`：Form 外申请按钮（`canEdit` 逻辑保留）替换为 `<RequestEditorButton resourceType="skill" resourceId={workspace.id} options={{ resourceName: workspace.name }} />`。

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd web && npx vitest run src/modules/agent src/modules/skill --reporter=verbose`
Expected: 不回归 PASS。

- [ ] **Step 3: Commit**

```bash
git add web/src/modules/agent/ web/src/modules/skill/
git commit -m "refactor(web): migrate agent/skill request-editor to shared component"
```

---

### Task 15: 前端全量质量门禁

**Files:**

- Test: `web/`

- [ ] **Step 1: Lint + build + 全量单测**

Run: `make fe-lint && make fe-build && cd web && npx vitest run --reporter=verbose`
Expected: 全部 PASS。

- [ ] **Step 2: 修复发现的问题并复跑**

- [ ] **Step 3: Commit**

```bash
git add web/
git commit -m "chore(web): pass lint, build and unit tests for editor whitelist"
```

---

### Task 16: E2E 系统验收（stratum-e2e-tester，R3 级）

**Files:**

- Test: 调用 `stratum-e2e-tester` agent（封装 `stratum-e2e-development` skill）
- Test: `.test/verification.yaml` R3 自动执行 `STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all` soak

**Interfaces:**

- Consumes: Task 1-15 全部改动（clean commit）。
- Produces: 验收报告；CI 全绿后合并 PR。

- [ ] **Step 1: 创建 PR（clean commit 之上）**

```bash
git fetch origin main
git push -u origin feat/unified-resource-whitelist
gh pr create --base main --title "feat(workflow): unified resource whitelist and member editor request" --body "..."
```

- [ ] **Step 2: 本地无头 E2E 验收**

调用 `stratum-e2e-tester` agent，覆盖以下场景（按 spec 测试清单）：

1. workflow 全链路：admin 设白名单 → 成员只读 + 申请 → admin 审批 → 授予 → 可编辑发布。
2. mcp / workspace 申请链路：申请 → 审批 → 授予 → 可编辑。
3. agent/skill/doc 回归：现有申请链路不回归。
4. 越权：非白名单成员调用 `PUT /workflows/:id/draft` → 403（fail-closed）。

- [ ] **Step 3: 修复失败并复跑至全绿**

- [ ] **Step 4: CI 全绿后合并并清理 worktree**

```bash
gh pr merge --merge
git worktree remove ../stratum-unified-resource-whitelist
```

---

## Self-Review

**1. Spec coverage（对照 spec 逐段）**

| spec 要求 | 对应任务 |
|---|---|
| `resource_editors` kind='workflow'（无需改表） | Task 1（注释）+ Task 3（helpers） |
| `workflow_definitions.created_by` 幂等 DDL + 空值兜底 | Task 1 |
| grantRouteResourceType +3 路由 | Task 6 |
| ProposeGrantEditor 六类白名单 | Task 8 |
| grantEditor 闭包 +3 kind（GetWorkspace 解析 workspace UUID） | Task 7 |
| workflow 编辑路由 member、DELETE/validate/rollback 保持 admin | Task 6 |
| `PUT /workflows/:id/editors` 新增 | Task 6（路由）+ Task 5（handler）+ Task 4（service） |
| `GET /workflows/:id` 响应附 `editors` | Task 4（service.Get）+ Task 2（domain json tag） |
| workflow 所有权矩阵（spec 矩阵）fail-closed | Task 4 |
| Create 写 `created_by=actorID` + 自动授予 creator | Task 3（store）+ Task 4（service） |
| 白名单写事务内复查 TOCTOU | Task 3（revalidateEditorIfActor） |
| mcp `config GET` admin→member | Task 6 |
| workspace 申请入口 | Task 6（路由）+ Task 7（grant）+ Task 13（前端） |
| 共享 useRequestEditorAccess/RequestEditorButton | Task 10 |
| 六资源前端接入 | Task 11-14 |
| workflow canEdit 公式 | Task 11 |
| 审批中心 resourceName 展示 | Task 8（ProposeGrantEditor 已传）+ E2E 验证 |
| 后端测试清单 | Task 8/9/4/3 |
| 契约测试 golden | Task 9 |
| E2E（R3，4 场景） | Task 16 |

**2. Placeholder scan**

- 全部代码块（含 Task 5 handler 测试、Task 11/12/13 前端 hook 与页面测试）均已按真实文件结构实写：handler 测试基于 `workflow_handler_test.go` 的 `workflowDefinitionFake` + gin TestMode 模式；前端测试基于 `useEditAgentPage.test.tsx` 的 `vi.mock('@/modules/iam', ...)` + `vi.hoisted` react-router-dom 模式，`Member` 结构使用真实 `{user_id, github_login, avatar_url, role, joined_at}`。无 TDD 占位。
- Task 3 auto-grant 测试依赖 `resourceaccess.AllowedEditorRoles()` 是否导出——已给出 fallback（硬编码 `[]string{"admin","owner","member"}`）。
- Task 13 workspace 前端「是否带 editors」开放点已核实关闭：`GetWorkspace`/`ListWorkspaces` 均不附带 editors，`canRequestEditor` 用 `!isAdmin && !isOwner` 近似（见 Task 13 内注释）。

**3. Type consistency**

- `UpdateDefinition(ctx, tenantID string, d *domain.Definition, expected int64, editorActor string, ev *...ResourceChangeAuditEvent) error`：Task 3 定义，Task 3 的 service 调用点 `(ctx, tenantID, definition, cmd.ExpectedRevision, "", ev)`、Task 4 `(ctx, tenantID, definition, cmd.ExpectedRevision, editorActor, ev)`、memoryStore 签名一致。
- `CreateNextVersion(ctx, tenantID string, definition *domain.Definition, versionID, editorActor string, ev ...)`：Task 3 定义，调用点一致。
- `port.TenantRoleResolver.ResolveTenantRole(context.Context, string, string) (string, error)`：Task 2 定义；Task 4 `stubTenantRole` 实现；Task 7 用 `c.Agent.RoleResolver`（agentport 接口结构兼容）。
- `port.ResourceEditorRepo`：Task 2 定义；Task 3 `PgWorkflowResourceEditorRepo` 实现；Task 4 `memoryEditorRepo` 实现 + `SetEditorRepo`。
- `Definition.CreatedBy`/`Editors` json tag `created_by,omitempty`/`editors,omitempty`：Task 2 定义；Task 11 前端 zod `created_by`/`editors` 对应。
- 前端 `GrantableResourceType` 六值、`requestAccessUrl` switch（knowledge_doc/workspace/mcp/default 复数）与后端路由一一对应。
- `SetEditors(ctx, tenantID, workflowID string, editorIDs []string, actorID string) error`：Task 4 service → Task 5 handler 接口 → Task 6 路由 → Task 11 前端 `setWorkflowEditors`。
