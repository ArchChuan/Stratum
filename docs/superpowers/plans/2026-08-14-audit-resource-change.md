# 审计日志重构：租户级资源变更审计 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `/audit` 从 global_admin 专属的平台 HTTP 审计改为租户 admin/owner 可见的资源变更审计（agent/skill/mcp/knowledge/workflow/evaluation 六类全量记录），并删除平台级 HTTP 审计。

**Architecture:** 唯一数据源为 tenant schema 的 `resource_change_audits`。写端在业务事务内复用 `newChangeAudit` + `insertChangeAudit` 模式（workflow/evaluation 各复制一份同构 helper）；读端新增查询 port + repo（走 `execTenant`，空 tenantID fail-closed），经 proto → handler → `RequireTenantRole("admin")` 路由暴露；操作者经 `public.users` 批量映射为 `display_name > github_login > actor_id`。

**Tech Stack:** Go 1.25（Gin、pgx v5、pgxmock v2）、proto3（buf + protoc-gen-ginstruct）、React 18 + Ant Design 5 + zod、PostgreSQL 多租户 schema。

## Global Constraints

- 多租户 schema 隔离：所有访问 tenant-scoped 表的 repo 方法必须走 `execTenant`（`postgres.ExecTenantWith`）；port 方法显式含 `tenantID string`。
- **空 tenantID fail-closed**：查询 repo `List`/`GetByID`（含内部 Count）入口第一行 `if tenantID == "" { return error }`；SQL 中 `tenant_id` 谓词恒存在，禁止条件性追加。
- 写端 actor 只能来自 handler 认证上下文（`workflowActor(c)` / `commandInput(c)` / `userIDFromCtx`），**禁止从 request body 读取**。
- 投影白名单：workflow `{id,name,description}`；evaluation `{resource_kind,resource_id,status}`；禁止 Spec/InputSchema/revision 载荷/评测指标。
- 业务写与审计同事务；审计插入失败 → 整个事务失败回滚。
- proto 契约走 `proto/audit/audit.proto` → `make proto-gen`；proto 字段 snake_case（仓库现有约定），生成的 Go/TS 面为 camelCase（`resourceKind` 等）。
- 操作者展示优先级 `display_name > github_login > actor_id` 原文兜底；**不返回 email**。
- `actor_name` 筛选在 SQL 子串匹配（`ILIKE`）；禁止对请求参数做 `public.users` 存在性探测。
- 用 `tenantIDFromCtx`（空返回 false），废弃宽容版 `tenantIDFromGinKey`。
- Go 质量门禁：圈复杂度 ≤10、认知复杂度 ≤15、函数长度 ≤120、嵌套 ≤4；任务内跑 `go vet && go test -short ./...`。
- 提交格式 `[type](scope): description`；所有提交在 worktree `feat/audit-resource-change` 分支，禁止 `main`。
- 前端行为常量放 `web/src/constants/`；错误通知 `message.error({content, duration: 0})`；禁止 `alert/confirm/console.log`。
- 每次 `make proto-gen` 或改生成 DTO 后，必须先读生成文件确认字段名再写消费代码（见各任务 codegen 适配步骤）。

---

## Task 1: 共享枚举扩展

**Files:**

- Modify: `internal/audit/domain/change_audit.go`
- Create: `internal/audit/domain/change_audit_test.go`

**Interfaces:**

- Produces: 常量 `ResourceKindWorkflow="workflow"`、`ResourceKindEvaluation="evaluation"`；`ChangeOpPublish="publish"`、`ChangeOpPromote="promote"`、`ChangeOpRollback="rollback"`、`ChangeOpReject="reject"`、`ChangeOpPause="pause"`、`ChangeOpActivate="activate"`。后续任务（workflow/evaluation 写端、读取端、前端常量）都引用这组值。

- [ ] **Step 1: Write the failing test**

`internal/audit/domain/change_audit_test.go`:

```go
package domain

import "testing"

// TestResourceChangeVocabulary 锁定共享枚举集合：六类资源 kind + 全部 operation
// 必须存在且互异。前端 RESOURCE_KIND_OPTIONS 与读取端筛选引用同一组值。
func TestResourceChangeVocabulary(t *testing.T) {
 kinds := []string{
  ResourceKindAgent, ResourceKindSkill, ResourceKindMCP, ResourceKindKnowledge,
  ResourceKindWorkflow, ResourceKindEvaluation,
 }
 seenKind := map[string]bool{}
 for _, k := range kinds {
  if k == "" {
   t.Fatalf("resource kind must not be empty")
  }
  if seenKind[k] {
   t.Fatalf("duplicate resource kind %q", k)
  }
  seenKind[k] = true
 }

 ops := []string{
  ChangeOpCreate, ChangeOpUpdate, ChangeOpDelete,
  ChangeOpPublish, ChangeOpPromote, ChangeOpRollback,
  ChangeOpReject, ChangeOpPause, ChangeOpActivate,
 }
 seenOp := map[string]bool{}
 for _, op := range ops {
  if op == "" {
   t.Fatalf("operation must not be empty")
  }
  if seenOp[op] {
   t.Fatalf("duplicate operation %q", op)
  }
  seenOp[op] = true
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/audit/domain/ -run TestResourceChangeVocabulary -v`
Expected: FAIL，编译错误 `undefined: ResourceKindWorkflow`。

- [ ] **Step 3: Add the constants**

`internal/audit/domain/change_audit.go` 顶部两处 const 块改为：

```go
// Resource kinds audited by the ownership/audit feature.
const (
 ResourceKindAgent      = "agent"
 ResourceKindSkill      = "skill"
 ResourceKindMCP        = "mcp"
 ResourceKindKnowledge  = "knowledge"
 ResourceKindWorkflow   = "workflow"   // 新增：工作流定义生命周期
 ResourceKindEvaluation = "evaluation" // 新增：评测实验生命周期
)

// Operations recorded for each committed change.
const (
 ChangeOpCreate   = "create"
 ChangeOpUpdate   = "update"
 ChangeOpDelete   = "delete"
 ChangeOpPublish  = "publish"  // 新增：workflow 版本发布
 ChangeOpPromote  = "promote"  // 新增：evaluation 发布
 ChangeOpRollback = "rollback" // 新增：evaluation 回滚
 ChangeOpReject   = "reject"   // 新增：evaluation 拒绝
 ChangeOpPause    = "pause"    // 新增：evaluation 暂停
 ChangeOpActivate = "activate" // 新增：evaluation 激活 pending 实验
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/audit/domain/ -run TestResourceChangeVocabulary -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/audit/domain/change_audit.go internal/audit/domain/change_audit_test.go
git commit -m "feat(audit): 扩展资源变更审计共享枚举(workflow/evaluation kind 与生命周期 op)"
```

---

## Task 2: 读取端 query port + repo

**Files:**

- Create: `internal/audit/domain/port/change_audit.go`
- Create: `internal/audit/infrastructure/persistence/change_audit_repo.go`
- Create: `internal/audit/infrastructure/persistence/change_audit_repo_test.go`

**Interfaces:**

- Consumes: Task 1 常量（`ResourceKindWorkflow` 等，本任务 SQL 只读字符串，不强制引用）。
- Produces: `port.ResourceChangeAuditQuery` 接口（`List`/`GetByID`）、`port.ResourceChangeAuditRow`、`port.ResourceChangeAuditFilter`、repo 实现 `persistence.PgResourceChangeAuditRepo` + `persistence.NewPgResourceChangeAuditRepo(pool)`。Task 3 的 handler/wiring 与 Task 6 的契约 stub 消费这些。

- [ ] **Step 1: Create the port**

`internal/audit/domain/port/change_audit.go`:

```go
package port

import (
 "context"
 "encoding/json"
 "time"
)

// ResourceChangeAuditRow 是查询返回的一行只读模型。Before/After 为原样存储的
// JSON 投影（{} 表示 create 等无前值场景）。
type ResourceChangeAuditRow struct {
 ID           string
 ResourceKind string
 ResourceID   string
 Operation    string
 ActorID      string
 ActorName    string // display_name > github_login > actor_id 兜底（system actor 直接 actor_id）
 CreatedAt    time.Time
 Before       json.RawMessage
 After        json.RawMessage
}

// ResourceChangeAuditFilter 列表筛选。Limit<=0 或 Offset<0 由实现归一化为无分页。
type ResourceChangeAuditFilter struct {
 ResourceKind string
 ActorName    string // 子串模糊匹配 display_name/github_login/actor_id 原文
 From         *time.Time
 To           *time.Time
 Limit        int
 Offset       int
}

// ResourceChangeAuditQuery 列出 tenant-scoped 资源变更审计。每个方法都要求
// 非空 tenantID（fail closed）；SQL 中 tenant_id 谓词恒存在，禁止条件性追加。
type ResourceChangeAuditQuery interface {
 List(ctx context.Context, tenantID string, filter ResourceChangeAuditFilter) ([]ResourceChangeAuditRow, int, error)
 // GetByID 查不到时返回 (nil, nil)。
 GetByID(ctx context.Context, tenantID, id string) (*ResourceChangeAuditRow, error)
}
```

- [ ] **Step 2: Create the repo**

`internal/audit/infrastructure/persistence/change_audit_repo.go`:

```go
package persistence

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "strings"

 "github.com/byteBuilderX/stratum/internal/audit/domain/port"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
)

// PgResourceChangeAuditRepo 在 tenant schema 内读取 resource_change_audits。
// 每个方法都要求非空 tenantID：为空时在触碰连接池之前 fail closed
// （跨租户泄露面防御；禁止复制旧 buildAuditFilter 的「空租户省略谓词」模式）。
type PgResourceChangeAuditRepo struct {
 pool *pgxpool.Pool
}

func NewPgResourceChangeAuditRepo(pool *pgxpool.Pool) *PgResourceChangeAuditRepo {
 return &PgResourceChangeAuditRepo{pool: pool}
}

func (r *PgResourceChangeAuditRepo) List(
 ctx context.Context,
 tenantID string,
 f port.ResourceChangeAuditFilter,
) ([]port.ResourceChangeAuditRow, int, error) {
 if tenantID == "" {
  return nil, 0, fmt.Errorf("audit: list resource change audits: tenant id required")
 }
 if f.Limit <= 0 {
  f.Limit = 20
 }
 if f.Offset < 0 {
  f.Offset = 0
 }
 rows := make([]port.ResourceChangeAuditRow, 0)
 var total int
 err := postgres.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  where, args := buildChangeAuditWhere(tenantID, f)
  if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM resource_change_audits `+where, args...).Scan(&total); err != nil {
   return fmt.Errorf("audit: count resource change audits: %w", err)
  }
  if total == 0 {
   return nil
  }
  paging := make([]any, 0, len(args)+2)
  paging = append(paging, args...)
  paging = append(paging, f.Limit, f.Offset)
  rowsQuery := `SELECT id, resource_kind, resource_id, operation, actor_id, created_at,
   before_projection, after_projection
   FROM resource_change_audits ` + where + fmt.Sprintf(` ORDER BY created_at DESC, id DESC
   LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
  got, err := tx.Query(ctx, rowsQuery, paging...)
  if err != nil {
   return fmt.Errorf("audit: query resource change audits: %w", err)
  }
  defer got.Close()
  actorIDs := make([]string, 0, 8)
  for got.Next() {
   var row port.ResourceChangeAuditRow
   var before, after []byte
   if err := got.Scan(&row.ID, &row.ResourceKind, &row.ResourceID, &row.Operation,
    &row.ActorID, &row.CreatedAt, &before, &after); err != nil {
    return fmt.Errorf("audit: scan resource change audit: %w", err)
   }
   row.Before = json.RawMessage(before)
   row.After = json.RawMessage(after)
   rows = append(rows, row)
   actorIDs = append(actorIDs, row.ActorID)
  }
  if err := got.Err(); err != nil {
   return fmt.Errorf("audit: iterate resource change audits: %w", err)
  }
  if len(rows) == 0 {
   return nil
  }
  names, err := loadActorNames(ctx, tx, actorIDs)
  if err != nil {
   return err
  }
  for i := range rows {
   rows[i].ActorName = actorDisplayName(rows[i].ActorID, names)
  }
  return nil
 })
 if err != nil {
  return nil, 0, err
 }
 return rows, total, nil
}

func (r *PgResourceChangeAuditRepo) GetByID(
 ctx context.Context,
 tenantID, id string,
) (*port.ResourceChangeAuditRow, error) {
 if tenantID == "" {
  return nil, fmt.Errorf("audit: get resource change audit: tenant id required")
 }
 var row port.ResourceChangeAuditRow
 err := postgres.ExecTenantWith(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  var before, after []byte
  if err := tx.QueryRow(ctx, `SELECT id, resource_kind, resource_id, operation, actor_id,
   created_at, before_projection, after_projection
   FROM resource_change_audits WHERE tenant_id = $1 AND id = $2`, tenantID, id).
   Scan(&row.ID, &row.ResourceKind, &row.ResourceID, &row.Operation, &row.ActorID,
    &row.CreatedAt, &before, &after); err != nil {
   return err
  }
  row.Before = json.RawMessage(before)
  row.After = json.RawMessage(after)
  names, err := loadActorNames(ctx, tx, []string{row.ActorID})
  if err != nil {
   return err
  }
  row.ActorName = actorDisplayName(row.ActorID, names)
  return nil
 })
 if errors.Is(err, pgx.ErrNoRows) {
  return nil, nil
 }
 if err != nil {
  return nil, fmt.Errorf("audit: get resource change audit: %w", err)
 }
 return &row, nil
}

// buildChangeAuditWhere 构造带占位符的 WHERE。tenant_id 谓词恒存在（$1）。
// actor_name 子串匹配：命中 public.users.display_name / github_login，或
// actor_id 原文（覆盖 system actor 如 evaluation-worker）。返回的 where 以
// "WHERE " 开头，可直接拼接在表名后；表别名恒为 r（子查询引用 r.actor_id）。
func buildChangeAuditWhere(tenantID string, f port.ResourceChangeAuditFilter) (string, []any) {
 conds := []string{`tenant_id = $1`}
 args := []any{tenantID}
 if f.ResourceKind != "" {
  args = append(args, f.ResourceKind)
  conds = append(conds, fmt.Sprintf(`resource_kind = $%d`, len(args)))
 }
 if f.ActorName != "" {
  args = append(args, `%`+f.ActorName+`%`)
  idx := len(args)
  conds = append(conds, fmt.Sprintf(
   `(EXISTS (SELECT 1 FROM public.users u WHERE u.id = r.actor_id AND (u.display_name ILIKE $%[1]d OR u.github_login ILIKE $%[1]d)) OR r.actor_id ILIKE $%[1]d)`, idx))
 }
 if f.From != nil {
  args = append(args, *f.From)
  conds = append(conds, fmt.Sprintf(`created_at >= $%d`, len(args)))
 }
 if f.To != nil {
  args = append(args, *f.To)
  conds = append(conds, fmt.Sprintf(`created_at <= $%d`, len(args)))
 }
 return `WHERE ` + strings.Join(conds, ` AND `), args
}

// actorNameRow 是 public.users 批量映射的中间载体。
type actorNameRow struct {
 DisplayName string
 GitHubLogin string
}

// loadActorNames 批量取分页行 actor 的 display_name/github_login。
// schema-qualified public.users（execTenant 内 search_path 含 public，显式限定
// 防未来 shadow）；只取两列，不返回 email。
func loadActorNames(
 ctx context.Context,
 tx pgx.Tx,
 actorIDs []string,
) (map[string]actorNameRow, error) {
 names := make(map[string]actorNameRow, len(actorIDs))
 rows, err := tx.Query(ctx, `SELECT id, COALESCE(display_name,''), COALESCE(github_login,'')
  FROM public.users WHERE id = ANY($1)`, actorIDs)
 if err != nil {
  return nil, fmt.Errorf("audit: load actor names: %w", err)
 }
 defer rows.Close()
 for rows.Next() {
  var id string
  var n actorNameRow
  if err := rows.Scan(&id, &n.DisplayName, &n.GitHubLogin); err != nil {
   return nil, fmt.Errorf("audit: scan actor name: %w", err)
  }
  names[id] = n
 }
 if err := rows.Err(); err != nil {
  return nil, fmt.Errorf("audit: iterate actor names: %w", err)
 }
 return names, nil
}

// actorDisplayName 按 display_name > github_login > actor_id 原文兜底。
// system actor（evaluation-worker 等）无对应 users 行，直接展示 actor_id。
func actorDisplayName(actorID string, names map[string]actorNameRow) string {
 n, ok := names[actorID]
 if !ok {
  return actorID
 }
 if n.DisplayName != "" {
  return n.DisplayName
 }
 if n.GitHubLogin != "" {
  return n.GitHubLogin
 }
 return actorID
}
```

- [ ] **Step 3: Write the failing tests**

`internal/audit/infrastructure/persistence/change_audit_repo_test.go`:

```go
package persistence

import (
 "context"
 "regexp"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/audit/domain/port"
 "github.com/pashagolub/pgxmock/v2"
 "github.com/stretchr/testify/require"
)

func newChangeAuditMock(t *testing.T) pgxmock.PgxPoolIface {
 t.Helper()
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 t.Cleanup(mock.Close)
 return mock
}

func withTenantExpects(mock pgxmock.PgxPoolIface) {
 mock.ExpectBegin()
 mock.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_abc-1", public`)).
  WillReturnResult(pgxmock.NewResult("SET", 0))
}

// TestPgResourceChangeAuditRepo_EmptyTenantFailsClosed 空 tenantID 在触碰连接池
// 之前 fail closed（nil pool 可安全传入，因为提前 return）。
func TestPgResourceChangeAuditRepo_EmptyTenantFailsClosed(t *testing.T) {
 repo := &PgResourceChangeAuditRepo{} // 注意：nil pool，仅验证提前 return
 _, _, err := repo.List(context.Background(), "", port.ResourceChangeAuditFilter{})
 require.Error(t, err)
 require.Contains(t, err.Error(), "tenant id required")

 got, err := repo.GetByID(context.Background(), "", "id")
 require.Error(t, err)
 require.Nil(t, got)
}

// TestBuildChangeAuditWhere 锁定 WHERE 构造：tenant_id 谓词恒存在、actor_name
// 走 public.users 子串匹配、时间范围带占位符。
func TestBuildChangeAuditWhere(t *testing.T) {
 from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
 to := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
 where, args := buildChangeAuditWhere("tenant-1", port.ResourceChangeAuditFilter{
  ResourceKind: "workflow",
  ActorName:    "li",
  From:         &from,
  To:           &to,
 })
 require.Contains(t, where, `tenant_id = $1`)
 require.Contains(t, where, `resource_kind = $2`)
 require.Contains(t, where, `public.users`)
 require.Contains(t, where, `created_at >= $`)
 require.Contains(t, where, `created_at <= $`)
 require.Len(t, args, 5)

 whereOnly, argsOnly := buildChangeAuditWhere("tenant-1", port.ResourceChangeAuditFilter{})
 require.Equal(t, `WHERE tenant_id = $1`, whereOnly)
 require.Equal(t, []any{"tenant-1"}, argsOnly)
}

// TestPgResourceChangeAuditRepo_List_NoFilter 全链路（count+list+users 映射）
// 走 execTenant 事务。
func TestPgResourceChangeAuditRepo_List_NoFilter(t *testing.T) {
 mock := newChangeAuditMock(t)
 withTenantExpects(mock)

 created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
 mock.ExpectQuery(`SELECT COUNT\(\*\) FROM resource_change_audits`).
  WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
 mock.ExpectQuery(`SELECT id, resource_kind, resource_id`).
  WillReturnRows(pgxmock.NewRows([]string{
   "id", "resource_kind", "resource_id", "operation", "actor_id", "created_at",
   "before_projection", "after_projection",
  }).
   AddRow("a1", "workflow", "wf-1", "publish", "u-1", created, `{}`, `{"id":"wf-1"}`).
   AddRow("a2", "agent", "ag-1", "create", "worker", created, `{}`, `{}`))
 mock.ExpectQuery(`SELECT id, COALESCE\(display_name`).
  WillReturnRows(pgxmock.NewRows([]string{"id", "display_name", "github_login"}).
   AddRow("u-1", "李雷", "lilei"))
 mock.ExpectCommit()

 repo := NewPgResourceChangeAuditRepo(mock)
 rows, total, err := repo.List(context.Background(), "tenant_abc-1", port.ResourceChangeAuditFilter{Limit: 20})
 require.NoError(t, err)
 require.Equal(t, 2, total)
 require.Len(t, rows, 2)
 require.Equal(t, "李雷", rows[0].ActorName) // display_name 命中
 require.Equal(t, "worker", rows[1].ActorName) // 无 users 行 → actor_id 兜底
 require.NoError(t, mock.ExpectationsWereMet())
}

// TestPgResourceChangeAuditRepo_List_Empty skips SELECT 当 total==0。
func TestPgResourceChangeAuditRepo_List_Empty(t *testing.T) {
 mock := newChangeAuditMock(t)
 withTenantExpects(mock)
 mock.ExpectQuery(`SELECT COUNT\(\*\) FROM resource_change_audits`).
  WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
 mock.ExpectCommit()

 repo := NewPgResourceChangeAuditRepo(mock)
 rows, total, err := repo.List(context.Background(), "tenant_abc-1", port.ResourceChangeAuditFilter{})
 require.NoError(t, err)
 require.Equal(t, 0, total)
 require.Empty(t, rows)
 require.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./internal/audit/infrastructure/persistence/ -run 'TestPgResourceChangeAuditRepo|TestBuildChangeAuditWhere' -v`
Expected: FAIL，编译错误 `undefined: port.ResourceChangeAuditQuery`（port 未建）。

> 注：Step 1 已在前面创建 port 文件，故此处应能编译；若你按顺序执行，Step 4 之前已运行 Step 1，则预期是 `pgxmock` 匹配失败或函数未定义。以「测试未全绿」为准。

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/audit/infrastructure/persistence/ -run 'TestPgResourceChangeAuditRepo|TestBuildChangeAuditWhere' -v`
Expected: PASS（3 个用例）。

- [ ] **Step 6: Full vet + short tests**

Run: `go vet ./internal/audit/... && go test -short ./...`
Expected: 全绿（旧 HTTP 审计测试不受影响）。

- [ ] **Step 7: Commit**

```bash
git add internal/audit/domain/port/change_audit.go internal/audit/infrastructure/persistence/change_audit_repo.go internal/audit/infrastructure/persistence/change_audit_repo_test.go
git commit -m "feat(audit): 新增资源变更审计查询 port 与 tenant 隔离 repo"
```

---

## Task 3: proto 契约 + handler 重写 + wiring + 路由权限

**Files:**

- Create: `proto/audit/audit.proto`
- Run: `make proto-gen`
- Rewrite: `api/http/handler/audit_handler.go`
- Rewrite: `api/wiring/audit.go`
- Modify: `api/http/router.go`（registerAudit 权限 + 移除 AuditMiddleware 挂载）
- Modify: `cmd/server/runtime.go`（移除 registerAuditCleanup）
- Modify: `api/http/contract_test.go`（wiring + contractAuditRepo 重写）
- Modify: `scripts/record-contracts.go`（同款接线同步）
- Rewrite: `api/http/handler/audit_handler_test.go`
- Test: `api/http/testdata/contracts/*.golden.json`（新增 audit 契约 golden）

**Interfaces:**

- Consumes: Task 2 的 `port.ResourceChangeAuditQuery`/`ResourceChangeAuditRow`/`ResourceChangeAuditFilter`、`persistence.NewPgResourceChangeAuditRepo`。
- Produces: `wiring.Audit{QueryService auditport.ResourceChangeAuditQuery}`；`handler.NewAuditHandler(query auditport.ResourceChangeAuditQuery, logger)`；HTTP `GET /audit/events`、`GET /audit/events/:id`（`RequireTenantRole("admin")`）。Task 4-7 依赖该路由与 DTO。

- [ ] **Step 1: Write the proto contract**

`proto/audit/audit.proto`:

```proto
syntax = "proto3";

package stratum.audit;

import "google/protobuf/struct.proto";

message ResourceChangeAudit {
  string id = 1;
  string resource_kind = 2; // agent|skill|mcp|knowledge|workflow|evaluation
  string resource_id = 3;
  string operation = 4;     // create|update|delete|publish|promote|rollback|reject|pause|activate
  string actor_id = 5;
  string actor_name = 6;    // display_name > github_login > actor_id 兜底
  string created_at = 7;    // RFC3339
  google.protobuf.Struct before = 8;
  google.protobuf.Struct after = 9;
}

message ListResourceChangeAuditsRequest {
  string resource_kind = 1;
  string actor_name = 2;
  string from = 3; // RFC3339
  string to = 4;   // RFC3339
  int32 page = 5;
  int32 page_size = 6;
}

message ListResourceChangeAuditsResponse {
  repeated ResourceChangeAudit events = 1;
  int32 total = 2;
}
```

- [ ] **Step 2: Generate DTOs and record the actual generated surface**

Run: `make proto-gen`
Expected: 生成 `api/http/dto/gen/audit.go` 与 `web/src/services/gen/audit.ts`（gitignored）。

然后读 `api/http/dto/gen/audit.go`，记录三件事（后续 handler 代码按此适配）：

1. 请求/响应消息的 Go 结构体字段名（预期 `ResourceKind`/`ActorName`/`CreatedAt`/`Before`/`After`，camelCase）；
2. `Before`/`After` 的 Go 类型（预期 `map[string]any` 或 `*structpb.Struct`）；
3. 是否生成了 `ToResourceChangeAudit(...)` 之类的 mapper。
写在这一步的注释里，供 Step 4 使用。

- [ ] **Step 3: Write the failing handler test**

`api/http/handler/audit_handler_test.go`（全量重写）：

```go
package handler

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"

 auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
 "github.com/byteBuilderX/stratum/pkg/reqctx"
 "github.com/gin-gonic/gin"
 "github.com/stretchr/testify/require"
 "go.uber.org/zap"
)

type stubAuditQuery struct {
 rows  []auditport.ResourceChangeAuditRow
 total int
 byID  *auditport.ResourceChangeAuditRow
}

func (s *stubAuditQuery) List(context.Context, string, auditport.ResourceChangeAuditFilter) ([]auditport.ResourceChangeAuditRow, int, error) {
 return s.rows, s.total, nil
}

func (s *stubAuditQuery) GetByID(context.Context, string, string) (*auditport.ResourceChangeAuditRow, error) {
 return s.byID, nil
}

// newAuditTestRouter 注入租户到 request context（tenantIDFromCtx 从 reqctx
// 读取，不是 gin.Context），与 workflow_handler_test.go 的注入模式一致。
func newAuditTestRouter(h *AuditHandler) *gin.Engine {
 r := gin.New()
 r.Use(func(c *gin.Context) {
  c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
  c.Next()
 })
 r.GET("/audit/events", h.ListEvents)
 r.GET("/audit/events/:id", h.GetEvent)
 return r
}

func TestAuditHandler_ListEvents(t *testing.T) {
 h := NewAuditHandler(&stubAuditQuery{
  rows: []auditport.ResourceChangeAuditRow{
   {ID: "a1", ResourceKind: "workflow", ResourceID: "wf-1", Operation: "publish",
    ActorID: "u-1", ActorName: "李雷", CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
    Before: json.RawMessage(`{}`), After: json.RawMessage(`{"id":"wf-1"}`)},
  },
  total: 1,
 }, zap.NewNop())
 r := newAuditTestRouter(h)

 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodGet, "/audit/events?resource_kind=workflow&page=1&page_size=20", nil)
 r.ServeHTTP(w, req)

 require.Equal(t, http.StatusOK, w.Code)
 var body map[string]any
 require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
 require.Equal(t, float64(1), body["total"])
 events := body["events"].([]any)
 first := events[0].(map[string]any)
 require.Equal(t, "李雷", first["actorName"])
 require.Equal(t, "wf-1", first["resourceId"])
}

func TestAuditHandler_GetEvent_NotFound(t *testing.T) {
 h := NewAuditHandler(&stubAuditQuery{}, zap.NewNop())
 r := newAuditTestRouter(h)
 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodGet, "/audit/events/missing", nil)
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusNotFound, w.Code)
}
```

- [ ] **Step 4: Run handler test to verify it fails**

Run: `go test ./api/http/handler/ -run TestAuditHandler -v`
Expected: FAIL，编译错误（`NewAuditHandler` 参数类型不匹配 / `gen.ResourceChangeAudit` 未定义 / `ContextKeyTenantID` 名字不符）。

> 适配：若生成 DTO 字段名与 Step 2 记录不同，按记录调整。tenant 注入必须走 `reqctx.WithTenantID`（`tenantIDFromCtx` 读 request context）；user 相关测试需补 `c.Set(middleware.ContextKeySub, "user-1")`。

- [ ] **Step 5: Rewrite the handler**

`api/http/handler/audit_handler.go`（全量重写）：

```go
package handler

import (
 "encoding/json"
 "errors"
 "net/http"
 "strconv"
 "time"

 "github.com/byteBuilderX/stratum/api/http/dto/gen"
 "github.com/byteBuilderX/stratum/api/middleware"
 auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

// AuditHandler 提供租户资源变更审计的读取接口。租户取自 tenantIDFromCtx
// （空字符串返回 false），不再使用宽容版 tenantIDFromGinKey。
type AuditHandler struct {
 query  auditport.ResourceChangeAuditQuery
 logger *zap.Logger
}

func NewAuditHandler(query auditport.ResourceChangeAuditQuery, logger *zap.Logger) *AuditHandler {
 return &AuditHandler{query: query, logger: logger}
}

// ListEvents 返回当前租户的资源变更审计分页列表。
func (h *AuditHandler) ListEvents(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
 pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 32)
 if page < 1 {
  page = 1
 }
 if pageSize < 1 || pageSize > 100 {
  pageSize = 20
 }
 filter := auditport.ResourceChangeAuditFilter{
  ResourceKind: c.Query("resource_kind"),
  ActorName:    c.Query("actor_name"),
  Limit:        int(pageSize),
  Offset:       int((page - 1) * pageSize),
 }
 filter = applyAuditTimeRange(filter, c.Query("from"), c.Query("to"))

 rows, total, err := h.query.List(c.Request.Context(), tenantID, filter)
 if err != nil {
  h.logger.Error("audit: list resource change audits failed", zap.Error(err))
  _ = c.Error(err)
  return
 }
 events := make([]gen.ResourceChangeAudit, 0, len(rows))
 for _, row := range rows {
  events = append(events, toResourceChangeAuditDTO(row))
 }
 c.JSON(http.StatusOK, gin.H{"events": events, "total": total})
}

// GetEvent 返回单条资源变更审计详情。
func (h *AuditHandler) GetEvent(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 row, err := h.query.GetByID(c.Request.Context(), tenantID, c.Param("id"))
 if err != nil {
  _ = c.Error(err)
  return
 }
 if row == nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusNotFound, errors.New("audit event not found")))
  return
 }
 c.JSON(http.StatusOK, toResourceChangeAuditDTO(*row))
}

// applyAuditTimeRange 把 RFC3339 字符串解析进筛选范围；非法值静默忽略
// （与现有行为数字约束一致，页面端 RangePicker 已保证合法格式）。
func applyAuditTimeRange(filter auditport.ResourceChangeAuditFilter, from, to string) auditport.ResourceChangeAuditFilter {
 if from != "" {
  if parsed, err := time.Parse(time.RFC3339, from); err == nil {
   filter.From = &parsed
  }
 }
 if to != "" {
  if parsed, err := time.Parse(time.RFC3339, to); err == nil {
   filter.To = &parsed
  }
 }
 return filter
}

// toResourceChangeAuditDTO 把查询行映射为契约 DTO。before/after 为 JSONB
// 投影，解到 map 后赋值；字段名以 make proto-gen 生成物为准（Step 2）。
func toResourceChangeAuditDTO(row auditport.ResourceChangeAuditRow) gen.ResourceChangeAudit {
 dto := gen.ResourceChangeAudit{
  Id:           row.ID,
  ResourceKind: row.ResourceKind,
  ResourceId:   row.ResourceID,
  Operation:    row.Operation,
  ActorId:      row.ActorID,
  ActorName:    row.ActorName,
  CreatedAt:    row.CreatedAt.Format(time.RFC3339),
 }
 if len(row.Before) > 0 {
  var obj map[string]any
  if err := json.Unmarshal(row.Before, &obj); err == nil {
   dto.Before = obj
  }
 }
 if len(row.After) > 0 {
  var obj map[string]any
  if err := json.Unmarshal(row.After, &obj); err == nil {
   dto.After = obj
  }
 }
 return dto
}
```

> codegen 适配：若 Step 2 显示 `gen.ResourceChangeAudit.Before/After` 是 `*structpb.Struct`，把两处赋值改为 `if s, err := structpb.NewStruct(obj); err == nil { dto.Before = s }`，并补 import `"google.golang.org/protobuf/types/known/structpb"`。若生成物自带 `ToResourceChangeAudit(...)` mapper，直接改用之、删除本函数。

- [ ] **Step 6: Run handler test to verify it passes**

Run: `go test ./api/http/handler/ -run TestAuditHandler -v`
Expected: PASS。

- [ ] **Step 7: Rewrite wiring**

`api/wiring/audit.go`（全量重写）：

```go
package wiring

import (
 auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
 "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
 "github.com/jackc/pgx/v5/pgxpool"
)

// Audit 承载资源变更审计的查询服务（平台级 HTTP 审计已废弃，见 spec F）。
type Audit struct {
 QueryService auditport.ResourceChangeAuditQuery
}

func buildAudit(db *pgxpool.Pool) *Audit {
 if db == nil {
  return nil
 }
 return &Audit{
  QueryService: persistence.NewPgResourceChangeAuditRepo(db),
 }
}
```

然后更新 `buildAudit` 调用点（grep `buildAudit(` 于 `api/wiring/`）：原签名 `buildAudit(db, logger)` → `buildAudit(db)`，删除 logger 实参。

- [ ] **Step 8: Update router**

`api/http/router.go`：

1. 移除 AuditMiddleware 挂载（约 L37-39）：

```go
// 删除以下整块（含 if c.Audit != nil && c.Audit.Recorder != nil 判断）
//   r.Use(middleware.AuditMiddleware(...))  // 以实际代码为准
```

删除后 `c.Audit.Recorder` 引用消失（wiring 已无该字段）。

1. `registerAudit`（约 L576-586）权限改为租户 admin：

```go
func registerAudit(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
 if c.Audit == nil || c.Audit.QueryService == nil {
  return
 }
 h := handler.NewAuditHandler(c.Audit.QueryService, c.Logger)
 // 审计日志:租户内 admin/owner 可见(owner 经 admin gate 通过)。
 auditGroup := r.Group("/audit", protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"))...)
 auditGroup.Use(requireActive)
 auditGroup.GET("/events", h.ListEvents)
 auditGroup.GET("/events/:id", h.GetEvent)
}
```

> 适配：确认 `middleware.RequireTenantRole("admin")` 存在（`/approvals` 等路由已在用）；`protectedTenantMiddleware` 的用法保持现状。

1. 更新 `handler.NewAuditHandler(c.Audit.QueryService, c.Logger)` 若编译器提示类型不符，确认 wiring.Audit.QueryService 已改为新接口。

- [ ] **Step 9: Remove audit cleanup worker wiring from runtime**

`cmd/server/runtime.go`：

- 删除 L124 的 `registerAuditCleanup(appHarness, c, logger)` 调用行；
- 删除 L187-191 的 `registerAuditCleanup` 函数整体。

> 适配：以 grep `registerAuditCleanup` 的实际行号为准；删除后若 `appHarness` 参数在所在函数内不再使用导致未用参数，确认该函数其它调用仍在用（否则保留一个 `_ = appHarness` 或调整）。`wiring.AuditCleanupWorker`/常量本任务暂不删（Task 6 删）。

- [ ] **Step 10: Sync contract test wiring + stub**

`api/http/contract_test.go`：

1. L154-156 wiring 改为（去掉 `Recorder` 字段与 `auditapp` 构造）：

```go
  Audit: &wiring.Audit{
   QueryService: contractAuditRepo{},
  },
```

1. 删除 import `auditapp "github.com/byteBuilderX/stratum/internal/audit/application"`（若不再被引用）。
2. 重写 `contractAuditRepo`（约 L633-650）：删除旧 `InsertBatch`/`Query`/`Count`/`DeleteOlderThan` 与旧签名 `GetByID`，改为实现新 `auditport.ResourceChangeAuditQuery`：

```go
// ── Audit stub ─────────────────────────────────────────────────────────────
type contractAuditRepo struct{}

func (contractAuditRepo) List(_ context.Context, _ string, _ auditport.ResourceChangeAuditFilter) ([]auditport.ResourceChangeAuditRow, int, error) {
 return nil, 0, nil
}

func (contractAuditRepo) GetByID(_ context.Context, _, _ string) (*auditport.ResourceChangeAuditRow, error) {
 return nil, nil
}
```

> 适配：新增 import `auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"`（若旧代码用了别的别名，检查重名）。

1. 新增契约用例：参照本文件既有列表用例结构（如 agents/`workflows` 用例），为 `GET /audit/events` 与 `GET /audit/events/:id` 各加一个 tenant-admin 用例，断言响应 JSON 与筛选参数透传。用例落在既有 case 表中（`Audit` wiring 已挂在 Container）。

- [ ] **Step 11: Sync record-contracts**

`scripts/record-contracts.go`：同样把 audit 旧接线改为 `wiring.Audit{QueryService: contractAuditRepo{}}`，删除 `auditapp` 依赖；`contractAuditRepo` 若在脚本里有同名 stub，同样重写为新接口。grep `contractAuditRepo` 与 `Audit.Recorder` 在 `scripts/` 下定位全部点。

- [ ] **Step 12: Regenerate contract goldens**

Run: `go run scripts/record-contracts.go`
Expected: 生成/更新 `api/http/testdata/contracts/*.golden.json`，出现新的 audit golden 文件。

Run: `go test ./api/http/ -run Contract -count=1`
Expected: PASS。

- [ ] **Step 13: Full verification**

Run: `go vet ./... && go test -short ./...`
Expected: 全绿。确认 `grep -rn "Audit.Recorder\|tenantIDFromGinKey" api/ cmd/ scripts/` 已无残留（除 Task 6 将删的旧文件）。

- [ ] **Step 14: Commit**

```bash
git add proto/audit/audit.proto api/http/handler/audit_handler.go api/http/handler/audit_handler_test.go api/wiring/audit.go api/http/router.go cmd/server/runtime.go api/http/contract_test.go scripts/record-contracts.go api/http/testdata/contracts/
git commit -m "feat(audit): 资源变更审计读取 API(proto+handler+租户admin路由)+废弃平台审计接线"
```

---

## Task 4: workflow 写端 — actor 级联 + 审计写入

**Files:**

- Modify: `internal/workflow/domain/port/repositories.go`
- Modify: `internal/workflow/application/service.go`
- Create: `internal/workflow/application/change_audit.go`
- Modify: `internal/workflow/infrastructure/persistence/store.go`
- Create: `internal/workflow/infrastructure/persistence/change_audit.go`
- Modify: `api/http/handler/workflow_handler.go`
- Modify: 全部 workflow test mock 与 contract stub

**Interfaces:**

- Consumes: Task 1 常量（`ResourceKindWorkflow`、`ChangeOpPublish` 等）；`auditdomain.ChangeAuditInsertSQL`。
- Produces:
  - `DefinitionRepository.CreateDefinition(ctx, tenantID, *domain.Definition, *auditdomain.ResourceChangeAuditEvent) error`
  - `DefinitionRepository.UpdateDefinition(ctx, tenantID, *domain.Definition, int64, *auditdomain.ResourceChangeAuditEvent) error`
  - `DefinitionRepository.DeleteDefinition(ctx, tenantID, string, *auditdomain.ResourceChangeAuditEvent) error`
  - `VersionRepository.CreateVersion(ctx, tenantID, *domain.Version, *auditdomain.ResourceChangeAuditEvent) error`
  - `AtomicVersionPublisher.CreateNextVersion(ctx, tenantID, *domain.Definition, string, *auditdomain.ResourceChangeAuditEvent) (*domain.Version, error)`
  - `DefinitionService.Create(ctx, tenantID, CreateDefinitionCommand, actorID string) (*domain.Definition, error)`（Update/Delete/Publish 同加 `actorID string`）

- [ ] **Step 1: Update the port interfaces**

`internal/workflow/domain/port/repositories.go`：为四个写入方法追加尾参 `ev *auditdomain.ResourceChangeAuditEvent`。新增 import `auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"`。

```go
type DefinitionRepository interface {
 CreateDefinition(context.Context, string, *domain.Definition, *auditdomain.ResourceChangeAuditEvent) error
 GetDefinition(context.Context, string, string) (*domain.Definition, error)
 UpdateDefinition(context.Context, string, *domain.Definition, int64, *auditdomain.ResourceChangeAuditEvent) error
 DeleteDefinition(context.Context, string, string, *auditdomain.ResourceChangeAuditEvent) error
}
```

`VersionRepository.CreateVersion` 与 `AtomicVersionPublisher.CreateNextVersion` 同样追加尾参（见 Interfaces 块签名）。

- [ ] **Step 2: Write the failing service test**

在 `internal/workflow/application/service_test.go` 的既有 `memoryStore` mock 上，先改四个方法签名以通过编译，并新增一个审计断言用例。先加用例（调用 service 需要新签名）：

```go
func TestDefinitionService_Create_WritesChangeAudit(t *testing.T) {
 store := newMemoryStore()
 svc := NewDefinitionService(store, store, func() string { return "def-1" })
 created, err := svc.Create(context.Background(), "tenant-1", CreateDefinitionCommand{
  Name: "n", Description: "d", Spec: domain.Spec{Nodes: []domain.Node{}},
 }, "u-1")
 require.NoError(t, err)
 require.Equal(t, "def-1", created.ID)
 require.Len(t, store.auditEvents, 1)
 ev := store.auditEvents[0]
 require.Equal(t, auditdomain.ResourceKindWorkflow, ev.ResourceKind)
 require.Equal(t, "def-1", ev.ResourceID)
 require.Equal(t, auditdomain.ChangeOpCreate, ev.Operation)
 require.Equal(t, "u-1", ev.ActorID)
 require.JSONEq(t, `{"id":"def-1","name":"n","description":"d"}`, string(ev.After))
}
```

> 适配：`domain.Spec{Nodes: ...}` 的零值构造以 `internal/workflow/domain` 现有 spec 结构为准；若 `NewDefinition` 对空 Spec 报错，改用测试内已有的合法 spec 构造（参考 `service_test.go` 既有用例）。`memoryStore` 需加字段 `auditEvents []*auditdomain.ResourceChangeAuditEvent` 并在四个写入方法里 `s.auditEvents = append(s.auditEvents, ev)`（nil 也追加，便于断言）。

- [ ] **Step 3: Run service test to verify it fails**

Run: `go test ./internal/workflow/application/ -run TestDefinitionService_Create_WritesChangeAudit`
Expected: FAIL，编译错误（`Create` 缺 actorID 参数 / `memoryStore.CreateDefinition` 签名不匹配）。

- [ ] **Step 4: Add the application change-audit helper**

`internal/workflow/application/change_audit.go`:

```go
package application

import (
 "encoding/json"
 "fmt"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/workflow/domain"
)

// workflowSafeProjection 白名单投影：仅核心元数据，禁止 Spec/InputSchema/节点配置
//（其中可嵌第三方密钥）。
func workflowSafeProjection(d *domain.Definition) map[string]any {
 return map[string]any{"id": d.ID, "name": d.Name, "description": d.Description}
}

// newWorkflowChangeAudit 构造 workflow 资源变更审计事件。actorID 来自 handler
// 认证上下文（auth.sub），禁止从 request body 读取。
func newWorkflowChangeAudit(resourceID, op, actorID string, before, after map[string]any) (*auditdomain.ResourceChangeAuditEvent, error) {
 ev := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindWorkflow,
  ResourceID:   resourceID,
  Operation:    op,
  ActorID:      actorID,
  ActorType:    auditdomain.ChangeActorUser,
  Source:       auditdomain.ChangeSourceAPI,
 }
 var err error
 if before != nil {
  ev.Before, err = json.Marshal(before)
  if err != nil {
   return nil, fmt.Errorf("change audit: marshal workflow before: %w", err)
  }
 }
 if after != nil {
  ev.After, err = json.Marshal(after)
  if err != nil {
   return nil, fmt.Errorf("change audit: marshal workflow after: %w", err)
  }
 }
 return ev, nil
}
```

- [ ] **Step 5: Update the service**

`internal/workflow/application/service.go`：四方法加 `actorID string` 尾参并在业务写前构造审计事件。

```go
func (s *DefinitionService) Create(ctx context.Context, tenantID string, cmd CreateDefinitionCommand, actorID string) (*domain.Definition, error) {
 definition, err := domain.NewDefinition(s.newID(), cmd.Name, cmd.Description, cmd.Spec, normalizeInputSchema(cmd.InputSchema))
 if err != nil {
  return nil, err
 }
 if err := s.validateSkillRefs(cmd.Spec); err != nil {
  return nil, err
 }
 ev, err := newWorkflowChangeAudit(definition.ID, auditdomain.ChangeOpCreate, actorID, nil, workflowSafeProjection(definition))
 if err != nil {
  return nil, err
 }
 if err := s.definitions.CreateDefinition(ctx, tenantID, definition, ev); err != nil {
  return nil, err
 }
 return definition, nil
}

func (s *DefinitionService) Update(ctx context.Context, tenantID, id string, cmd UpdateDefinitionCommand, actorID string) (*domain.Definition, error) {
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 before := workflowSafeProjection(definition)
 if err := definition.UpdateDraft(cmd.Name, cmd.Description, cmd.Spec, cmd.ExpectedRevision, normalizeInputSchema(cmd.InputSchema)); err != nil {
  return nil, err
 }
 if err := s.validateSkillRefs(cmd.Spec); err != nil {
  return nil, err
 }
 ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpUpdate, actorID, before, workflowSafeProjection(definition))
 if err != nil {
  return nil, err
 }
 if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision, ev); err != nil {
  return nil, err
 }
 return definition, nil
}

func (s *DefinitionService) Delete(ctx context.Context, tenantID, id string, actorID string) error {
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return err
 }
 ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpDelete, actorID, workflowSafeProjection(definition), nil)
 if err != nil {
  return err
 }
 return s.definitions.DeleteDefinition(ctx, tenantID, id, ev)
}

func (s *DefinitionService) Publish(ctx context.Context, tenantID, id string, actorID string) (*domain.Version, error) {
 definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 projection := workflowSafeProjection(definition)
 ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpPublish, actorID, projection, projection)
 if err != nil {
  return nil, err
 }
 if publisher, ok := s.versions.(port.AtomicVersionPublisher); ok {
  return publisher.CreateNextVersion(ctx, tenantID, definition, s.newID(), ev)
 }
 number, err := s.versions.NextVersionNumber(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 version, err := definition.Publish(s.newID(), number)
 if err != nil {
  return nil, err
 }
 if err := s.versions.CreateVersion(ctx, tenantID, version, ev); err != nil {
  return nil, err
 }
 return version, nil
}
```

> 注意：`service_test.go` 的 `memoryStore` **未实现** `AtomicVersionPublisher`，故 Publish 测试走 fallback `CreateVersion` 路径（已补审计参数）。

- [ ] **Step 6: Update the repo implementations**

`internal/workflow/infrastructure/persistence/store.go`：四方法各追加 `ev *auditdomain.ResourceChangeAuditEvent` 尾参，在各自 `s.exec` 事务闭包末尾调用 `insertChangeAudit(ctx, tx, ev)`。

- `CreateDefinition`：INSERT 之后 `if err := insertChangeAudit(ctx, tx, ev); err != nil { return err }`
- `UpdateDefinition`：UPDATE 之后同样
- `DeleteDefinition`：`RowsAffected()!=1` 检查通过后、`return nil` 之前
- `CreateVersion`：INSERT 之后
- `CreateNextVersion`：版本 INSERT 之后（在该事务闭包内）

> 适配：以 grep `func (s *PgStore)` 定位各方法体；`DeleteDefinition` 现实现位于 store.go L149-160。

- [ ] **Step 7: Add the persistence change-audit helper**

`internal/workflow/infrastructure/persistence/change_audit.go`（逐字复制 agent repo 同构实现，import 改为本包路径）：

```go
package persistence

import (
 "context"
 "fmt"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/pkg/tenantdb"
 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"
)

// insertChangeAudit 在业务事务内写一条变更审计；nil 事件跳过。租户取自
// tenant 上下文（execTenant 注入）；缺失为调用方 bug，fail transaction closed。
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
 ev = ev.Normalized()
 if ev == nil {
  return nil
 }
 if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
  return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
   ev.ResourceKind, ev.ResourceID, ev.Operation)
 }
 tc, ok := tenantdb.FromContext(ctx)
 if !ok || tc.TenantID == "" {
  return fmt.Errorf("change audit: missing tenant context")
 }
 _, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
  uuid.Must(uuid.NewV7()).String(), tc.TenantID,
  ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
  ev.ProposalID, ev.Before, ev.After)
 if err != nil {
  return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
 }
 return nil
}
```

- [ ] **Step 8: Update handler call sites**

`api/http/handler/workflow_handler.go`：`workflowActor(c)` 签名 `(workflowapp.Actor, bool)`（L343-354，已确认）。四方法各加 actor 并传入（失败路径沿用既有 handler 的 `NewHTTPError` 模式，见 L220-224）：

```go
// CreateDefinition 内:
 actor, ok := workflowActor(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, fmt.Errorf("authenticated actor required")))
  return
 }
 definition, err := h.definitions.Create(c.Request.Context(), tenantID, workflowapp.CreateDefinitionCommand{Name: req.Name, Description: req.Description, Spec: req.Spec, InputSchema: req.InputSchema}, actor.UserID)
```

- `UpdateDefinition`：`..., actor.UserID)`（service 方法 Update）
- `DeleteDefinition`：`..., actor.UserID)`
- `PublishDefinition`：`..., actor.UserID)`

> 适配：`middleware`/`fmt`/`net/http` 均已由 `workflow_handler.go` 现有代码 import（L222 同款 `NewHTTPError` 调用），无需新增。

- [ ] **Step 9: Sync all workflow mocks and contract stubs**

grep 全部实现这四个接口的 mock/stub，追加尾参（实现体忽略或追加到 `auditEvents`）：

```bash
grep -rn "func (.*) CreateDefinition\|func (.*) UpdateDefinition\|func (.*) DeleteDefinition\|func (.*) CreateVersion\|func (.*) CreateNextVersion" internal/workflow api/http/contract_test.go scripts/record-contracts.go --include='*.go'
```

已知清单（以 grep 结果为准）：

- `internal/workflow/application/service_test.go` 的 `memoryStore`（CreateDefinition/UpdateDefinition/DeleteDefinition/CreateVersion + 加 `auditEvents` 字段）
- `api/http/contract_test.go` 的 `contractDefRepo`（L301/313/316）与 `contractVersionRepo`（L323）
- `scripts/record-contracts.go` 的 `contractDefRepo`/`contractVerRepo`（L449/461/464/471）

若 grep 还命中 `service_integration_test.go`/`store_mock_test.go` 等文件内的实现，同样追加尾参。Go 编译器会逐个点名漏改处——`go test ./internal/workflow/...` 是机械驱动。

- [ ] **Step 10: Run all workflow tests**

Run: `go test ./internal/workflow/... -count=1`
Expected: PASS（含新增审计断言用例）。

- [ ] **Step 11: Run contract tests + vet + short**

Run: `go vet ./internal/workflow/... ./api/http/... && go test -short ./...`
Expected: 全绿。`grep -rn "ResourceKindWorkflow" internal/workflow` 应命中 helper 与测试。

- [ ] **Step 12: Commit**

```bash
git add internal/workflow/domain/port/repositories.go internal/workflow/application/service.go internal/workflow/application/change_audit.go internal/workflow/infrastructure/persistence/store.go internal/workflow/infrastructure/persistence/change_audit.go api/http/handler/workflow_handler.go internal/workflow/application/service_test.go api/http/contract_test.go scripts/record-contracts.go
git commit -m "feat(workflow): 定义 CRUD 与版本发布写入资源变更审计(actor 来自认证上下文)"
```

---

## Task 5: evaluation 写端 — actor 级联 + 审计写入

**Files:**

- Modify: `internal/evaluation/application/experiment_service.go`
- Modify: `internal/evaluation/domain/port/evaluation.go`
- Create: `internal/evaluation/application/change_audit.go`
- Modify: `internal/evaluation/infrastructure/persistence/experiment_repository.go`
- Create: `internal/evaluation/infrastructure/persistence/change_audit.go`
- Modify: `api/http/handler/evaluation_handler.go`
- Modify: 全部 evaluation test mock 与 contract stub

**Interfaces:**

- Consumes: Task 1 常量（`ResourceKindEvaluation`、`ChangeOpCreate/Promote/Rollback/Reject/Pause/Activate`）。
- Produces:
  - `ExperimentRepository.Create(ctx, tenantID, experiment, deployment, ev *auditdomain.ResourceChangeAuditEvent) error`
  - `CreateExperimentInput` 增加 `ActorID string`
  - 审计语义映射：`Create`/`Enqueue`→`create`；`Activate`→`activate`；`Reject`→`reject`；`Pause`→`pause`；`Rollback`→`rollback`；`Promote`→`promote`（改造既有 `promoteChangeAuditTx`）。

- [ ] **Step 1: Update the port**

`internal/evaluation/domain/port/evaluation.go` L193-217：`Create` 追加尾参 `ev *auditdomain.ResourceChangeAuditEvent`：

```go
 Create(ctx context.Context, tenantID string, experiment domain.Experiment, deployment domain.Deployment, ev *auditdomain.ResourceChangeAuditEvent) error
```

新增 import `auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"`。

- [ ] **Step 2: Write the failing service test**

在 `internal/evaluation/application/experiment_service_test.go` 补审计断言用例。先确认该文件的 mock repo 结构（grep `func (.*) Create(`），给 mock 的 `Create` 加 `ev *auditdomain.ResourceChangeAuditEvent` 尾参并记录到 `lastAudit`。用例：

```go
func TestExperimentService_Create_WritesChangeAudit(t *testing.T) {
 mockRepo := newMockExperimentRepository(t) // 以既有 helper 名为准
 svc := NewExperimentService(mockRepo)
 stable := domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "r-1", RevisionID: "rev-s"}
 canary := domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "r-1", RevisionID: "rev-c"}
 _, _, err := svc.Create(context.Background(), "tenant-1", CreateExperimentInput{
  Stable: stable, Canary: canary, SuiteRevisionID: "suite-1", ActorID: "u-1",
 })
 require.NoError(t, err)
 ev := mockRepo.lastAudit
 require.NotNil(t, ev)
 require.Equal(t, auditdomain.ResourceKindEvaluation, ev.ResourceKind)
 require.Equal(t, auditdomain.ChangeOpCreate, ev.Operation)
 require.Equal(t, "u-1", ev.ActorID)
 require.JSONEq(t, `{"resource_kind":"skill","resource_id":"r-1","status":"running"}`, string(ev.After))
}
```

> 适配：`domain.Experiment.Status` 创建时为 `ExperimentRunning`，投影 status 为 `"running"`；若 mock helper 名不同，按既有文件调整。`ValidatePrerequisites` mock 需返回 nil（参考既有 Create 用例的 stub）。

- [ ] **Step 3: Run service test to verify it fails**

Run: `go test ./internal/evaluation/application/ -run TestExperimentService_Create_WritesChangeAudit`
Expected: FAIL，编译错误（`Create` 缺 ev 参数 / `CreateExperimentInput` 无 `ActorID`）。

- [ ] **Step 4: Add the application change-audit helper**

`internal/evaluation/application/change_audit.go`:

```go
package application

import (
 "encoding/json"
 "fmt"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// experimentSafeProjection 白名单投影：仅被测资源 kind/id 与 status 流转，
// 不携带 revision 载荷、评测指标明细。
func experimentSafeProjection(e domain.Experiment) map[string]any {
 return map[string]any{
  "resource_kind": string(e.ResourceKind),
  "resource_id":   e.ResourceID,
  "status":        string(e.Status),
 }
}

// newExperimentChangeAudit 构造评测实验变更审计事件。审计行 resource_kind
// 恒为 "evaluation"、resource_id 为 experiment.ID（评测操作本身是审计对象）。
func newExperimentChangeAudit(e domain.Experiment, op, actorID string, before, after map[string]any) (*auditdomain.ResourceChangeAuditEvent, error) {
 ev := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindEvaluation,
  ResourceID:   e.ID,
  Operation:    op,
  ActorID:      actorID,
  ActorType:    auditdomain.ChangeActorUser,
  Source:       auditdomain.ChangeSourceAPI,
 }
 var err error
 if before != nil {
  ev.Before, err = json.Marshal(before)
  if err != nil {
   return nil, fmt.Errorf("change audit: marshal experiment before: %w", err)
  }
 }
 if after != nil {
  ev.After, err = json.Marshal(after)
  if err != nil {
   return nil, fmt.Errorf("change audit: marshal experiment after: %w", err)
  }
 }
 return ev, nil
}
```

- [ ] **Step 5: Update the service Create/Enqueue**

`internal/evaluation/application/experiment_service.go`：

1. `CreateExperimentInput` 增加字段：

```go
type CreateExperimentInput struct {
 Stable          domain.ResourceRef
 Canary          domain.ResourceRef
 SuiteRevisionID string
 ActorID         string // 来自 handler userIDFromCtx；禁止从 request body 读取
}
```

1. `Create` 在 `s.repo.Create(...)` 调用处构造审计事件：

```go
 ev, err := newExperimentChangeAudit(experiment, auditdomain.ChangeOpCreate, input.ActorID, nil, experimentSafeProjection(experiment))
 if err != nil {
  return domain.Experiment{}, domain.Deployment{}, err
 }
 if err := s.repo.Create(ctx, tenantID, experiment, deployment, ev); err != nil {
  return domain.Experiment{}, domain.Deployment{}, err
 }
```

1. `Enqueue` 同款（op=`create`，after=projection，此时 status=pending）：

```go
 ev, err := newExperimentChangeAudit(experiment, auditdomain.ChangeOpCreate, input.ActorID, nil, experimentSafeProjection(experiment))
 if err != nil {
  return domain.Experiment{}, domain.Deployment{}, err
 }
 if err := s.repo.Create(ctx, tenantID, experiment, deployment, ev); err != nil {
  return domain.Experiment{}, domain.Deployment{}, err
 }
```

- [ ] **Step 6: Add the persistence change-audit helper**

`internal/evaluation/infrastructure/persistence/change_audit.go`：

```go
package persistence

import (
 "context"
 "encoding/json"
 "fmt"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/pkg/tenantdb"
 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"
)

// insertChangeAudit 在业务事务内写一条变更审计；nil 事件跳过。与
// agent/skill/workflow 的 insertChangeAudit 同构（tenant 取自 tenant 上下文）。
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
 ev = ev.Normalized()
 if ev == nil {
  return nil
 }
 if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
  return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
   ev.ResourceKind, ev.ResourceID, ev.Operation)
 }
 tc, ok := tenantdb.FromContext(ctx)
 if !ok || tc.TenantID == "" {
  return fmt.Errorf("change audit: missing tenant context")
 }
 _, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
  uuid.Must(uuid.NewV7()).String(), tc.TenantID,
  ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
  ev.ProposalID, ev.Before, ev.After)
 if err != nil {
  return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
 }
 return nil
}

// commandChangeAuditTx 在 applyCommand 事务内写命令类变更审计（activate/reject/
// pause/rollback；promote 走 promoteChangeAuditTx）。
func commandChangeAuditTx(
 ctx context.Context, tx pgx.Tx, current domain.Experiment, newStatus domain.ExperimentStatus, op, actorID string,
) error {
 before := experimentProjectionTx(current)
 after := experimentProjectionTx(current)
 after["status"] = string(newStatus)
 return insertProjectionAudit(ctx, tx, current.ID, op, actorID, before, after)
}

func experimentProjectionTx(e domain.Experiment) map[string]any {
 return map[string]any{
  "resource_kind": string(e.ResourceKind),
  "resource_id":   e.ResourceID,
  "status":        string(e.Status),
 }
}

func insertProjectionAudit(
 ctx context.Context, tx pgx.Tx, resourceID, op, actorID string, before, after map[string]any,
) error {
 beforeJSON, err := json.Marshal(before)
 if err != nil {
  return fmt.Errorf("change audit: marshal before: %w", err)
 }
 afterJSON, err := json.Marshal(after)
 if err != nil {
  return fmt.Errorf("change audit: marshal after: %w", err)
 }
 return insertChangeAudit(ctx, tx, &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindEvaluation,
  ResourceID:   resourceID,
  Operation:    op,
  ActorID:      actorID,
  ActorType:    auditdomain.ChangeActorUser,
  Source:       auditdomain.ChangeSourceAPI,
  Before:       beforeJSON,
  After:        afterJSON,
 })
}
```

> 适配：若 `domain.ExperimentStatus` 类型名不同（grep `type ExperimentStatus` 确认），改用实际类型。

- [ ] **Step 7: Rewrite promoteChangeAuditTx + cascade**

`internal/evaluation/infrastructure/persistence/experiment_repository.go`：

1. `promoteChangeAuditTx`（L448-466）重写为统一 `evaluation` kind + actor 透传 + 安全投影：

```go
func promoteChangeAuditTx(ctx context.Context, tx pgx.Tx, experiment domain.Experiment, actorID string) error {
 return insertProjectionAudit(ctx, tx, experiment.ID, auditdomain.ChangeOpPromote, actorID,
  experimentProjectionTx(experiment), experimentProjectionTx(experiment))
}
```

1. `promoteCandidateTx`（L468-509）签名加 `actorID string`，末尾调用改为 `promoteChangeAuditTx(ctx, tx, experiment, actorID)`。

2. `ApplyCommand`（L258-302）：Promote 分支调用改为 `promoteCandidateTx(ctx, tx, current, command.ActorID)`；在 `recordExperimentDecisionTx(...)` 之后、`updated = resultSnapshot` 之前插入命令类审计（Promote 已由 promote 路径写入，跳过）：

```go
  if op, ok := experimentAuditOperation(action); ok {
   if err := commandChangeAuditTx(ctx, tx, *current, newStatus, op, command.ActorID); err != nil {
    return err
   }
  }
```

1. 新增 action→op 映射 helper（放同文件私有区）：

```go
// experimentAuditOperation 把命令 action 映射为审计 op；promote 由
// promoteChangeAuditTx 单独写入，此处不返回。
func experimentAuditOperation(action domain.ExperimentCommandAction) (string, bool) {
 switch action {
 case domain.CommandActivate:
  return auditdomain.ChangeOpActivate, true
 case domain.CommandReject:
  return auditdomain.ChangeOpReject, true
 case domain.CommandPause:
  return auditdomain.ChangeOpPause, true
 case domain.CommandRollback:
  return auditdomain.ChangeOpRollback, true
 default:
  return "", false
 }
}
```

> 适配：`domain.CommandActivate/Reject/Pause/Rollback` 常量名以 `internal/evaluation/domain` 为准（service.go 已见 `CommandActivate/Reject/Pause/Promote/Rollback`）。

1. `Create` 方法（grep `func (r *PgExperimentRepository) Create`）：加 `ev *auditdomain.ResourceChangeAuditEvent` 尾参，在 `r.execTenant` 事务闭包内 INSERT 之后调用 `insertChangeAudit(ctx, tx, ev)`。

- [ ] **Step 8: Update the handler Create**

`api/http/handler/evaluation_handler.go` `CreateExperiment`（L465-493）：在 `requireApprovalOrExecute` 闭包内补 `ActorID`。L179 已有 `actor, ok := userIDFromCtx(c)` 先例，按同款写：

```go
 h.requireApprovalOrExecute(c, "evaluation.create_experiment", args, http.StatusCreated, func() (any, error) {
  actorID, _ := userIDFromCtx(c)
  experiment, deployment, err := h.experiments.Create(c.Request.Context(), tenantID, evalapp.CreateExperimentInput{
   Stable: stable, Canary: canary, SuiteRevisionID: req.SuiteRevisionID, ActorID: actorID,
  })
  if err != nil {
   return nil, err
  }
  return gin.H{"experiment": experiment, "deployment": deployment}, nil
 })
```

> 适配：`userIDFromCtx` 返回 `(string, bool)`（tenant.go:20，已确认）；`requireApprovalOrExecute` 闭包外不重复取。

命令路径（Pause/Promote/Rollback 等）已通过 `commandInput(c)`（L580 `userIDFromCtx`）填 `ActorID`，无需改动。

- [ ] **Step 9: Sync all evaluation mocks and contract stubs**

grep 全部实现 `ExperimentRepository.Create` 的 mock/stub，追加 `ev` 尾参：

```bash
grep -rn ") Create(" internal/evaluation api/http/contract_test.go scripts/record-contracts.go --include='*.go' | grep -v "experiment_repository.go"
```

已知清单（以 grep 为准）：`experiment_repository_mock_test.go`、`experiment_command_integration_test.go`、`feedback_repository_integration_test.go`、`contract_test.go` 的 evaluation stub。Go 编译器逐个点名漏改处。

- [ ] **Step 10: Run all evaluation tests**

Run: `go test ./internal/evaluation/... -count=1`
Expected: PASS（含新增审计断言用例）。

- [ ] **Step 11: Contract tests + vet + short**

Run: `go vet ./internal/evaluation/... ./api/http/... && go test -short ./...`
Expected: 全绿。`grep -rn "promoteChangeAuditTx" internal/evaluation` 确认三处调用签名一致。

- [ ] **Step 12: Commit**

```bash
git add internal/evaluation/application/experiment_service.go internal/evaluation/application/change_audit.go internal/evaluation/domain/port/evaluation.go internal/evaluation/infrastructure/persistence/experiment_repository.go internal/evaluation/infrastructure/persistence/change_audit.go api/http/handler/evaluation_handler.go internal/evaluation/application/experiment_service_test.go api/http/contract_test.go scripts/record-contracts.go
git commit -m "feat(evaluation): 实验生命周期写入资源变更审计(create/activate/promote/rollback/reject/pause)"
```

---

## Task 6: 废弃平台级 HTTP 审计 + 删表迁移

**Files:**

- Delete: `api/middleware/audit.go`, `api/middleware/audit_test.go`
- Delete: `internal/audit/domain/audit.go`
- Delete: `internal/audit/domain/port/audit.go`
- Delete: `internal/audit/application/audit_service.go`
- Delete: `internal/audit/infrastructure/persistence/audit_repo.go`, `audit_repo_mock_test.go`
- Delete: `api/http/audit_smoke_test.go`
- Rewrite/Delete: `api/wiring/audit_test.go`
- Modify: `api/wiring/audit.go`（删 `AuditCleanupWorker`/`NewAuditCleanupWorker`）
- Modify: `pkg/constants`（删 `AuditRetentionDays`/`AuditCleanupInterval`）
- Modify: `api/http/contract_test.go`（删 contractAuditRepo 旧方法——Task 3 已改新接口，此处清残留）
- Create: `pkg/migration/sql/036_drop_audit_events.up.sql`, `pkg/migration/sql/036_drop_audit_events.down.sql`

**Interfaces:**

- Consumes: Task 3 已把 handler/wiring/router/runtime 从旧审计脱钩；本任务只做纯删除。
- Produces: 平台 HTTP 审计全链路归零；`public.audit_events` 表删除。

- [ ] **Step 1: Delete the HTTP audit files**

```bash
git rm api/middleware/audit.go api/middleware/audit_test.go \
  internal/audit/domain/audit.go internal/audit/domain/port/audit.go \
  internal/audit/application/audit_service.go \
  internal/audit/infrastructure/persistence/audit_repo.go internal/audit/infrastructure/persistence/audit_repo_mock_test.go \
  api/http/audit_smoke_test.go
```

- [ ] **Step 2: Remove the cleanup worker from wiring + constants**

`api/wiring/audit.go`：删除 `AuditCleanupWorker` 结构体与 `NewAuditCleanupWorker`（Task 3 已重写文件，本步从当前文件删这两段）。
`pkg/constants`：删除 `AuditRetentionDays`、`AuditCleanupInterval`（grep 定位文件与行）。
`api/wiring/audit_test.go`：删除 `stubAuditRepo` 与 `TestNewAuditCleanupWorker_Construction`；若文件只剩这两个，整文件删除。

- [ ] **Step 3: Remove stale contract stub methods**

`api/http/contract_test.go`：确认 `contractAuditRepo` 只剩 Task 3 的 `List`/`GetByID` 两个方法；若旧 `InsertBatch`/`Query`/`Count`/`DeleteOlderThan` 仍在，删除。删除后检查 import（`auditdomain` 若只被旧方法用则移除；workflow/evaluation 契约 stub 仍用 `auditdomain.ResourceChangeAuditEvent` 参数，保留）。`scripts/record-contracts.go` 同款清理。

- [ ] **Step 4: Add the drop migration**

`pkg/migration/sql/036_drop_audit_events.up.sql`:

```sql
-- 平台级 HTTP 请求审计废弃(停写删表)。resource_change_audits(tenant schema)
-- 为唯一审计源。历史数据不可回滚，删除前已确认无合规保留需求。
DROP TABLE IF EXISTS public.audit_events;
```

`pkg/migration/sql/036_drop_audit_events.down.sql`（按 031 结构重建**空表**；先读 `pkg/migration/sql/031_*.sql` 的建表语句复制）:

```sql
-- 反向迁移仅重建空表结构，历史数据已丢失无法恢复。
CREATE TABLE IF NOT EXISTS public.audit_events (
    -- 列定义复制自 031_*.sql 的 audit_events 建表语句
);
```

> 适配：`031_*.sql` 若不存在则 grep `audit_events` 于 `pkg/migration/sql/` 找到建表迁移，复制其完整列定义到 down 文件。

- [ ] **Step 5: Verify zero references**

```bash
grep -rn "audit_events\|AuditService\|AuditRecorder\|AuditQueryService\|AuditCleanupWorker\|AuditMiddleware\|AuditRetentionDays\|AuditCleanupInterval" api/ cmd/ internal/ scripts/ pkg/ --include='*.go' | grep -v "resource_change_audits\|change_audit"
```

Expected: 无残留（`resource_change_audits` 与 `change_audit*` 是保留项）。若命中，删除或改写。

- [ ] **Step 6: Full verification**

Run: `go vet ./... && go test -short ./...`
Expected: 全绿。

> 注意：迁移编号 036 需在最新编号之后；跑 `ls pkg/migration/sql/ | sort` 确认最大编号 <36（若已 >35，把 036 改成 `NNN+1` 并在两处文件名同步）。

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore(audit): 废弃平台 HTTP 审计,删除 audit_events 表与配套代码(迁移 036)"
```

---

## Task 7: 前端

**Files:**

- Rewrite: `web/src/modules/audit/model/audit.ts`
- Rewrite: `web/src/modules/audit/api/audit.api.ts`
- Rewrite: `web/src/modules/audit/hooks/useAuditListPage.ts`
- Rewrite: `web/src/modules/audit/pages/AuditEventsPage.tsx`
- Rewrite: `web/src/modules/audit/components/AuditEventDrawer.tsx`
- Modify: `web/src/modules/audit/routes.tsx`
- Modify: `web/src/app/layout/menu.config.tsx`
- Modify: `web/src/app/layout/menu.config.test.tsx`
- Modify: `web/src/constants/index.ts`
- Rewrite: `web/src/modules/audit/hooks/useAuditListPage.test.ts`
- Rewrite: `web/src/modules/audit/api/audit.api.test.ts`

**Interfaces:**

- Consumes: Task 3 生成的 `web/src/services/gen/audit.ts`（字段 camelCase：`resourceKind`/`resourceId`/`operation`/`actorId`/`actorName`/`createdAt`/`before`/`after`）；后端查询参数 `resource_kind`/`actor_name`/`from`/`to`/`page`/`page_size`。
- Produces: `/audit` 页（租户 admin/owner 可见），筛选=时间范围+操作者纯文本+资源类型六类下拉。

- [ ] **Step 1: Rewrite the model**

`web/src/modules/audit/model/audit.ts`（全量重写）：

```ts
import { z } from 'zod';

// 与 proto/audit/audit.proto 对齐：字段 camelCase；before/after 为 JSONB 投影。
export const resourceChangeAuditSchema = z.object({
  id: z.string(),
  resourceKind: z.string(),
  resourceId: z.string(),
  operation: z.string(),
  actorId: z.string(),
  actorName: z.string(),
  createdAt: z.string(),
  before: z.unknown().optional(),
  after: z.unknown().optional(),
});

export const resourceChangeAuditsPageSchema = z.object({
  events: z.array(resourceChangeAuditSchema),
  total: z.number().int().nonnegative(),
});

export type ResourceChangeAudit = z.infer<typeof resourceChangeAuditSchema>;

// operation 是控制逻辑枚举，硬编码（后端无枚举端点）。
export const OPERATION_LABELS: Record<string, string> = {
  create: '创建',
  update: '更新',
  delete: '删除',
  publish: '发布',
  promote: '发布',
  rollback: '回滚',
  reject: '拒绝',
  pause: '暂停',
  activate: '激活',
};
```

> codegen 适配：以 `web/src/services/gen/audit.ts` 生成的 TS 类型为准核对字段名；若生成类型已导出，优先 `z.object({...})` 保持运行时校验。

- [ ] **Step 2: Rewrite the api client**

`web/src/modules/audit/api/audit.api.ts`（全量重写）：

```ts
import { resourceChangeAuditSchema, resourceChangeAuditsPageSchema, type ResourceChangeAudit } from '../model/audit';

import api from '@/services/client';

export interface AuditFilter {
  from?: string;
  to?: string;
  resourceKind?: string;
  actorName?: string;
  page: number;
  pageSize: number;
}

export const auditApi = {
  listEvents: async (filter: AuditFilter): Promise<{ events: ResourceChangeAudit[]; total: number }> => {
    const params: Record<string, string | number> = { page: filter.page, page_size: filter.pageSize };
    if (filter.from) params.from = filter.from;
    if (filter.to) params.to = filter.to;
    if (filter.resourceKind) params.resource_kind = filter.resourceKind;
    if (filter.actorName) params.actor_name = filter.actorName;
    const response = await api.get('/audit/events', { params });
    return resourceChangeAuditsPageSchema.parse(response.data);
  },

  getEvent: async (id: string): Promise<ResourceChangeAudit> => {
    const response = await api.get(`/audit/events/${encodeURIComponent(id)}`);
    return resourceChangeAuditSchema.parse(response.data);
  },
};
```

- [ ] **Step 3: Rewrite the hook**

`web/src/modules/audit/hooks/useAuditListPage.ts`（全量重写）：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { auditApi } from '../api/audit.api';
import type { ResourceChangeAudit } from '../model/audit';

import { usePagination } from '@/shared/hooks';

export interface AuditFilters {
  from?: string;
  to?: string;
  resourceKind?: string;
  actorName?: string;
}

interface RequestError { response?: { data?: { error?: string } } }

const EMPTY_FILTERS: AuditFilters = {};

export const useAuditListPage = () => {
  const [events, setEvents] = useState<ResourceChangeAudit[]>([]);
  const [loading, setLoading] = useState(true);
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [detailEvent, setDetailEvent] = useState<ResourceChangeAudit | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  // 请求序号：快速翻页/切换筛选时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async (nextPage: number, nextPageSize: number, nextFilters: AuditFilters) => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const pageData = await auditApi.listEvents({ ...nextFilters, page: nextPage, pageSize: nextPageSize });
      if (seq !== requestSeqRef.current) return;
      setEvents(pageData.events);
      setTotal(pageData.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计记录失败', duration: 0 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [setTotal]);

  useEffect(() => {
    void load(1, pageSize, EMPTY_FILTERS);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首次加载
  }, []);

  const applyFilters = useCallback((next: AuditFilters) => {
    setFilters(next);
    void load(1, pageSize, next);
  }, [load, pageSize]);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void load(nextPage, nextPageSize, filters);
  }, [onChange, load, filters]);

  const openDetail = useCallback(async (id: string) => {
    setDetailId(id);
    setDetailEvent(null);
    setDetailLoading(true);
    try {
      const event = await auditApi.getEvent(id);
      setDetailEvent(event);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载审计详情失败', duration: 0 });
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const closeDetail = useCallback(() => {
    setDetailId(null);
    setDetailEvent(null);
  }, []);

  return {
    events, loading, filters, total, page, pageSize, pageSizeOptions,
    detailId, detailEvent, detailLoading,
    applyFilters, handlePageChange, openDetail, closeDetail,
  };
};

export default useAuditListPage;
```

- [ ] **Step 4: Add the shared resource-kind options**

`web/src/constants/index.ts` 追加：

```ts
// 资源变更审计的资源类型（与 internal/audit/domain/change_audit.go 对齐）。
export const RESOURCE_KIND_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'agent', label: 'Agent' },
  { value: 'skill', label: '技能' },
  { value: 'mcp', label: 'MCP 服务器' },
  { value: 'knowledge', label: '知识库' },
  { value: 'workflow', label: '工作流' },
  { value: 'evaluation', label: '评测' },
];
```

- [ ] **Step 5: Rewrite the page**

`web/src/modules/audit/pages/AuditEventsPage.tsx`（全量重写）：

```tsx
import { Button, DatePicker, Form, Input, Pagination, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Dayjs } from 'dayjs';
import { useCallback } from 'react';

import { AuditEventDrawer } from '../components/AuditEventDrawer';
import { useAuditListPage } from '../hooks/useAuditListPage';
import type { ResourceChangeAudit } from '../model/audit';
import { OPERATION_LABELS } from '../model/audit';

import { RESOURCE_KIND_OPTIONS } from '@/constants';
import { EmptyHint } from '@/shared/ui';

interface FilterFormValues {
  range?: [Dayjs, Dayjs];
  actorName?: string;
  resourceKind?: string;
}

export const AuditEventsPage = () => {
  const {
    events, loading, total, page, pageSize, pageSizeOptions,
    detailId, detailEvent, detailLoading,
    applyFilters, handlePageChange, openDetail, closeDetail,
  } = useAuditListPage();
  const [form] = Form.useForm<FilterFormValues>();

  const onSearch = useCallback((values: FilterFormValues) => {
    applyFilters({
      from: values.range?.[0]?.toISOString(),
      to: values.range?.[1]?.toISOString(),
      actorName: values.actorName?.trim() || undefined,
      resourceKind: values.resourceKind,
    });
  }, [applyFilters]);

  const onReset = useCallback(() => {
    form.resetFields();
    applyFilters({});
  }, [applyFilters, form]);

  const columns: ColumnsType<ResourceChangeAudit> = [
    { title: '时间', dataIndex: 'createdAt', width: 160, render: (v: string) => new Date(v).toLocaleString() },
    { title: '操作者', dataIndex: 'actorName', ellipsis: true, width: 140 },
    { title: '资源类型', dataIndex: 'resourceKind', width: 120, render: (v: string) => (
      <Tag color="blue">{RESOURCE_KIND_OPTIONS.find((o) => o.value === v)?.label || v}</Tag>
    ) },
    { title: '操作', dataIndex: 'operation', width: 100, render: (v: string) => OPERATION_LABELS[v] || v },
    { title: '资源 ID', dataIndex: 'resourceId', ellipsis: true },
    {
      title: '操作', key: 'actions', width: 80,
      render: (_, record) => (
        <Button type="link" size="small" onClick={() => void openDetail(record.id)}>详情</Button>
      ),
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>审计日志</Typography.Title>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          租户内资源变更审计，记录 agent / skill / MCP / 知识库 / 工作流 / 评测的创建、更新、删除与生命周期操作
        </Typography.Text>
      </div>

      <Form<FilterFormValues> form={form} layout="inline" onFinish={onSearch} style={{ marginBottom: 16, rowGap: 8 }}>
        <Form.Item name="range" label="时间范围">
          <DatePicker.RangePicker showTime />
        </Form.Item>
        <Form.Item name="actorName" label="操作者">
          <Input placeholder="按姓名或登录名模糊搜索" allowClear style={{ width: 180 }} />
        </Form.Item>
        <Form.Item name="resourceKind" label="资源类型">
          <Select placeholder="全部" allowClear style={{ width: 160 }} options={RESOURCE_KIND_OPTIONS} />
        </Form.Item>
        <Form.Item>
          <Space>
            <Button type="primary" htmlType="submit">查询</Button>
            <Button onClick={onReset}>重置</Button>
          </Space>
        </Form.Item>
      </Form>

      <Table<ResourceChangeAudit>
        rowKey="id"
        columns={columns}
        dataSource={events}
        loading={loading}
        size="small"
        pagination={false}
        locale={{ emptyText: <EmptyHint title="没有找到审计记录" description="调整筛选条件后重试" /> }}
      />

      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
        <Pagination
          current={page} pageSize={pageSize} total={total} pageSizeOptions={pageSizeOptions}
          showSizeChanger showTotal={(t) => `共 ${t} 条记录`} onChange={handlePageChange}
        />
      </div>

      <AuditEventDrawer event={detailEvent} loading={detailLoading} open={detailId !== null} onClose={closeDetail} />
    </div>
  );
};

export default AuditEventsPage;
```

- [ ] **Step 6: Rewrite the drawer**

`web/src/modules/audit/components/AuditEventDrawer.tsx`（全量重写；复用既有 `DiffBlock` 组件展示 before/after 投影）：

```tsx
import { Descriptions, Drawer, Spin, Tag } from 'antd';
import { useMemo } from 'react';

import type { ResourceChangeAudit } from '../model/audit';
import { OPERATION_LABELS } from '../model/audit';

import { RESOURCE_KIND_OPTIONS } from '@/constants';
import { DiffBlock } from '@/shared/ui';

interface Props {
  event: ResourceChangeAudit | null;
  loading: boolean;
  open: boolean;
  onClose: () => void;
}

export const AuditEventDrawer = ({ event, loading, open, onClose }: Props) => {
  const before = useMemo(() => (event?.before ? JSON.stringify(event.before, null, 2) : '{}'), [event?.before]);
  const after = useMemo(() => (event?.after ? JSON.stringify(event.after, null, 2) : '{}'), [event?.after]);

  return (
    <Drawer title="审计详情" width={640} open={open} onClose={onClose}>
      <Spin spinning={loading}>
        {event && (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="时间">{new Date(event.createdAt).toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label="操作者">{event.actorName}</Descriptions.Item>
            <Descriptions.Item label="资源类型">
              {RESOURCE_KIND_OPTIONS.find((o) => o.value === event.resourceKind)?.label || event.resourceKind}
            </Descriptions.Item>
            <Descriptions.Item label="资源 ID">{event.resourceId}</Descriptions.Item>
            <Descriptions.Item label="操作">
              <Tag color="blue">{OPERATION_LABELS[event.operation] || event.operation}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="变更前">
              <pre style={{ maxHeight: 240, overflow: 'auto', margin: 0 }}>{before}</pre>
            </Descriptions.Item>
            <Descriptions.Item label="变更后">
              <pre style={{ maxHeight: 240, overflow: 'auto', margin: 0 }}>{after}</pre>
            </Descriptions.Item>
          </Descriptions>
        )}
      </Spin>
    </Drawer>
  );
};
```

> 适配：`DiffBlock` 的实际 props 以 `web/src/shared/ui` 现有实现为准；若它渲染两个 JSON 字符串参数则改用 `DiffBlock` 替换两个 `<pre>`，否则保留 `<pre>` 简化实现（本功能非本任务核心）。

- [ ] **Step 7: Update routes**

`web/src/modules/audit/routes.tsx`：`requiredRole="global_admin"` → `requiredTenantRole="admin"`（`PrivateRoute` 已支持，owner 经 `TENANT_ROLE_RANK` 自动通过）：

```tsx
export const auditRoutes = [
  <Route
    key="audit"
    path="/audit"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <AuditEventsPage />
      </PrivateRoute>
    }
  />,
];
```

- [ ] **Step 8: Update the menu**

`web/src/app/layout/menu.config.tsx`：

1. 从 `platform-admin-group` children 删除 `/audit` 项（L159-162）。
2. 在 `tenant-group` push 块之后（L137 之后、`platform-admin-group` 之前）插入顶层菜单项：

```tsx
  if (user?.current_tenant && canManageTenant) {
    base.push({
      key: '/audit',
      icon: <AuditOutlined />,
      label: '审计日志',
    });
  }
```

1. `resolveOpenKeys`：从 `platform-admin-group` 的匹配分支（L195）移除 `/audit`（否则 `/audit` 会尝试展开已不存在的分组）：

```tsx
  if (
    pathname.startsWith('/models') ||
    pathname.startsWith('/prompts') ||
    pathname.startsWith('/mechanism') ||
    pathname.startsWith('/admin')
  )
    return ['platform-admin-group'];
  return [];
```

> 说明：`/audit` 是顶层菜单项，`resolveOpenKeys('/audit')` 落入 `return []`（无展开分组），符合顶层项行为。

- [ ] **Step 9: Update menu tests**

`web/src/app/layout/menu.config.test.tsx`：新增/调整用例，覆盖：

- 租户 `admin`/`owner` → 出现 `/audit` 顶层项；
- 租户 `member` → 不出现 `/audit`；
- `global_admin`（作为 member 角色，无 tenant）→ 不出现 `/audit`（`canManageTenant` 为 false 且无 current_tenant）；
- `resolveOpenKeys('/audit')` → `[]`。

按既有测试风格编写（读文件开头取 user fixture 结构）。

- [ ] **Step 10: Rewrite frontend tests**

`web/src/modules/audit/hooks/useAuditListPage.test.ts`：按新 `AuditFilters`（from/to/resourceKind/actorName）重写，mock `auditApi.listEvents`，断言 `resource_kind`/`actor_name` 查询参数透传与列表渲染（参考既有测试的 renderer 与 mock 风格）。

`web/src/modules/audit/api/audit.api.test.ts`：mock `client.ts` 的 `api.get`，断言 URL/params（`/audit/events` + `page/page_size/resource_kind/actor_name/from/to`）与 zod 解析（含非法 payload 报错用例）。

- [ ] **Step 11: Lint + build**

Run: `make fe-lint && make fe-build`
Expected: 通过。`grep -rn "risk_level\|outcome" web/src/modules/audit` 应无残留。

- [ ] **Step 12: Commit**

```bash
git add web/src/modules/audit web/src/app/layout/menu.config.tsx web/src/app/layout/menu.config.test.tsx web/src/constants/index.ts
git commit -m "feat(web): /audit 改为租户资源变更审计页面(六类资源+时间/操作者/类型筛选)"
```

---

## Task 8: E2E 与系统验收

**Files:**

- Rewrite: `web/e2e/stateful/packs/audit.ts`（改查 `resource_change_audits` + 新筛选断言）
- Verify: `web/src/services/e2e-surface.json`（`/audit` 路由语义变更后确认）

**Interfaces:**

- Consumes: Task 3 路由与 Task 7 页面；spec F 引用清理清单的残留项。

- [ ] **Step 1: Rewrite the E2E audit pack**

`web/e2e/stateful/packs/audit.ts`：把对 `public.audit_events` 的查询改为 tenant schema 的 `resource_change_audits`；断言 admin/owner 可见 `/audit` 页、member 不可见；执行一次资源 create（如创建 workflow），对账列表出现对应 `resource_kind`/`operation` 行；用新筛选（时间范围/操作者/资源类型）验证过滤。具体断言以 `web/e2e/stateful/packs/` 既有 pack 的 DB 对账风格为准（读同目录其它 pack 复制结构）。

- [ ] **Step 2: Verify e2e-surface**

`web/src/services/e2e-surface.json`：确认 `/audit` 条目在新路由语义下仍存在；若字段描述绑定 global_admin，更新为租户 admin 语义。

- [ ] **Step 3: Run the system acceptance via the mandatory skill**

Run: `stratum-e2e-development` skill（由主会话调用，不得绕过 skill 直敲 make）。

**This is the sole gate for merging.** 走 skill 全流程：本地无头 Chromium short 跑 `make test-verify-before-pr`；R3 自动 `STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all` soak；创建 PR 前在 clean commit 上通过系统验收。

- [ ] **Step 4: Final quality gates before PR**

Run: `bash scripts/quality/risk-regression-guard.sh --explain && go test -v -race -timeout 30s ./... && make fe-lint && make fe-build`
Expected: 全绿；无 failed/skipped/unreconciled capability。

- [ ] **Step 5: PR**

```bash
git fetch origin main
# 若 base 落后 origin/main:先合入最新 main,本地验证后 push(merge commit 关联提交者)
git push -u origin feat/audit-resource-change
gh pr create --base main
```

PR 描述含 What/Why/HowToTest，并写明：删表破坏性（`public.audit_events` 已确认无合规保留需求）。

---

## Self-Review

**Spec coverage:**

- A（枚举）→ Task 1 ✓
- B（workflow 写端）→ Task 4（四方法 actor 级联 + 投影白名单 + Delete 先 GetDefinition 加载 before + Publish op=publish + port 级联 + mock/contract stub 同步）✓
- C（evaluation 写端）→ Task 5（统一 evaluation kind + experiment.ID；Create/Enqueue→create、Activate→activate、Promote 改造、Rollback/Reject/Pause；`promoteChangeAuditTx` 待改造点 → op=promote、actor=command.ActorID、安全投影）✓
- D（读取端）→ Task 2（port + repo，空 tenantID fail-closed 三处，tenant_id 谓词恒存在，actor 映射 display_name>github_login>actor_id 兜底，`public.users` schema-qualified 不返回 email，actor_name ILIKE 子串）；handler 用 `tenantIDFromCtx` → Task 3 ✓
- E（proto+handler+路由+契约 golden）→ Task 3（proto/audit/audit.proto；camelCase 契约面；`RequireTenantRole("admin")`；新增契约 golden）✓
- F（废弃 HTTP 审计）→ Task 3 脱钩（middleware 挂载、runtime registerAuditCleanup、contract wiring）+ Task 6 删除（文件、worker、常量、036 迁移、引用清零）✓
- G（前端）→ Task 7（routes `requiredTenantRole="admin"`；菜单顶层项 + canManageTenant；RESOURCE_KIND_OPTIONS；页面/筛选/详情重写；model/api/hook 全量重写；测试更新）✓
- 测试/风险/迁移 → Task 2-8 内嵌；删表破坏性写入 Task 8 PR 描述 ✓

**Placeholder scan:** 无 TBD/TODO；codegen 适配步骤均为「读生成文件→按记录调整」的具体指令，非占位符。`<pre>`/`DiffBlock` 适配标注了既有组件读取路径。

**Type consistency:**

- `ResourceChangeAuditQuery.List(ctx, tenantID, filter) (rows, total, error)` 在 Task 2/3/stub 全链一致；`GetByID` 返回 `(*Row, error)`、nil 表示未找到，handler 映射 404 ✓
- workflow port 四方法尾参 `*auditdomain.ResourceChangeAuditEvent` 在 Task 4 全部实现/mock/stub 一致；service 方法 `actorID string` 尾参在 handler 调用点一致 ✓
- evaluation `Create` 尾参 `ev` 在 port/service/mock 一致；`CreateExperimentInput.ActorID` 在 service/handler 一致；`experimentAuditOperation` 映射与 Task 1 op 常量一致 ✓
- `insertChangeAudit(ctx, tx, ev)` 在四个 context（agent/skill 既有 + workflow/evaluation 新增）签名统一 ✓
- 前端字段 camelCase（`resourceKind`/`actorName`/`createdAt`）在 model/api/page/drawer/后端 DTO 一致 ✓
