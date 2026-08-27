# 用户记忆管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户全部 5 类可进入上下文的记忆（事实 / 实体 / 历史摘要 / 活跃快照 / 原始条目）可可视化、可删除、可修改，并在事实编辑/删除时同步 Milvus 向量数据。

**Architecture:** 方案 A —— 统一 `internal/memory/application.MemoryService` 扩展 12 个用户侧方法（每类资源独立子方法），复用既有 `FactRepo/EntityRepo/HistoryRepo/ActiveSnapshotRepo/MemoryRepo` 与 `VectorStore`（Upsert/DeleteFactVectors/DeleteEntryVectors）。新增 5 个 REST 子资源挂到现有 `/memory` 组（member + requireActive）。事实编辑顺序刻意设计：先嵌入新内容（fail-closed 502）→ 删旧向量 → PG update → upsert 新向量，避免同 ID 向量互相覆盖；PG 是唯一真相源，向量清理 best-effort + GC reconcile 兜底。前端 `MyMemoriesPage` 扩为 5 Tab，每 Tab 独立组件 + hook。

**Tech Stack:** Go 1.25、Gin v1.9.1、pgx v5.9.2、protoc-gen-ginstruct（proto 契约生成）、Ant Design 5.20、React 18.3、Vite 6.4、zod、vitest。

## Global Constraints

> 本功能所有任务都隐式遵守；未列出的 spec 约束见 `docs/superpowers/specs/2026-08-27-user-memory-management-design.md`。

- **分层依赖**：`handler → application → domain/port`；`application/` 禁止 import pgx/存储驱动/Gin；`domain/` 仅依赖 stdlib + `pkg/constants`。
- **多租户**：所有 tenant-scoped 表的 repo 方法必须经 `execTenant(ctx, tenantID, fn)`；port 方法签名必须显式含 `tenantID string`。
- **port 变更后立即同步所有 test mock/stub**：`internal/memory/application/memory_service_v2_test.go` 的 `MockFactRepo/MockEntityRepo/cleanupMemoryRepo`、`test/e2e/testutil.go` 的 mock、handler 测试的 fake 必须同步实现新方法，否则编译失败。
- **错误逐层 wrap**：`fmt.Errorf("operation: %w", err)`；handler 一律 `c.Error(err)` 交给统一错误中间件；业务错误按 `domain.Err*` → `translatePgError` → `error_mapping` 映射 HTTP。
- **代码质量棘轮**：新函数圈复杂度 ≤10、认知复杂度 ≤15、函数 ≤120 行、嵌套 ≤4、Go 行宽 ≤120。
- **禁止 inline 魔法数字**：分页默认用 `pkg/constants.DefaultPageSize`（=20）与前端 `DEFAULT_PAGE_SIZE`；快照限制用 `constants.ActiveSnapshot*`；跨包常量放 `pkg/constants/memory.go`（`Default`/`Max`/`Min` 前缀）。
- **JSONB**：pgx v5 写自定义 struct 前先 `json.Marshal` 再传 `string(b)`。
- **proto 契约**：HTTP JSON 参数契约唯一事实源是 `proto/` 下 .proto；改动后 `make proto-gen`（生成 `api/http/dto/gen/` 与 `web/src/services/gen/`，均 gitignored）；绕过 make 直敲 `go test` 且未生成时 import 编译失败属预期。`optional` 字段由 protoc-gen-ginstruct 生成 Go 指针（先例：`UpdateWorkspaceRequest.Name *string`）。
- **前端规范**：普通 API 走 `web/src/services/client.ts` 唯一实例；错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 3 })`，成功 `message.success({ content, duration: 2 })`；危险操作 `Modal.confirm`/`DangerPopconfirm` + 「不可恢复」后果描述，禁止 `alert()/confirm()`；用户可见字符串全部中文；`useEffect` 依赖完整 + cancelled 标志；行为常量来自 `web/src/constants/`，页面禁止硬编码。
- **产品规范**（`docs/agent/product.md`）：列表 ≤5 列；危险操作二次确认；空状态用 `Empty` + 引导操作；用户侧只读 content/时间/importance，管理侧额外展示 scope/agent_id。
- **归属校验**：所有 `:id` 操作先读实体校验 `UserID == 当前用户`，不匹配一律 404（不泄露存在性）。
- **Git**：工作区为 worktree，分支 `feat/user-memory-management`（非 main），可直接 commit；Commit 标题 `[type](scope): description`。

## File Structure

新建/修改文件清单（按任务归属）：

| 层 | 文件 | 责任 | Task |
|---|---|---|---|
| proto | `proto/memory/memory.proto` | 扩展 `MemoryFactResponse` + 8 个新消息 | 1 |
| DTO | `api/http/dto/gen/memory.go`、`web/src/services/gen/memory.ts` | 生成物（gitignored） | 1 |
| domain | `internal/memory/domain/errors.go` | +5 个 sentinel | 2、3 |
| domain | `internal/memory/domain/fact.go` | +`FactListFilter` + 导出 `IsValidFactCategory` | 2 |
| domain | `internal/memory/domain/memory_entry_list.go` | +`MemoryEntryListItem` | 3 |
| port | `internal/memory/domain/port/fact_repo.go`、`entity_repo.go`、`history_repo.go`、`active_snapshot_repo.go`、`memory_repo.go` | +8 个方法 | 2、3 |
| persistence | `internal/memory/infrastructure/persistence/fact_repo.go`、`entity_repo.go`、`history_repo.go`、`active_snapshot_repo.go`、`memory_repo.go` | +8 个方法、扩展 Update SQL | 2、3 |
| persistence test | `internal/memory/infrastructure/persistence/fact_repo_mock_test.go`、`entity_repo_mock_test.go`、`history_repo_mock_test.go`、`active_snapshot_repo_mock_test.go`、`memory_repo_mock_test.go` | pgxmock 测试 | 2、3 |
| application | `internal/memory/application/memory_service_v2.go` | +DTO 类型、UserMemory 扩展、+12 方法、embed 辅助 | 4、5 |
| application | `internal/memory/application/types.go` | +`ErrMemoryEmbeddingUnavailable` | 4 |
| application test | `internal/memory/application/memory_service_v2_test.go` | mock 同步 + 新方法测试 | 2、3、4、5 |
| handler | `api/http/handler/user_memory_handler.go` | userMemorySvc 接口扩展 + 12 方法 + helper | 6 |
| handler test | `api/http/handler/user_memory_handler_test.go` | fake 扩展 + 新测试 | 6 |
| router | `api/http/router.go` | +12 路由 | 6 |
| middleware | `api/middleware/error_mapping.go` | +8 条错误映射 | 6 |
| wiring | `api/wiring/memory.go` | 注入 historyRepo/activeSnapshotRepo | 6 |
| 前端 model/api | `web/src/modules/memory/model/memory.ts`、`api/memory-user.api.ts` | 类型 + 12 个请求函数 | 7 |
| 前端 hooks | `web/src/modules/memory/hooks/use{Facts,Entities,Summaries,Snapshots,Entries}Tab.ts` | 每 Tab 数据/分页/删除/编辑 | 7 |
| 前端 hook test | `web/src/modules/memory/hooks/__tests__/use{Facts,Entries}Tab.test.ts` | 代表 hook 测试 | 7 |
| 前端组件 | `web/src/modules/memory/components/{FactTable,EntityTable,SummaryTable,SnapshotPanel,EntryTable}.tsx` | 5 Tab 组件 | 8 |
| 前端页面 | `web/src/modules/memory/pages/MyMemoriesPage.tsx`、`hooks/useMyMemoriesPage.ts` + 测试 | 5 Tab 布局 + 统计/清空 | 8 |
| e2e | `test/e2e/memory_lifecycle_test.go` | 编辑/删除事实 → 召回联动 | 8 |

**任务依赖**：Task 1（proto/DTO）→ Task 2/3（repo，编译依赖 DTO 无关但先做契约）→ Task 4/5（service）→ Task 6（handler/wiring，编译依赖 1-5）→ Task 7（前端数据层）→ Task 8（页面 + e2e）。Task 1 先行：所有后端编译依赖 `api/http/dto/gen` 存在，未生成时 `go test` 失败。

---

### Task 1: proto 契约扩展 + DTO 生成

**Files:**

- Modify: `proto/memory/memory.proto`
- Generate: `api/http/dto/gen/memory.go`、`web/src/services/gen/memory.ts`（gitignored）

**Interfaces:**

- Consumes: 无（现有 8 个消息保持不变）。
- Produces: `gen.MemoryFactResponse` 新增字段 `Confidence float64`、`Category string`、`Source string`、`Status string`（json `confidence/category/source/status`）；新类型 `gen.UpdateMemoryFactRequest{Content *string; Importance *float64; Category *string}`、`gen.ListMemoryFactsResponse{Facts []MemoryFactResponse; Total int64}`、`gen.MemorySummaryItemResponse`、`gen.ListMemorySummariesResponse`、`gen.MemorySnapshotResponse`、`gen.ListMemorySnapshotsResponse`、`gen.UpdateMemorySnapshotRequest`、`gen.MemoryEntryItemResponse`、`gen.ListMemoryEntriesResponse`。Task 6 handler 与 Task 7 前端依赖这些类型。

- [ ] **Step 1: 编辑 `proto/memory/memory.proto`，扩展 `MemoryFactResponse` 并新增 8 个消息**

在 `MemoryFactResponse` 末尾追加 4 个字段，并在文件末尾追加新消息：

```proto
message MemoryFactResponse {
  string id = 1;
  string scope = 2;
  string content = 3;
  double importance = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp updated_at = 6;
  // 用户侧管理页详情展示字段（spec §1 DTO 变更）。
  double confidence = 7;
  string category = 8;
  string source = 9;
  string status = 10;
}

// UpdateMemoryFactRequest 事实编辑请求；至少提供一项（proto3 optional 生成
// Go 指针，handler 据此判断「未提供」与「零值」）。
message UpdateMemoryFactRequest {
  optional string content = 1;
  optional double importance = 2;
  optional string category = 3;
}

// ListMemoryFactsResponse 事实分页列表（GET /memory/facts）。
message ListMemoryFactsResponse {
  repeated MemoryFactResponse facts = 1;
  int64 total = 2;
}

// MemorySummaryItemResponse 历史摘要条目（用户侧只读 + 删除）。
message MemorySummaryItemResponse {
  string id = 1;
  string summary = 2;
  string tier = 3;
  double importance = 4;
  string conversation_id = 5;
  google.protobuf.Timestamp period_end = 6;
  google.protobuf.Timestamp created_at = 7;
}

message ListMemorySummariesResponse {
  repeated MemorySummaryItemResponse summaries = 1;
  int64 total = 2;
}

// MemorySnapshotResponse 活跃快照（每 (user_id, agent_id) 一条）。
message MemorySnapshotResponse {
  string agent_id = 1;
  repeated string work_context = 2;
  repeated string personal_context = 3;
  repeated string top_of_mind = 4;
  google.protobuf.Timestamp expires_at = 5;
  google.protobuf.Timestamp updated_at = 6;
  string status = 7;
}

message ListMemorySnapshotsResponse {
  repeated MemorySnapshotResponse snapshots = 1;
}

// UpdateMemorySnapshotRequest 快照编辑（三段数组整体替换）。
message UpdateMemorySnapshotRequest {
  repeated string work_context = 1;
  repeated string personal_context = 2;
  repeated string top_of_mind = 3;
}

// MemoryEntryItemResponse 原始条目（用户侧只读 + 删除）。
message MemoryEntryItemResponse {
  string id = 1;
  string role = 2;
  string content = 3;
  string type = 4;
  string scope = 5;
  double importance = 6;
  google.protobuf.Timestamp created_at = 7;
  google.protobuf.Timestamp expires_at = 8;
}

message ListMemoryEntriesResponse {
  repeated MemoryEntryItemResponse entries = 1;
  int64 total = 2;
}
```

- [ ] **Step 2: 生成 Go 与 TS DTO**

Run: `make proto-gen`
Expected: 无输出；`api/http/dto/gen/memory.go` 与 `web/src/services/gen/memory.ts` 更新时间戳。

- [ ] **Step 3: 验证生成物编译且 optional 生成指针**

Run: `go build ./api/http/dto/gen/... && grep -A4 "type UpdateMemoryFactRequest" api/http/dto/gen/memory.go`
Expected: 编译通过；`UpdateMemoryFactRequest` 字段为指针（`Content *string`、`Importance *float64`、`Category *string`）。若非指针类型（生成器未来行为变化），停下并重新设计 handler bind（Task 6 Step 5）为「零值不可区分」策略。

- [ ] **Step 4: 提交**

```bash
git add proto/memory/memory.proto
git commit -m "feat(memory): extend proto contract with management resources"
```

---

### Task 2: FactRepo 过滤列表 + Update 写 category/confidence

**Files:**

- Modify: `internal/memory/domain/errors.go`（+`ErrImportanceOutOfRange`、`ErrFactNotEditable`、`ErrEmptyFactPatch`）
- Modify: `internal/memory/domain/fact.go`（+`FactListFilter` + 导出 `IsValidFactCategory`）
- Modify: `internal/memory/domain/port/fact_repo.go`（+`ListUserFactsFiltered`、`CountUserFactsFiltered`）
- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`（+2 方法、扩展 `Update` SQL）
- Modify: `internal/memory/application/memory_service_v2_test.go`（`MockFactRepo` +2 方法）
- Test: `internal/memory/infrastructure/persistence/fact_repo_mock_test.go`

**Interfaces:**

- Consumes: `domain.MemoryFact`（既有 17 字段）、`domain.Scope`、`translatePgError`（fact_repo.go:635）。
- Produces:
  - `domain.FactListFilter{Query string; ImportanceMin, ImportanceMax *float64; Category string}`
  - `domain.IsValidFactCategory(category string) bool`
  - `port.FactRepo.ListUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter, limit, offset int) ([]*domain.MemoryFact, error)`
  - `port.FactRepo.CountUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter) (int, error)`
  - 错误：`domain.ErrImportanceOutOfRange`（400）、`domain.ErrFactNotEditable`（409）、`domain.ErrEmptyFactPatch`（400）。Task 4 service 使用这三个 sentinel，Task 6 映射 HTTP。

- [ ] **Step 1: 写失败测试（domain 常量 + 过滤列表）**

在 `internal/memory/infrastructure/persistence/fact_repo_mock_test.go` 末尾追加：

```go
func TestFactRepo_ListUserFactsFiltered(t *testing.T) {
 mock, _ := newFactMock(t)
 repo := newMockFactRepo(mock)
 ts := ts()
 rows := pgxmock.NewRows([]string{"id", "user_id", "agent_id", "scope", "conversation_id", "content", "importance",
  "status", "superseded_by", "access_count", "last_accessed_at",
  "created_at", "updated_at", "frecency_score", "category", "confidence", "source"}).
  AddRow("fact-1", "user-1", "", "user", "conv-1", "I prefer dark mode", 0.8,
   "active", nil, 1, ts, ts, ts, 0.5, "preference", 0.9, "explicit_user")

 importanceMin, importanceMax := 0.5, 0.9
 mock.ExpectQuery("SELECT id, user_id, agent_id").
  WithArgs("user-1", "%dark%", "preference", importanceMin, importanceMax, 10, 0).
  WillReturnRows(rows)

 got, err := repo.ListUserFactsFiltered(context.Background(), "tenant-1", "user-1",
  domain.FactListFilter{Query: "dark", ImportanceMin: &importanceMin, ImportanceMax: &importanceMax, Category: "preference"},
  10, 0)
 require.NoError(t, err)
 require.Len(t, got, 1)
 require.Equal(t, "fact-1", got[0].ID)
 require.Equal(t, "preference", got[0].Category)
}

func TestFactRepo_CountUserFactsFiltered(t *testing.T) {
 mock, _ := newFactMock(t)
 repo := newMockFactRepo(mock)
 mock.ExpectQuery("SELECT count").
  WithArgs("user-1", "dark").
  WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))

 total, err := repo.CountUserFactsFiltered(context.Background(), "tenant-1", "user-1",
  domain.FactListFilter{Query: "dark"})
 require.NoError(t, err)
 require.Equal(t, 3, total)
}
```

- [ ] **Step 2: 运行测试确认失败（编译失败）**

Run: `go test ./internal/memory/infrastructure/persistence/ -run TestFactRepo_List -count=1`
Expected: FAIL —— `ListUserFactsFiltered` 未定义（port/实现缺失）。

- [ ] **Step 3: 新增 domain 类型与 sentinel**

在 `internal/memory/domain/errors.go` 的 `Err*` 块内追加：

```go
 // ErrImportanceOutOfRange is returned when a fact importance is outside [0, 1].
 ErrImportanceOutOfRange = errors.New("memory fact importance must be in [0, 1]")
 // ErrFactNotEditable is returned when editing a fact whose status is not active
 // (superseded/archived facts are owned by the extraction pipeline).
 ErrFactNotEditable = errors.New("memory fact not editable")
 // ErrEmptyFactPatch is returned when an update request carries no field to change.
 ErrEmptyFactPatch = errors.New("memory fact update requires at least one field")
```

在 `internal/memory/domain/fact.go` 的 `factCategoryAllowSet` 定义附近追加：

```go
// FactListFilter 事实列表筛选条件（管理页 GET /memory/facts，spec §1 Facts）。
type FactListFilter struct {
 Query         string   // 内容匹配（复用 SearchByContent 的 ILIKE 机制）
 ImportanceMin *float64
 ImportanceMax *float64
 Category      string // 精确分类
}

// IsValidFactCategory 报告 category 是否在合法白名单内（供 PATCH 校验复用，
// factCategoryAllowSet 保持 unexported）。
func IsValidFactCategory(category string) bool { return factCategoryAllowSet[category] }
```

- [ ] **Step 4: 扩展 `port/fact_repo.go`**

在 `FactRepo` 接口的 `ListUserFacts` 方法后追加：

```go
 // ListUserFactsFiltered lists the authenticated user's active user-scope facts
 // with content/category/importance filter and pagination.
 ListUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter, limit, offset int) ([]*domain.MemoryFact, error)
 // CountUserFactsFiltered counts rows matching the same filter (pagination total).
 CountUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter) (int, error)
```

- [ ] **Step 5: 实现 `persistence/fact_repo.go` 的 2 个新方法 + Update SQL 扩展**

在 `ListUserFacts` 方法后追加（沿用 `scanFacts` 与 `execTenant` 模式；`strings` 需加入 import）：

```go
// factFilterClause 构造 ListUserFactsFiltered/CountUserFactsFiltered 的 WHERE 子句
// 与参数；$N 占位符与 args 顺序绑定，禁止把用户输入拼进 SQL 文本。
func factFilterClause(userID string, filter domain.FactListFilter) (string, []any) {
 clauses := []string{"user_id = $1", "status = 'active'", "scope = 'user'"}
 args := []any{userID}
 if filter.Query != "" {
  clauses = append(clauses, fmt.Sprintf("content ILIKE $%d", len(args)+1))
  args = append(args, "%"+filter.Query+"%")
 }
 if filter.Category != "" {
  clauses = append(clauses, fmt.Sprintf("category = $%d", len(args)+1))
  args = append(args, filter.Category)
 }
 if filter.ImportanceMin != nil {
  clauses = append(clauses, fmt.Sprintf("importance >= $%d", len(args)+1))
  args = append(args, *filter.ImportanceMin)
 }
 if filter.ImportanceMax != nil {
  clauses = append(clauses, fmt.Sprintf("importance <= $%d", len(args)+1))
  args = append(args, *filter.ImportanceMax)
 }
 return strings.Join(clauses, " AND "), args
}

func (r *FactRepo) ListUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter, limit, offset int) ([]*domain.MemoryFact, error) {
 where, args := factFilterClause(userID, filter)
 query := `SELECT id, user_id, agent_id, scope, conversation_id, content, importance,
  status, superseded_by, access_count, last_accessed_at,
  created_at, updated_at, frecency_score,
  category, confidence, source
 FROM memory_facts WHERE ` + where +
  ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1) +
  ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
 args = append(args, limit, offset)

 var facts []*domain.MemoryFact
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, query, args...)
  if err != nil {
   return fmt.Errorf("list user facts filtered: %w", err)
  }
  defer rows.Close()
  facts, err = scanFacts(rows)
  return err
 })
 return facts, err
}

func (r *FactRepo) CountUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter) (int, error) {
 where, args := factFilterClause(userID, filter)
 query := `SELECT count(*) FROM memory_facts WHERE ` + where

 var total int
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx, query, args...).Scan(&total)
 })
 return total, err
}
```

将 `Update` 的 SQL 扩展为写 `category = $10, confidence = $11`（原 9 参数）：

```go
 const query = `
  UPDATE memory_facts SET
   content = $2, importance = $3, status = $4, superseded_by = $5,
   access_count = $6, last_accessed_at = $7, updated_at = $8,
   frecency_score = $9, category = $10, confidence = $11
  WHERE id = $1`

 var supersededBy *string
 if fact.SupersededBy != "" {
  supersededBy = &fact.SupersededBy
 }

 return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx, query,
   fact.ID, fact.Content, fact.Importance, fact.Status, supersededBy,
   fact.AccessCount, fact.LastAccessAt, fact.UpdatedAt, fact.FrecencyScore,
   fact.Category, fact.Confidence,
  )
  if err != nil {
   return translatePgError(err, "update fact")
  }
  if tag.RowsAffected() == 0 {
   return domain.ErrFactNotFound
  }
  return nil
 })
```

> **Pre-Flight 已核验的回归点**：`Update` 的调用方有三处——(a) `supersedeCandidate`（extraction.go:324）用 `FindSupersedeCandidates` 返回的 candidate.Fact；(b) recall 访问计数（retrieval.go:104）用 PG 二次校验后的完整 fact（GetByID，17 列含 category/confidence）；(c) `supersede_worker.go:149` 同 (a)。
> 现状：`supersedeQuery`（fact_repo.go:408）的 SELECT 只选 12 列，**不含 category/confidence** —— candidate.Fact 的这两字段是零值。若只扩展 `Update` SQL 而不同步扩展 supersedeQuery，supersede 路径会把 category 写成空串、confidence 写成 0（回归 bug）。因此必须同步把 `category, confidence` 加入 supersedeQuery 的 SELECT 与 `FindSupersedeCandidates` 的 scan：(b) 路径安全（fact 已完整）。

**同时修改 `supersedeQuery` 与 `FindSupersedeCandidates`（同一文件）：**

```go
// supersedeQuery 的 SELECT 列表补 category, confidence（置于 updated_at 之后、sim 之前）：
 query := `
  SELECT id, user_id, agent_id, scope, content, importance,
   status, superseded_by, access_count, last_accessed_at,
   created_at, updated_at, category, confidence,
   similarity(content, $2) as sim
  FROM memory_facts
  WHERE user_id = $1 AND status = 'active' AND similarity(content, $2) > ` + thresholdParam + `
    AND ` + supersedeScopeClause(filter) + `
  ORDER BY sim DESC LIMIT ` + limitParam
```

```go
// FindSupersedeCandidates 的 rows.Scan 补两个目标字段：
   if err := rows.Scan(
    &f.ID, &f.UserID, &aid, &scope, &f.Content, &f.Importance,
    &f.Status, &supersededBy, &f.AccessCount, &f.LastAccessAt,
    &f.CreatedAt, &f.UpdatedAt, &f.Category, &f.Confidence, &sim,
   ); err != nil {
    return fmt.Errorf("scan supersede candidate: %w", err)
   }
```

> 新增 pgxmock 回归测试 `TestFactRepo_Update_SupersedePreservesCategory`：断言 Update 扩展后 supersede 链路（FindSupersedeCandidates → supersedeCandidate.Update）不会把 category 写空。实现层验证：`go test ./internal/memory/... -run TestFactRepo_ -count=1` 覆盖既有 `fact_repo_test.go` 的 Update 用例（该用例用完整 `testFact` 构造，天然覆盖 11 参数路径）。

- [ ] **Step 6: 同步 `MockFactRepo`（application 测试）**

在 `internal/memory/application/memory_service_v2_test.go` 的 `MockFactRepo.ListUserFacts` 后追加：

```go
func (m *MockFactRepo) ListUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter, limit, offset int) ([]*domain.MemoryFact, error) {
 args := m.Called(ctx, tenantID, userID, filter, limit, offset)
 return args.Get(0).([]*domain.MemoryFact), args.Error(1)
}

func (m *MockFactRepo) CountUserFactsFiltered(ctx context.Context, tenantID, userID string, filter domain.FactListFilter) (int, error) {
 args := m.Called(ctx, tenantID, userID, filter)
 return args.Int(0), args.Error(1)
}
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/memory/... -count=1`
Expected: PASS（新增 2 测试通过；`MockFactRepo` 同步后 application 测试编译通过）。

- [ ] **Step 8: 提交**

```bash
git add internal/memory/domain/ internal/memory/infrastructure/persistence/fact_repo.go internal/memory/application/memory_service_v2_test.go
git commit -m "feat(memory): add filtered fact list and write category/confidence on update"
```

---

### Task 3: 其余 repo 新增方法（Entity/History/ActiveSnapshot/MemoryRepo）

**Files:**

- Modify: `internal/memory/domain/errors.go`（+`ErrSummaryNotFound`、`ErrSnapshotNotFound`）
- Create: `internal/memory/domain/memory_entry_list.go`
- Modify: `internal/memory/domain/port/entity_repo.go`、`port/history_repo.go`、`port/active_snapshot_repo.go`、`port/memory_repo.go`
- Modify: `internal/memory/infrastructure/persistence/entity_repo.go`、`history_repo.go`、`active_snapshot_repo.go`、`memory_repo.go`
- Modify: `internal/memory/application/memory_service_v2_test.go`（`MockEntityRepo` +`Delete`；`cleanupMemoryRepo` +`ListUserEntries`/`CountUserEntries`）
- Test: 新建 `internal/memory/infrastructure/persistence/entity_repo_mock_test.go`、`history_repo_mock_test.go`、`active_snapshot_repo_mock_test.go`、`memory_repo_mock_test.go`

**Interfaces:**

- Consumes: `domain.HistorySegment`（既有 18 字段）、`domain.ActiveSnapshot`、`domain.ErrEntityNotFound`、`translatePgError`（fact_repo.go:635，包级）。
- Produces:
  - `domain.MemoryEntryListItem{ID, Role, Content, Type, Scope string; Importance float64; CreatedAt time.Time; ExpiresAt *time.Time}`
  - `port.EntityRepo.Delete(ctx, tenantID, id string) error`
  - `port.HistoryRepo.ListUserSummaries(ctx, tenantID, userID string, limit, offset int) ([]*domain.HistorySegment, error)`、`CountUserSummaries(ctx, tenantID, userID string) (int, error)`、`Delete(ctx, tenantID, userID, id string) error`
  - `port.ActiveSnapshotRepo.ListUser(ctx, tenantID, userID string) ([]*domain.ActiveSnapshot, error)`
  - `port.MemoryRepo.ListUserEntries(ctx, tenantID, userID string, limit, offset int, query string) ([]*domain.MemoryEntryListItem, error)`、`CountUserEntries(ctx, tenantID, userID, query string) (int, error)`
  - 错误：`domain.ErrSummaryNotFound`、`domain.ErrSnapshotNotFound`（Task 5 service 使用，Task 6 映射 404）。

- [ ] **Step 1: 写失败测试（EntityRepo.Delete + MemoryRepo.ListUserEntries）**

新建 `internal/memory/infrastructure/persistence/entity_repo_mock_test.go`：

```go
package persistence

import (
 "context"
 "errors"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/jackc/pgxmock"
 "github.com/stretchr/testify/require"
)

func TestEntityRepo_Delete(t *testing.T) {
 mock, err := pgxmock.NewPool()
 require.NoError(t, err)
 defer mock.Close()
 repo := NewEntityRepo(mock)

 mock.ExpectExec("DELETE FROM memory_entities WHERE id").
  WithArgs("entity-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
 require.NoError(t, repo.Delete(context.Background(), "tenant-1", "entity-1"))

 mock.ExpectExec("DELETE FROM memory_entities WHERE id").
  WithArgs("entity-2").WillReturnResult(pgxmock.NewResult("DELETE", 0))
 require.ErrorIs(t, repo.Delete(context.Background(), "tenant-1", "entity-2"), domain.ErrEntityNotFound)
 require.NoError(t, mock.ExpectationsWereMet())
}
```

> 说明：`NewEntityRepo(mock)` 要求 `mock` 满足 `pgxpool.Pool` 形状；`pgxmock.Pool` 满足 `tenantPool` 接口（仅需 `Begin`）。若类型不匹配，测试改为新建 `&EntityRepo{pool: mock}` 并复用 `pgxmock.NewPool()`——以实际编译为准，目标是通过。

在 `internal/memory/infrastructure/persistence/fact_repo_mock_test.go` 末尾追加 `MemoryRepo` 测试（`MemoryRepo` 的 `execTenant` 走 `pgstore.ExecTenantWith`，会执行 `SET LOCAL search_path` —— pgxmock 需用 `MatchAny` 放宽期望）：

```go
func TestMemoryRepo_ListUserEntries(t *testing.T) {
 mock, _ := newFactMock(t) // pgxmock.PgxPoolIface，复用既有 helper
 repo := NewMemoryRepo(mock)
 ts := ts()
 rows := pgxmock.NewRows([]string{"id", "role", "content", "type", "scope", "importance", "created_at", "expires_at"}).
  AddRow("entry-1", "user", "hello world", "message", "user", 0.6, ts, ts)

 mock.ExpectQuery("SELECT id, role, content").
  WithArgs("user-1", "%hello%", 10, 0).
  WillReturnRows(rows)

 got, err := repo.ListUserEntries(context.Background(), "tenant-1", "user-1", 10, 0, "hello")
 require.NoError(t, err)
 require.Len(t, got, 1)
 require.Equal(t, "entry-1", got[0].ID)
 require.Equal(t, "user", got[0].Scope)
 require.NotNil(t, got[0].ExpiresAt)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/memory/infrastructure/persistence/ -run "TestEntityRepo_Delete|TestMemoryRepo_ListUserEntries" -count=1`
Expected: FAIL（编译失败，方法未定义）。若 pgxmock 期望与实现不符，先跳过此步的通过验证，Step 5 实现后再回来校准 SQL 匹配串。

- [ ] **Step 3: 新增 domain 类型与 sentinel**

在 `internal/memory/domain/errors.go` 追加：

```go
 // ErrSummaryNotFound is returned when a history summary lookup misses.
 ErrSummaryNotFound = errors.New("memory summary not found")
 // ErrSnapshotNotFound is returned when an active snapshot lookup misses.
 ErrSnapshotNotFound = errors.New("memory snapshot not found")
```

新建 `internal/memory/domain/memory_entry_list.go`：

```go
package domain

import "time"

// MemoryEntryListItem 是管理页原始条目列表项（区别于写入路径的 MemoryEntry：
// memory_entries 表含 scope/created_at 等只读列，而 v1 MemoryEntry 未携带）。
type MemoryEntryListItem struct {
 ID         string
 Role       string
 Content    string
 Type       string
 Scope      string
 Importance float64
 CreatedAt  time.Time
 ExpiresAt  *time.Time // 可空：条目可无过期时间
}
```

- [ ] **Step 4: 扩展 4 个 port 接口**

`port/entity_repo.go` 追加：

```go
 // Delete removes a single entity by id.
 Delete(ctx context.Context, tenantID, id string) error
```

`port/history_repo.go` 追加：

```go
 // ListUserSummaries lists the user's active user-scope summaries, newest first.
 ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.HistorySegment, error)
 // CountUserSummaries counts the user's active user-scope summaries.
 CountUserSummaries(ctx context.Context, tenantID, userID string) (int, error)
 // Delete removes a summary owned by userID; ErrSummaryNotFound when no row matches.
 Delete(ctx context.Context, tenantID, userID, id string) error
```

`port/active_snapshot_repo.go` 追加：

```go
 // ListUser lists every snapshot row for a user across agents (含过期/inactive，
 // 管理页需展示并允许清空；区别于注入路径 Get 的活跃过滤)。
 ListUser(ctx context.Context, tenantID, userID string) ([]*domain.ActiveSnapshot, error)
```

`port/memory_repo.go` 追加：

```go
 // ListUserEntries lists the user's user-scope raw entries with optional content
 // match (query) and pagination.
 ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*domain.MemoryEntryListItem, error)
 // CountUserEntries counts rows matching the same filter (pagination total).
 CountUserEntries(ctx context.Context, tenantID, userID, query string) (int, error)
```

- [ ] **Step 5: 实现 persistence 4 个文件**

`entity_repo.go` 在 `DeleteAllByAgent` 后追加：

```go
func (r *EntityRepo) Delete(ctx context.Context, tenantID, id string) error {
 return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx, `DELETE FROM memory_entities WHERE id = $1`, id)
  if err != nil {
   return translatePgError(err, "delete entity")
  }
  if tag.RowsAffected() == 0 {
   return domain.ErrEntityNotFound
  }
  return nil
 })
}
```

`history_repo.go` 在 `SearchRelevant` 后追加：

```go
const historyListUserQuery = `
 SELECT id, conversation_id, user_id, agent_id, scope, summary, tier,
  period_start, period_end, source_start, source_end, source_ids,
  importance, confidence, aggregation_key, status, created_at, updated_at
 FROM memory_summaries
 WHERE user_id = $1 AND scope = 'user' AND status = 'active'
 ORDER BY created_at DESC LIMIT $2 OFFSET $3`

const historyCountUserQuery = `
 SELECT count(*) FROM memory_summaries
 WHERE user_id = $1 AND scope = 'user' AND status = 'active'`

func (r *HistoryRepo) ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.HistorySegment, error) {
 var out []*domain.HistorySegment
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, historyListUserQuery, userID, limit, offset)
  if err != nil {
   return fmt.Errorf("list user summaries: %w", err)
  }
  defer rows.Close()
  for rows.Next() {
   var h domain.HistorySegment
   var scope string
   if err := rows.Scan(&h.ID, &h.ConversationID, &h.UserID, &h.AgentID, &scope, &h.Summary, &h.Tier,
    &h.PeriodStart, &h.PeriodEnd, &h.SourceStart, &h.SourceEnd, &h.SourceIDs,
    &h.Importance, &h.Confidence, &h.AggregationKey, &h.Status, &h.CreatedAt, &h.UpdatedAt); err != nil {
    return err
   }
   h.Scope = domain.Scope(scope)
   h.TenantID = tenantID
   out = append(out, &h)
  }
  return rows.Err()
 })
 return out, err
}

func (r *HistoryRepo) CountUserSummaries(ctx context.Context, tenantID, userID string) (int, error) {
 var total int
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx, historyCountUserQuery, userID).Scan(&total)
 })
 return total, err
}

func (r *HistoryRepo) Delete(ctx context.Context, tenantID, userID, id string) error {
 return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx, `DELETE FROM memory_summaries WHERE id = $1 AND user_id = $2`, id, userID)
  if err != nil {
   return translatePgError(err, "delete summary")
  }
  if tag.RowsAffected() == 0 {
   return domain.ErrSummaryNotFound
  }
  return nil
 })
}
```

`active_snapshot_repo.go` 在 `Delete` 后追加（复用同文件 `execTenant`；`source` JSONB 先 `Scan` 到 `[]byte` 再 `json.Unmarshal`）：

```go
func (r *ActiveSnapshotRepo) ListUser(ctx context.Context, tenantID, userID string) ([]*domain.ActiveSnapshot, error) {
 var out []*domain.ActiveSnapshot
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, `SELECT user_id, agent_id, work_context, personal_context, top_of_mind,
   source, expires_at, updated_at, version, status
   FROM memory_active_snapshots WHERE user_id = $1 ORDER BY updated_at DESC`, userID)
  if err != nil {
   return fmt.Errorf("list user snapshots: %w", err)
  }
  defer rows.Close()
  for rows.Next() {
   var s domain.ActiveSnapshot
   var source []byte
   if err := rows.Scan(&s.UserID, &s.AgentID, &s.WorkContext, &s.PersonalContext, &s.TopOfMind,
    &source, &s.ExpiresAt, &s.UpdatedAt, &s.Version, &s.Status); err != nil {
    return err
   }
   if err := json.Unmarshal(source, &s.Source); err != nil {
    return fmt.Errorf("memory: decode active snapshot source: %w", err)
   }
   s.TenantID = tenantID
   out = append(out, &s)
  }
  return rows.Err()
 })
 return out, err
}
```

`memory_repo.go` 在 `GetSummary` 后追加（`expires_at` 可空 → scan 到 `*time.Time`；`content ILIKE` 复用 `Search` 机制）：

```go
// entryListWhere 构造 ListUserEntries/CountUserEntries 的 WHERE 与参数（$N 绑定）。
func entryListWhere(userID, query string) (string, []any) {
 clauses := []string{"user_id = $1", "scope = 'user'"}
 args := []any{userID}
 if query != "" {
  clauses = append(clauses, fmt.Sprintf("content ILIKE $%d", len(args)+1))
  args = append(args, "%"+query+"%")
 }
 return strings.Join(clauses, " AND "), args
}

func (r *MemoryRepo) ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*domain.MemoryEntryListItem, error) {
 where, args := entryListWhere(userID, query)
 sql := `SELECT id, role, content, type, scope, importance, created_at, expires_at
  FROM memory_entries WHERE ` + where +
  ` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1) +
  ` OFFSET $` + fmt.Sprintf("%d", len(args)+2)
 args = append(args, limit, offset)

 var out []*domain.MemoryEntryListItem
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, sql, args...)
  if err != nil {
   return fmt.Errorf("list user entries: %w", err)
  }
  defer rows.Close()
  for rows.Next() {
   var e domain.MemoryEntryListItem
   if err := rows.Scan(&e.ID, &e.Role, &e.Content, &e.Type, &e.Scope, &e.Importance, &e.CreatedAt, &e.ExpiresAt); err != nil {
    return err
   }
   out = append(out, &e)
  }
  return rows.Err()
 })
 return out, err
}

func (r *MemoryRepo) CountUserEntries(ctx context.Context, tenantID, userID, query string) (int, error) {
 where, args := entryListWhere(userID, query)
 var total int
 err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx, `SELECT count(*) FROM memory_entries WHERE `+where, args...).Scan(&total)
 })
 return total, err
}
```

> 需要给 `memory_repo.go` 补 `fmt`、`strings` import（现有 import 已含 `fmt`，缺 `strings` 时追加）。

- [ ] **Step 6: 同步 application 测试 mock**

在 `internal/memory/application/memory_service_v2_test.go`：

`MockEntityRepo` 的 `DeleteAllByAgent` 后追加：

```go
func (m *MockEntityRepo) Delete(ctx context.Context, tenantID, id string) error {
 args := m.Called(ctx, tenantID, id)
 return args.Error(0)
}
```

`cleanupMemoryRepo` 的 `GetSummary` 后追加（`strings` 若未 import 则追加；`ListUserEntries` 返回空切片即可，Task 5 的 entries 测试会改用带 mock 方法的 stub）：

```go
func (m *cleanupMemoryRepo) ListUserEntries(context.Context, string, string, int, int, string) ([]*domain.MemoryEntryListItem, error) {
 return nil, nil
}
func (m *cleanupMemoryRepo) CountUserEntries(context.Context, string, string, string) (int, error) {
 return 0, nil
}
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/memory/... -count=1`
Expected: PASS。若 pgxmock 期望与 `execTenant` 内部 `SET LOCAL search_path` 不匹配导致失败，改用 `mock.ExpectQuery(...).WithArgs(...).WillReturnRows(rows)` 去掉对 `$N` 精确参数的强依赖（`pgxmock` 允许只校验首条语句；如仍失败，用 `WithArgs(anyArgs(n)...)` 并核对实际参数顺序——以编译与 SQL 为准）。

- [ ] **Step 8: 提交**

```bash
git add internal/memory/domain/ internal/memory/infrastructure/persistence/
git commit -m "feat(memory): add repo list/delete methods for entities summaries snapshots entries"
```

---

### Task 4: service 层——事实管理（含向量同步）

**Files:**

- Modify: `internal/memory/application/types.go`（+`ErrMemoryEmbeddingUnavailable`）
- Modify: `internal/memory/application/memory_service_v2.go`（UserMemory 扩展 + DTO 类型 + `resolveEmbedClient` + `factVectorMetadata` + 5 个方法）
- Modify: `internal/memory/application/memory_service_v2_test.go`（MockEmbedClient 已存在；新增 facts 方法测试）

**Interfaces:**

- Consumes: Task 1 `gen`（无直接依赖，handler 层才用）；Task 2 的 `domain.FactListFilter`、`ListUserFactsFiltered/CountUserFactsFiltered`、`ErrImportanceOutOfRange/ErrFactNotEditable/ErrEmptyFactPatch`、`IsValidFactCategory`；Task 3 的 `EntityRepo.Delete`；既有 `VectorStore`（`Upsert/DeleteFactVectors`）、`embedClientResolver`、`factsCollectionName`、`constants.DefaultPageSize`。
- Produces:
  - `UserMemory` 新增字段 `Category/Source/Status string`、`Confidence float64`
  - `UserFactDetail{ID, Scope, Content, Category, Source, Status string; Importance, Confidence float64; CreatedAt, UpdatedAt time.Time}` 与 `userFactDetailFromFact(*domain.MemoryFact) *UserFactDetail`
  - `ListUserFactsFilteredRequest{TenantID, UserID, Query, Category string; ImportanceMin, ImportanceMax *float64; Limit, Offset int}`
  - `UpdateUserFactPatch{Content *string; Importance *float64; Category *string}`
  - `MemoryService.ListUserFactsFiltered(ctx, *ListUserFactsFilteredRequest) ([]*UserFactDetail, int, error)`
  - `MemoryService.GetUserFact(ctx, tenantID, userID, factID string) (*UserFactDetail, error)`
  - `MemoryService.UpdateUserFact(ctx, tenantID, userID, factID string, patch *UpdateUserFactPatch) (*UserFactDetail, bool, error)` —— bool=向量 upsert 失败（PG 已提交）
  - `MemoryService.DeleteUserFact(ctx, tenantID, userID, factID string) error`
  - `MemoryService.DeleteUserEntity(ctx, tenantID, userID, entityID string) error`
  - `application.ErrMemoryEmbeddingUnavailable`（嵌入未配置/失败 → 502，fail-closed）
  - Task 5 service 复用 `UserFactDetail` 与 `userFactDetailFromFact`；Task 6 handler 复用全部方法与 `UserFactDetail`。

- [ ] **Step 1: 写失败测试**

在 `internal/memory/application/memory_service_v2_test.go` 追加（复用既有 `newTestMemoryService` 风格的构造；该文件已有 `MockFactRepo/MockVectorStore/MockEntityRepo/MockEmbedClient`）：

```go
func newFactSvc() (*MemoryService, *MockFactRepo, *MockVectorStore, *MockEntityRepo, *MockEmbedClient) {
 facts, vectors, entities := new(MockFactRepo), new(MockVectorStore), new(MockEntityRepo)
 svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
 embed := new(MockEmbedClient)
 svc.SetEmbedClientResolver(func(context.Context, string) port.EmbedClient { return embed })
 return svc, facts, vectors, entities, embed
}

func TestMemoryService_UpdateUserFact(t *testing.T) {
 t.Run("success re-embeds and syncs vectors", func(t *testing.T) {
  ctx := context.Background()
  svc, facts, vectors, _, embed := newFactSvc()
  fact := &domain.MemoryFact{ID: "fact-1", UserID: "user-1", Content: "I prefer dark mode",
   Importance: 0.8, Category: "preference", Status: domain.FactStatusActive}
  newContent := "I prefer light mode"
  facts.On("GetByID", ctx, "tenant-1", "fact-1").Return(fact, nil).Once()
  embed.On("Embed", ctx, newContent).Return([]float32{0.1, 0.2}, nil).Once()
  embed.On("Model").Return("text-embedding-v3").Once()
  vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(nil).Once()
  facts.On("Update", ctx, mock.Anything).Return(nil).Once()
  vectors.On("Upsert", ctx, "memory_facts_tenant-1_text-embedding-v3", mock.Anything).Return(nil).Once()

  got, vectorSyncFailed, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1",
   &UpdateUserFactPatch{Content: &newContent})
  require.NoError(t, err)
  require.False(t, vectorSyncFailed)
  require.Equal(t, newContent, got.Content)
 })

 t.Run("rejects empty patch", func(t *testing.T) {
  svc, _, _, _, _ := newFactSvc()
  _, _, err := svc.UpdateUserFact(context.Background(), "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{})
  require.ErrorIs(t, err, domain.ErrEmptyFactPatch)
 })

 t.Run("rejects other users fact as 404", func(t *testing.T) {
  ctx := context.Background()
  svc, facts, _, _, _ := newFactSvc()
  facts.On("GetByID", ctx, "tenant-1", "fact-1").
   Return(&domain.MemoryFact{ID: "fact-1", UserID: "other-user"}, nil).Once()
  content := "x"
  _, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
  require.ErrorIs(t, err, domain.ErrFactNotFound)
 })

 t.Run("rejects non-active fact as 409", func(t *testing.T) {
  ctx := context.Background()
  svc, facts, _, _, _ := newFactSvc()
  facts.On("GetByID", ctx, "tenant-1", "fact-1").
   Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Status: domain.FactStatusSuperseded}, nil).Once()
  content := "x"
  _, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
  require.ErrorIs(t, err, domain.ErrFactNotEditable)
 })

 t.Run("fails closed when embedder unavailable", func(t *testing.T) {
  ctx := context.Background()
  facts := new(MockFactRepo)
  svc := NewMemoryService(facts, new(MockEntityRepo), nil, new(MockVectorStore), nil, nil, nil, nil)
  facts.On("GetByID", ctx, "tenant-1", "fact-1").
   Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Status: domain.FactStatusActive}, nil).Once()
  content := "x"
  _, _, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
  require.ErrorIs(t, err, ErrMemoryEmbeddingUnavailable)
 })

 t.Run("reports vector sync failure but keeps pg change", func(t *testing.T) {
  ctx := context.Background()
  svc, facts, vectors, _, embed := newFactSvc()
  facts.On("GetByID", ctx, "tenant-1", "fact-1").
   Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1", Content: "old", Status: domain.FactStatusActive}, nil).Once()
  embed.On("Embed", ctx, "new").Return([]float32{0.1}, nil).Once()
  embed.On("Model").Return("text-embedding-v3").Once()
  vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(nil).Once()
  facts.On("Update", ctx, mock.Anything).Return(nil).Once()
  vectors.On("Upsert", ctx, "memory_facts_tenant-1_text-embedding-v3", mock.Anything).Return(errors.New("milvus down")).Once()

  content := "new"
  got, vectorSyncFailed, err := svc.UpdateUserFact(ctx, "tenant-1", "user-1", "fact-1", &UpdateUserFactPatch{Content: &content})
  require.NoError(t, err)
  require.True(t, vectorSyncFailed)
  require.Equal(t, "new", got.Content)
 })
}

func TestMemoryService_DeleteUserFact(t *testing.T) {
 t.Run("deletes fact and best-effort clears vectors", func(t *testing.T) {
  ctx := context.Background()
  svc, facts, vectors, _, _ := newFactSvc()
  facts.On("GetByID", ctx, "tenant-1", "fact-1").
   Return(&domain.MemoryFact{ID: "fact-1", UserID: "user-1"}, nil).Once()
  facts.On("Delete", ctx, "tenant-1", "fact-1").Return(nil).Once()
  // 向量清理失败不阻塞主操作。
  vectors.On("DeleteFactVectors", ctx, "tenant-1", []string{"fact-1"}).Return(errors.New("milvus down")).Once()
  require.NoError(t, svc.DeleteUserFact(ctx, "tenant-1", "user-1", "fact-1"))
 })
}

func TestMemoryService_ListUserFactsFiltered(t *testing.T) {
 ctx := context.Background()
 svc, facts, _, _, _ := newFactSvc()
 filter := domain.FactListFilter{Query: "dark"}
 facts.On("ListUserFactsFiltered", ctx, "tenant-1", "user-1", filter, 20, 0).
  Return([]*domain.MemoryFact{{ID: "fact-1", UserID: "user-1", Category: "preference", Status: "active"}}, nil).Once()
 facts.On("CountUserFactsFiltered", ctx, "tenant-1", "user-1", filter).Return(1, nil).Once()

 got, total, err := svc.ListUserFactsFiltered(ctx, &ListUserFactsFilteredRequest{TenantID: "tenant-1", UserID: "user-1", Query: "dark", Limit: 20})
 require.NoError(t, err)
 require.Equal(t, 1, total)
 require.Len(t, got, 1)
 require.Equal(t, "fact-1", got[0].ID)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/memory/application/ -run "TestMemoryService_UpdateUserFact|TestMemoryService_DeleteUserFact|TestMemoryService_ListUserFactsFiltered" -count=1`
Expected: FAIL（方法未定义；`ErrMemoryEmbeddingUnavailable` 未定义）。

- [ ] **Step 3: 新增 `ErrMemoryEmbeddingUnavailable`**

在 `internal/memory/application/types.go` 的 `ErrNotFound` 后追加：

```go
// ErrMemoryEmbeddingUnavailable 表示嵌入模型未配置或调用失败（spec §5：嵌入
// 失败 fail-closed 返回 502，不写任何数据）。errors.Is 可用于 handle 路径。
var ErrMemoryEmbeddingUnavailable = errors.New("memory embedding unavailable")
```

（`types.go` 需补 `errors` import。）

- [ ] **Step 4: 扩展 `UserMemory` 并新增 DTO 类型**

在 `internal/memory/application/memory_service_v2.go` 的 `UserMemory`（第 246 行）后追加字段：

```go
type UserMemory struct {
 ID         string
 Scope      string
 Content    string
 Importance float64
 CreatedAt  time.Time
 UpdatedAt  time.Time
 Category   string
 Confidence float64
 Source     string
 Status     string
}
```

同步 `userMemoryFromFact` 填新字段（在 `UserMemory` 定义后）：

```go
func userMemoryFromFact(fact *domain.MemoryFact) *UserMemory {
 return &UserMemory{
  ID: fact.ID, Scope: string(fact.Scope), Content: fact.Content,
  Importance: fact.Importance, CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
  Category: fact.Category, Confidence: fact.Confidence, Source: fact.Source,
  Status: fact.Status,
 }
}
```

在 `UserMemoryEntity` 定义后追加：

```go
// UserFactDetail 事实详情（管理页展示 / 编辑返回，字段与 gen.MemoryFactResponse 对齐）。
type UserFactDetail struct {
 ID         string
 Scope      string
 Content    string
 Category   string
 Source     string
 Status     string
 Importance float64
 Confidence float64
 CreatedAt  time.Time
 UpdatedAt  time.Time
}

func userFactDetailFromFact(fact *domain.MemoryFact) *UserFactDetail {
 return &UserFactDetail{
  ID: fact.ID, Scope: string(fact.Scope), Content: fact.Content,
  Category: fact.Category, Source: fact.Source, Status: fact.Status,
  Importance: fact.Importance, Confidence: fact.Confidence,
  CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
 }
}

// ListUserFactsFilteredRequest 事实列表查询（GET /memory/facts）。
type ListUserFactsFilteredRequest struct {
 TenantID      string
 UserID        string
 Query         string
 ImportanceMin *float64
 ImportanceMax *float64
 Category      string
 Limit         int
 Offset        int
}

// UpdateUserFactPatch 事实编辑补丁，至少一项（PATCH /memory/facts/:id）。
type UpdateUserFactPatch struct {
 Content    *string
 Importance *float64
 Category   *string
}
```

- [ ] **Step 5: 新增 embed 辅助函数**

在 `currentEmbedModel` 后追加（供 `UpdateUserFact` 与后续复用；`embedAndStoreFactVector` 保持原样不动以避免引入回归）：

```go
// resolveEmbedClient 返回 tenant 的嵌入客户端；未配置返回 ErrMemoryEmbeddingUnavailable
// （fail-closed：编辑前必须先能嵌入，否则拒绝写数据，spec §3 第 2 步）。
func (s *MemoryService) resolveEmbedClient(ctx context.Context, tenantID string) (port.EmbedClient, error) {
 embedder := s.embedClient
 if embedder == nil && s.embedClientResolver != nil {
  embedder = s.embedClientResolver(ctx, tenantID)
 }
 if embedder == nil {
  return nil, ErrMemoryEmbeddingUnavailable
 }
 return embedder, nil
}

// factVectorMetadata 构造事实向量元数据，与提取路径 embedAndStoreFactVector 的
// key 集合保持一致（召回 filter 依赖这些 key）。
func factVectorMetadata(fact *domain.MemoryFact) map[string]interface{} {
 return map[string]interface{}{
  "user_id":         fact.UserID,
  "agent_id":        fact.AgentID,
  "conversation_id": fact.ConversationID,
  "scope":           string(fact.Scope),
  "content":         fact.Content,
  "importance":      fact.Importance,
  "category":        fact.Category,
  "confidence":      fact.Confidence,
  "source":          fact.Source,
 }
}
```

- [ ] **Step 6: 实现 5 个 service 方法**

在 `memory_service_v2.go` 的 `ListUserMemories` 定义后追加（`strings` 需加入 import）：

```go
func (s *MemoryService) ListUserFactsFiltered(ctx context.Context, req *ListUserFactsFilteredRequest) ([]*UserFactDetail, int, error) {
 limit := req.Limit
 if limit <= 0 {
  limit = constants.DefaultPageSize
 }
 filter := domain.FactListFilter{
  Query: req.Query, ImportanceMin: req.ImportanceMin,
  ImportanceMax: req.ImportanceMax, Category: req.Category,
 }
 facts, err := s.factRepo.ListUserFactsFiltered(ctx, req.TenantID, req.UserID, filter, limit, req.Offset)
 if err != nil {
  return nil, 0, fmt.Errorf("list user facts: %w", err)
 }
 total, err := s.factRepo.CountUserFactsFiltered(ctx, req.TenantID, req.UserID, filter)
 if err != nil {
  return nil, 0, fmt.Errorf("count user facts: %w", err)
 }
 out := make([]*UserFactDetail, 0, len(facts))
 for _, f := range facts {
  out = append(out, userFactDetailFromFact(f))
 }
 return out, total, nil
}

func (s *MemoryService) GetUserFact(ctx context.Context, tenantID, userID, factID string) (*UserFactDetail, error) {
 fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
 if err != nil {
  return nil, err
 }
 if fact.UserID != userID {
  return nil, domain.ErrFactNotFound // 归属不匹配一律 404，不泄露存在性
 }
 return userFactDetailFromFact(fact), nil
}

// UpdateUserFact 编辑事实并同步向量。顺序（spec §3）：校验 + GetByID + 归属 →
// 新内容嵌入（失败 502 不写数据）→ 删旧向量（best-effort）→ PG update →
// upsert 新向量（失败返回 vectorSyncFailed=true，PG 已提交）。
func (s *MemoryService) UpdateUserFact(ctx context.Context, tenantID, userID, factID string, patch *UpdateUserFactPatch) (*UserFactDetail, bool, error) {
 if patch == nil || (patch.Content == nil && patch.Importance == nil && patch.Category == nil) {
  return nil, false, domain.ErrEmptyFactPatch
 }
 fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
 if err != nil {
  return nil, false, err
 }
 if fact.UserID != userID {
  return nil, false, domain.ErrFactNotFound
 }
 if fact.Status != domain.FactStatusActive {
  // 仅 active 可编辑：superseded/archived 归提取管线管辖，避免冲突（spec §5）。
  return nil, false, domain.ErrFactNotEditable
 }
 next := *fact
 if patch.Content != nil {
  content := strings.TrimSpace(*patch.Content)
  if content == "" {
   return nil, false, domain.ErrEmptyContent
  }
  next.Content = content
 }
 if patch.Importance != nil {
  if *patch.Importance < 0 || *patch.Importance > 1 {
   return nil, false, domain.ErrImportanceOutOfRange
  }
  next.Importance = *patch.Importance
 }
 if patch.Category != nil {
  if !domain.IsValidFactCategory(*patch.Category) {
   return nil, false, domain.ErrInvalidCategory
  }
  next.Category = *patch.Category
 }
 next.UpdatedAt = time.Now().UTC()

 embedder, err := s.resolveEmbedClient(ctx, tenantID)
 if err != nil {
  return nil, false, err
 }
 vector, err := embedder.Embed(ctx, next.Content)
 if err != nil {
  return nil, false, fmt.Errorf("%w: %v", ErrMemoryEmbeddingUnavailable, err)
 }
 // 先删旧向量：陈旧内容立即停止被召回（best-effort，失败不阻塞主操作）。
 if s.vectorStore != nil {
  if err := s.vectorStore.DeleteFactVectors(ctx, tenantID, []string{next.ID}); err != nil {
   s.logger.Error("memory: delete old fact vectors failed", zap.String("fact_id", next.ID), zap.Error(err))
  }
 }
 if err := s.factRepo.Update(ctx, tenantID, &next); err != nil {
  return nil, false, err
 }
 // 后写新向量：同 ID（fact.ID 是 Milvus 主键）不会与旧向量互相覆盖。
 if s.vectorStore != nil {
  collection := factsCollectionName(tenantID, embedder.Model())
  doc := &port.VectorDoc{ID: next.ID, Embedding: vector, Metadata: factVectorMetadata(&next)}
  if err := s.vectorStore.Upsert(ctx, collection, []*port.VectorDoc{doc}); err != nil {
   s.logger.Error("memory: upsert fact vector failed", zap.String("fact_id", next.ID), zap.Error(err))
   return userFactDetailFromFact(&next), true, nil // 内容已保存，向量待后台补偿
  }
 }
 return userFactDetailFromFact(&next), false, nil
}

func (s *MemoryService) DeleteUserFact(ctx context.Context, tenantID, userID, factID string) error {
 fact, err := s.factRepo.GetByID(ctx, tenantID, factID)
 if err != nil {
  return err
 }
 if fact.UserID != userID {
  return domain.ErrFactNotFound
 }
 if err := s.factRepo.Delete(ctx, tenantID, factID); err != nil {
  return err
 }
 // PG 删除成功即操作成功；向量清理 best-effort，GC reconcile 兜底（spec §3）。
 if s.vectorStore != nil {
  if err := s.vectorStore.DeleteFactVectors(ctx, tenantID, []string{factID}); err != nil {
   s.logger.Error("memory: delete fact vectors failed", zap.String("fact_id", factID), zap.Error(err))
  }
 }
 return nil
}

func (s *MemoryService) DeleteUserEntity(ctx context.Context, tenantID, userID, entityID string) error {
 entity, err := s.entityRepo.GetByID(ctx, tenantID, entityID)
 if err != nil {
  return err
 }
 if entity.UserID != userID {
  return domain.ErrEntityNotFound
 }
 return s.entityRepo.Delete(ctx, tenantID, entityID)
}
```

> `constants` 与 `zap` 已在 `memory_service_v2.go` import；`strings` 需追加。若 `ListUserMemories` 在文件后面定义，追加位置以其声明之前为准（Go 不要求定义顺序，放哪都行，但保持调用方在前）。

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/memory/application/ -count=1`
Expected: PASS。若 `MockEmbedClient` 缺 `Embed`/`Model` 期望方法，核对现有定义（该文件已含 `Model()` 返回 `"text-embedding-v3"`）；`embed.On("Embed", ctx, newContent)` 若失败说明 `MemoryService.Embed` 传参含 ctx 包装，改 `embed.On("Embed", mock.Anything, newContent)`。

- [ ] **Step 8: 提交**

```bash
git add internal/memory/application/
git commit -m "feat(memory): add fact management service methods with vector sync"
```

---

### Task 5: service 层——摘要 / 快照 / 条目管理

**Files:**

- Modify: `internal/memory/application/memory_service_v2.go`（struct +`historyRepo`/`activeSnapshotRepo` + 2 个 setter + DTO + 7 个方法）
- Modify: `internal/memory/application/memory_service_v2_test.go`（+`MockHistoryRepo`/`MockActiveSnapshotRepo` + 新方法测试）

**Interfaces:**

- Consumes: Task 3 的 `HistoryRepo.ListUserSummaries/CountUserSummaries/Delete`、`ActiveSnapshotRepo.ListUser/Upsert/Delete`、`MemoryRepo.ListUserEntries/CountUserEntries/Get/Delete`、`ErrSummaryNotFound/ErrSnapshotNotFound`、`domain.MemoryEntryListItem`；`constants.ActiveSnapshotTTL`。
- Produces:
  - `MemoryService.SetHistoryRepo(r port.HistoryRepo)`、`SetActiveSnapshotRepo(r port.ActiveSnapshotRepo)`
  - `UserSummary{ID, Summary, Tier, ConversationID string; Importance float64; PeriodEnd, CreatedAt time.Time}`
  - `UserSnapshot{AgentID string; WorkContext, PersonalContext, TopOfMind []string; ExpiresAt, UpdatedAt time.Time; Status string}`
  - `UpdateUserSnapshotPatch{WorkContext, PersonalContext, TopOfMind []string}`
  - `UserEntry{ID, Role, Content, Type, Scope string; Importance float64; CreatedAt time.Time; ExpiresAt *time.Time}`
  - `MemoryService.ListUserSummaries(ctx, tenantID, userID string, limit, offset int) ([]*UserSummary, int, error)`
  - `MemoryService.DeleteUserSummary(ctx, tenantID, userID, summaryID string) error`
  - `MemoryService.ListUserSnapshots(ctx, tenantID, userID string) ([]*UserSnapshot, error)`
  - `MemoryService.UpdateUserSnapshot(ctx, tenantID, userID, agentID string, patch *UpdateUserSnapshotPatch) (*UserSnapshot, error)`
  - `MemoryService.DeleteUserSnapshot(ctx, tenantID, userID, agentID string) error`
  - `MemoryService.ListUserEntries(ctx, tenantID, userID string, limit, offset int, query string) ([]*UserEntry, int, error)`
  - `MemoryService.DeleteUserEntry(ctx, tenantID, userID, entryID string) error`
  - Task 6 handler 依赖以上全部签名与 DTO。

- [ ] **Step 1: 写失败测试**

在 `internal/memory/application/memory_service_v2_test.go` 追加（`MockHistoryRepo`/`MockActiveSnapshotRepo` 沿用 testify mock 风格）：

```go
type MockHistoryRepo struct{ mock.Mock }

func (m *MockHistoryRepo) NextBatch(context.Context, string, int, int) (*domain.HistoryBatch, error) {
 args := m.Called()
 return args.Get(0).(*domain.HistoryBatch), args.Error(1)
}
func (m *MockHistoryRepo) Upsert(context.Context, string, *domain.HistorySegment) error { return m.Called().Error(0) }
func (m *MockHistoryRepo) NextOverflow(context.Context, string, int, int, int) (*domain.HistoryOverflowGroup, error) {
 args := m.Called()
 return args.Get(0).(*domain.HistoryOverflowGroup), args.Error(1)
}
func (m *MockHistoryRepo) ReplaceOverflow(context.Context, string, *domain.HistorySegment, []string) error {
 return m.Called().Error(0)
}
func (m *MockHistoryRepo) Maintain(context.Context, string) error { return m.Called().Error(0) }
func (m *MockHistoryRepo) ArchiveColdFacts(context.Context, string) ([]string, error) {
 args := m.Called()
 return args.Get(0).([]string), args.Error(1)
}
func (m *MockHistoryRepo) SearchRelevant(context.Context, string, string, string, string, int) ([]domain.HistorySegment, error) {
 args := m.Called()
 return args.Get(0).([]domain.HistorySegment), args.Error(1)
}
func (m *MockHistoryRepo) ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.HistorySegment, error) {
 args := m.Called(ctx, tenantID, userID, limit, offset)
 return args.Get(0).([]*domain.HistorySegment), args.Error(1)
}
func (m *MockHistoryRepo) CountUserSummaries(ctx context.Context, tenantID, userID string) (int, error) {
 args := m.Called(ctx, tenantID, userID)
 return args.Int(0), args.Error(1)
}
func (m *MockHistoryRepo) Delete(ctx context.Context, tenantID, userID, id string) error {
 args := m.Called(ctx, tenantID, userID, id)
 return args.Error(0)
}

type MockActiveSnapshotRepo struct{ mock.Mock }

func (m *MockActiveSnapshotRepo) Upsert(context.Context, *domain.ActiveSnapshot) error {
 return m.Called().Error(0)
}
func (m *MockActiveSnapshotRepo) Get(context.Context, string, string, string) (*domain.ActiveSnapshot, error) {
 args := m.Called()
 return args.Get(0).(*domain.ActiveSnapshot), args.Error(1)
}
func (m *MockActiveSnapshotRepo) Delete(context.Context, string, string, string) error {
 return m.Called().Error(0)
}
func (m *MockActiveSnapshotRepo) ListUser(ctx context.Context, tenantID, userID string) ([]*domain.ActiveSnapshot, error) {
 args := m.Called(ctx, tenantID, userID)
 return args.Get(0).([]*domain.ActiveSnapshot), args.Error(1)
}

func TestMemoryService_ListUserSummaries(t *testing.T) {
 ctx := context.Background()
 facts, vectors, memories, entities := new(MockFactRepo), new(MockVectorStore), new(cleanupMemoryRepo), new(MockEntityRepo)
 svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
 histories := new(MockHistoryRepo)
 svc.SetHistoryRepo(histories)
 histories.On("ListUserSummaries", ctx, "tenant-1", "user-1", 20, 0).
  Return([]*domain.HistorySegment{{ID: "s-1", Summary: "user likes dark mode", Tier: "recent_months"}}, nil).Once()
 histories.On("CountUserSummaries", ctx, "tenant-1", "user-1").Return(1, nil).Once()

 got, total, err := svc.ListUserSummaries(ctx, "tenant-1", "user-1", 20, 0)
 require.NoError(t, err)
 require.Equal(t, 1, total)
 require.Len(t, got, 1)
 require.Equal(t, "s-1", got[0].ID)
}

func TestMemoryService_DeleteUserSummary(t *testing.T) {
 ctx := context.Background()
 facts, vectors, memories, entities := new(MockFactRepo), new(MockVectorStore), new(cleanupMemoryRepo), new(MockEntityRepo)
 svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
 histories := new(MockHistoryRepo)
 svc.SetHistoryRepo(histories)
 histories.On("Delete", ctx, "tenant-1", "user-1", "s-1").Return(nil).Once()
 require.NoError(t, svc.DeleteUserSummary(ctx, "tenant-1", "user-1", "s-1"))
}

func TestMemoryService_UpdateUserSnapshot(t *testing.T) {
 ctx := context.Background()
 facts, vectors, memories, entities := new(MockFactRepo), new(MockVectorStore), new(cleanupMemoryRepo), new(MockEntityRepo)
 svc := NewMemoryService(facts, entities, nil, vectors, nil, nil, nil, nil)
 snapshots := new(MockActiveSnapshotRepo)
 svc.SetActiveSnapshotRepo(snapshots)
 snapshots.On("ListUser", ctx, "tenant-1", "user-1").
  Return([]*domain.ActiveSnapshot{{AgentID: "agent-1",
   Source: domain.ActiveSnapshotSource{Type: "reflection", Reference: "conv-1"}}}, nil).Once()
 snapshots.On("Upsert", ctx, mock.Anything).Return(nil).Once()

 got, err := svc.UpdateUserSnapshot(ctx, "tenant-1", "user-1", "agent-1",
  &UpdateUserSnapshotPatch{WorkContext: []string{"building feature x"}})
 require.NoError(t, err)
 require.Equal(t, "agent-1", got.AgentID)
 require.Equal(t, "reflection", got.Status) // 占位断言，见 Step 6 实现
}
```

> 若 `domain.ActiveSnapshotSource` 字段名与上述不一致，以 `internal/memory/domain/active_snapshot.go` 实际定义为准（`Type`/`Reference`，validate 要求两者非空）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/memory/application/ -run "TestMemoryService_ListUserSummaries|TestMemoryService_DeleteUserSummary|TestMemoryService_UpdateUserSnapshot" -count=1`
Expected: FAIL（`SetHistoryRepo`/`SetActiveSnapshotRepo`/新方法未定义）。

- [ ] **Step 3: 给 `MemoryService` 加字段 + setter**

在 `memory_service_v2.go` 的 struct 内（`judgeResolver` 之后）追加：

```go
 historyRepo        port.HistoryRepo
 activeSnapshotRepo port.ActiveSnapshotRepo
```

在 `SetTrajectoryReflectorResolver` 后追加：

```go
// SetHistoryRepo wires the history repo for user-facing summary management
// (called during wiring; summaries are otherwise only touched by workers).
func (s *MemoryService) SetHistoryRepo(r port.HistoryRepo) { s.historyRepo = r }

// SetActiveSnapshotRepo wires the active snapshot repo for user-facing
// snapshot management.
func (s *MemoryService) SetActiveSnapshotRepo(r port.ActiveSnapshotRepo) { s.activeSnapshotRepo = r }
```

- [ ] **Step 4: 新增 DTO 类型**

在 `UpdateUserFactPatch` 定义后追加：

```go
// UserSummary 历史摘要条目（管理页只读 + 删除）。
type UserSummary struct {
 ID             string
 Summary        string
 Tier           string
 ConversationID string
 Importance     float64
 PeriodEnd      time.Time
 CreatedAt      time.Time
}

// UserSnapshot 活跃快照（每 (user_id, agent_id) 一条，管理页展示/编辑/清空）。
type UserSnapshot struct {
 AgentID         string
 WorkContext     []string
 PersonalContext []string
 TopOfMind       []string
 ExpiresAt       time.Time
 UpdatedAt       time.Time
 Status          string
}

// UpdateUserSnapshotPatch 快照编辑（三段数组整体替换）。
type UpdateUserSnapshotPatch struct {
 WorkContext     []string
 PersonalContext []string
 TopOfMind       []string
}

// UserEntry 原始条目（管理页只读 + 删除）。
type UserEntry struct {
 ID         string
 Role       string
 Content    string
 Type       string
 Scope      string
 Importance float64
 CreatedAt  time.Time
 ExpiresAt  *time.Time
}
```

- [ ] **Step 5: 实现 7 个 service 方法**

在 `DeleteUserEntity` 后追加：

```go
func (s *MemoryService) ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*UserSummary, int, error) {
 if s.historyRepo == nil {
  return nil, 0, fmt.Errorf("memory: history repo not wired")
 }
 segments, err := s.historyRepo.ListUserSummaries(ctx, tenantID, userID, limit, offset)
 if err != nil {
  return nil, 0, fmt.Errorf("list user summaries: %w", err)
 }
 total, err := s.historyRepo.CountUserSummaries(ctx, tenantID, userID)
 if err != nil {
  return nil, 0, fmt.Errorf("count user summaries: %w", err)
 }
 out := make([]*UserSummary, 0, len(segments))
 for _, h := range segments {
  out = append(out, &UserSummary{
   ID: h.ID, Summary: h.Summary, Tier: h.Tier,
   ConversationID: h.ConversationID, Importance: h.Importance,
   PeriodEnd: h.PeriodEnd, CreatedAt: h.CreatedAt,
  })
 }
 return out, total, nil
}

func (s *MemoryService) DeleteUserSummary(ctx context.Context, tenantID, userID, summaryID string) error {
 if s.historyRepo == nil {
  return fmt.Errorf("memory: history repo not wired")
 }
 return s.historyRepo.Delete(ctx, tenantID, userID, summaryID)
}

func (s *MemoryService) ListUserSnapshots(ctx context.Context, tenantID, userID string) ([]*UserSnapshot, error) {
 if s.activeSnapshotRepo == nil {
  return nil, fmt.Errorf("memory: active snapshot repo not wired")
 }
 snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
 if err != nil {
  return nil, fmt.Errorf("list user snapshots: %w", err)
 }
 out := make([]*UserSnapshot, 0, len(snapshots))
 for _, sn := range snapshots {
  out = append(out, userSnapshotFromDomain(sn))
 }
 return out, nil
}

// UpdateUserSnapshot 编辑快照三段内容。保留既有 Source（注入/反思来源），
// 重置 expires_at 为 now+TTL 并以 UpdatedAt=now 绕过 Upsert 的覆盖守卫（用户显式
// 操作优先，spec §2 归属校验 + §5 边界）。快照不存在时 404。
func (s *MemoryService) UpdateUserSnapshot(ctx context.Context, tenantID, userID, agentID string, patch *UpdateUserSnapshotPatch) (*UserSnapshot, error) {
 if s.activeSnapshotRepo == nil {
  return nil, fmt.Errorf("memory: active snapshot repo not wired")
 }
 if patch == nil {
  return nil, domain.ErrSnapshotNotFound // 防御：无 body 视为不存在
 }
 snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
 if err != nil {
  return nil, err
 }
 var existing *domain.ActiveSnapshot
 for _, sn := range snapshots {
  if sn.AgentID == agentID {
   existing = sn
   break
  }
 }
 if existing == nil {
  return nil, domain.ErrSnapshotNotFound
 }
 now := time.Now().UTC()
 next := domain.ActiveSnapshot{
  TenantID: tenantID, UserID: userID, AgentID: agentID,
  WorkContext: patch.WorkContext, PersonalContext: patch.PersonalContext, TopOfMind: patch.TopOfMind,
  Source: existing.Source, ExpiresAt: now.Add(constants.ActiveSnapshotTTL),
  UpdatedAt: now, Status: domain.SnapshotStatusActive,
 }
 if err := next.Validate(); err != nil {
  return nil, err
 }
 if err := s.activeSnapshotRepo.Upsert(ctx, &next); err != nil {
  return nil, err
 }
 return userSnapshotFromDomain(&next), nil
}

func (s *MemoryService) DeleteUserSnapshot(ctx context.Context, tenantID, userID, agentID string) error {
 if s.activeSnapshotRepo == nil {
  return fmt.Errorf("memory: active snapshot repo not wired")
 }
 snapshots, err := s.activeSnapshotRepo.ListUser(ctx, tenantID, userID)
 if err != nil {
  return err
 }
 found := false
 for _, sn := range snapshots {
  if sn.AgentID == agentID {
   found = true
   break
  }
 }
 if !found {
  return domain.ErrSnapshotNotFound
 }
 return s.activeSnapshotRepo.Delete(ctx, tenantID, userID, agentID)
}

func (s *MemoryService) ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*UserEntry, int, error) {
 entries, err := s.memoryRepo.ListUserEntries(ctx, tenantID, userID, limit, offset, query)
 if err != nil {
  return nil, 0, fmt.Errorf("list user entries: %w", err)
 }
 total, err := s.memoryRepo.CountUserEntries(ctx, tenantID, userID, query)
 if err != nil {
  return nil, 0, fmt.Errorf("count user entries: %w", err)
 }
 out := make([]*UserEntry, 0, len(entries))
 for _, e := range entries {
  out = append(out, &UserEntry{
   ID: e.ID, Role: e.Role, Content: e.Content, Type: e.Type, Scope: e.Scope,
   Importance: e.Importance, CreatedAt: e.CreatedAt, ExpiresAt: e.ExpiresAt,
  })
 }
 return out, total, nil
}

func (s *MemoryService) DeleteUserEntry(ctx context.Context, tenantID, userID, entryID string) error {
 entry, err := s.memoryRepo.Get(ctx, tenantID, entryID)
 if err != nil {
  return err
 }
 if entry.UserID != userID {
  return domain.ErrEntryNotFound
 }
 if err := s.memoryRepo.Delete(ctx, tenantID, entryID); err != nil {
  return err
 }
 if s.vectorStore != nil {
  if err := s.vectorStore.DeleteEntryVectors(ctx, tenantID, []string{entryID}); err != nil {
   s.logger.Error("memory: delete entry vectors failed", zap.String("entry_id", entryID), zap.Error(err))
  }
 }
 return nil
}

func userSnapshotFromDomain(sn *domain.ActiveSnapshot) *UserSnapshot {
 return &UserSnapshot{
  AgentID: sn.AgentID, WorkContext: sn.WorkContext, PersonalContext: sn.PersonalContext,
  TopOfMind: sn.TopOfMind, ExpiresAt: sn.ExpiresAt, UpdatedAt: sn.UpdatedAt, Status: sn.Status,
 }
}
```

> 注意：`domain.ActiveSnapshot.Validate()` 要求 `Source.Type`/`Source.Reference` 非空且 `ExpiresAt > UpdatedAt`。`UpdateUserSnapshot` 保留既有 Source 并用 `now+TTL` 作为 ExpiresAt，满足两者。

- [ ] **Step 6: 校准 `UpdateUserSnapshot` 测试断言**

Step 1 中 `require.Equal(t, "reflection", got.Status)` 是占位断言（`Status` 应为 `"active"`）。改为：

```go
 require.Equal(t, "active", got.Status)
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/memory/application/ -count=1`
Expected: PASS（含 Step 1-5 全部新测试）。若 `MockHistoryRepo`/`MockActiveSnapshotRepo` 的未使用方法签名与 port 不符，以 `port/history_repo.go`、`port/active_snapshot_repo.go` 实际声明为准修正（方法个数与参数必须匹配接口）。

- [ ] **Step 8: 提交**

```bash
git add internal/memory/application/
git commit -m "feat(memory): add summary snapshot entry management service methods"
```

---

### Task 6: handler + 路由 + 错误映射 + wiring 注入

**Files:**

- Modify: `api/http/handler/user_memory_handler.go`（userMemorySvc 接口扩展 + 12 方法 + helper）
- Modify: `api/http/handler/user_memory_handler_test.go`（fake 扩展 + 新测试）
- Modify: `api/http/router.go`（+12 路由）
- Modify: `api/middleware/error_mapping.go`（+8 条映射）
- Modify: `api/wiring/memory.go`（注入 historyRepo/activeSnapshotRepo）

**Interfaces:**

- Consumes: Task 4/5 的全部 service 方法签名与 DTO；Task 1 的 `gen` 新类型；既有 `tenantIDFromCtx/userIDFromCtx/respondMissingTenant/respondMissingUser`、`middleware.NewHTTPError`。
- Produces: 12 个 `*UserMemoryHandler` 方法（`ListFacts/GetFact/UpdateFact/DeleteFact/DeleteEntity/ListSummaries/DeleteSummary/ListSnapshots/UpdateSnapshot/DeleteSnapshot/ListEntries/DeleteEntry`）；路由挂在 `/memory` 组；error_mapping 新映射。

- [ ] **Step 1: 扩展 `userMemorySvc` 接口与 fake（先让接口编译）**

在 `api/http/handler/user_memory_handler.go` 的 `userMemorySvc` 接口内追加：

```go
 ListUserFactsFiltered(ctx context.Context, req *application.ListUserFactsFilteredRequest) ([]*application.UserFactDetail, int, error)
 GetUserFact(ctx context.Context, tenantID, userID, factID string) (*application.UserFactDetail, error)
 UpdateUserFact(ctx context.Context, tenantID, userID, factID string, patch *application.UpdateUserFactPatch) (*application.UserFactDetail, bool, error)
 DeleteUserFact(ctx context.Context, tenantID, userID, factID string) error
 DeleteUserEntity(ctx context.Context, tenantID, userID, entityID string) error
 ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*application.UserSummary, int, error)
 DeleteUserSummary(ctx context.Context, tenantID, userID, summaryID string) error
 ListUserSnapshots(ctx context.Context, tenantID, userID string) ([]*application.UserSnapshot, error)
 UpdateUserSnapshot(ctx context.Context, tenantID, userID, agentID string, patch *application.UpdateUserSnapshotPatch) (*application.UserSnapshot, error)
 DeleteUserSnapshot(ctx context.Context, tenantID, userID, agentID string) error
 ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*application.UserEntry, int, error)
 DeleteUserEntry(ctx context.Context, tenantID, userID, entryID string) error
```

在 `user_memory_handler_test.go` 的 `fakeUserMemorySvc` struct 后追加这 12 个方法的 fake 实现（返回零值，测试逐用例覆盖）：

```go
func (f *fakeUserMemorySvc) ListUserFactsFiltered(_ context.Context, req *application.ListUserFactsFilteredRequest) ([]*application.UserFactDetail, int, error) {
 f.factsReq = req
 return f.factDetails, f.factTotal, f.factsErr
}
func (f *fakeUserMemorySvc) GetUserFact(_ context.Context, _, _, _ string) (*application.UserFactDetail, error) {
 return f.factDetail, f.factErr
}
func (f *fakeUserMemorySvc) UpdateUserFact(_ context.Context, _, _, _ string, _ *application.UpdateUserFactPatch) (*application.UserFactDetail, bool, error) {
 return f.factDetail, f.vectorSyncFailed, f.factErr
}
func (f *fakeUserMemorySvc) DeleteUserFact(_ context.Context, _, _, _ string) error { return f.factErr }
func (f *fakeUserMemorySvc) DeleteUserEntity(_ context.Context, _, _, _ string) error { return f.factErr }
func (f *fakeUserMemorySvc) ListUserSummaries(_ context.Context, _, _ string, _, _ int) ([]*application.UserSummary, int, error) {
 return f.summaries, f.summaryTotal, f.factErr
}
func (f *fakeUserMemorySvc) DeleteUserSummary(_ context.Context, _, _, _ string) error { return f.factErr }
func (f *fakeUserMemorySvc) ListUserSnapshots(_ context.Context, _, _ string) ([]*application.UserSnapshot, error) {
 return f.snapshots, f.factErr
}
func (f *fakeUserMemorySvc) UpdateUserSnapshot(_ context.Context, _, _, _ string, _ *application.UpdateUserSnapshotPatch) (*application.UserSnapshot, error) {
 return f.snapshot, f.factErr
}
func (f *fakeUserMemorySvc) DeleteUserSnapshot(_ context.Context, _, _, _ string) error { return f.factErr }
func (f *fakeUserMemorySvc) ListUserEntries(_ context.Context, _, _ string, _, _ int, _ string) ([]*application.UserEntry, int, error) {
 return f.entries, f.entryTotal, f.factErr
}
func (f *fakeUserMemorySvc) DeleteUserEntry(_ context.Context, _, _, _ string) error { return f.factErr }
```

并在 `fakeUserMemorySvc` struct 内追加字段：

```go
 factsReq       *application.ListUserFactsFilteredRequest
 factDetails    []*application.UserFactDetail
 factTotal      int
 factsErr       error
 factDetail     *application.UserFactDetail
 factErr        error
 vectorSyncFailed bool
 summaries      []*application.UserSummary
 summaryTotal   int
 snapshots      []*application.UserSnapshot
 snapshot       *application.UserSnapshot
 entries        []*application.UserEntry
 entryTotal     int
```

> `go build ./api/http/...` 此时应通过（fake 补齐接口）。

- [ ] **Step 2: 写 handler 失败测试**

在 `user_memory_handler_test.go` 追加（沿用 `setupUserMemoryRouter` + `reqctx.WithTenantID` + `c.Set(middleware.ContextKeySub, userID)` 模式）：

```go
func TestUserMemoryHandler_ListFacts(t *testing.T) {
 svc := &fakeUserMemorySvc{factDetails: []*application.UserFactDetail{
  {ID: "fact-1", Content: "dark mode", Category: "preference", Confidence: 0.9},
 }, factTotal: 1}
 r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
 req := httptest.NewRequest(http.MethodGet, "/memory/facts?q=dark&category=preference&page=1&page_size=10", nil)
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusOK, w.Code)
 require.Contains(t, w.Body.String(), `"category":"preference"`)
}

func TestUserMemoryHandler_UpdateFact(t *testing.T) {
 svc := &fakeUserMemorySvc{factDetail: &application.UserFactDetail{ID: "fact-1", Content: "new"}}
 r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
 body := `{"content":"new content"}`
 req := httptest.NewRequest(http.MethodPatch, "/memory/facts/fact-1", strings.NewReader(body))
 req.Header.Set("Content-Type", "application/json")
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusOK, w.Code)
 require.Contains(t, w.Body.String(), `"content":"new content"`)
 require.Contains(t, w.Body.String(), `"vector_sync_failed":false`)
}

func TestUserMemoryHandler_UpdateFactEmptyPatch(t *testing.T) {
 svc := &fakeUserMemorySvc{}
 r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
 req := httptest.NewRequest(http.MethodPatch, "/memory/facts/fact-1", strings.NewReader(`{}`))
 req.Header.Set("Content-Type", "application/json")
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserMemoryHandler_DeleteFact(t *testing.T) {
 svc := &fakeUserMemorySvc{}
 r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
 req := httptest.NewRequest(http.MethodDelete, "/memory/facts/fact-1", nil)
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusNoContent, w.Code)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./api/http/handler/ -run "TestUserMemoryHandler_ListFacts|TestUserMemoryHandler_UpdateFact|TestUserMemoryHandler_DeleteFact" -count=1`
Expected: FAIL（路由未注册 → 404 或方法未实现）。

- [ ] **Step 4: 实现 handler 方法与 helper**

在 `user_memory_handler.go` 的 `ListMemories` 前追加分页 helper，在 `ClearSession` 后追加 12 个方法（`strings` 需加入 import）：

```go
// parsePageParams 解析 page/page_size，非法值返回 error（handler 统一 400）。
func parsePageParams(c *gin.Context) (page, pageSize int, err error) {
 page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
 pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
 if page < 1 {
  page = 1
 }
 if pageSize < 1 {
  pageSize = 20
 }
 return page, pageSize, nil
}
```

```go
// ListFacts godoc
// GET /memory/facts?page=&page_size=&q=&importance_min=&importance_max=&category=
// 事实管理列表（搜索 + 重要度/分类筛选 + 分页）。
func (h *UserMemoryHandler) ListFacts(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 page, pageSize, err := parsePageParams(c)
 if err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 var importanceMin, importanceMax *float64
 for key, dst := range map[string]**float64{
  "importance_min": &importanceMin,
  "importance_max": &importanceMax,
 } {
  raw := c.Query(key)
  if raw == "" {
   continue
  }
  value, err := strconv.ParseFloat(raw, 64)
  if err != nil {
   _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
   return
  }
  *dst = &value
 }
 facts, total, err := h.svc.ListUserFactsFiltered(c.Request.Context(), &application.ListUserFactsFilteredRequest{
  TenantID: tenantID, UserID: userID, Query: c.Query("q"), Category: c.Query("category"),
  ImportanceMin: importanceMin, ImportanceMax: importanceMax,
  Limit: pageSize, Offset: (page - 1) * pageSize,
 })
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.MemoryFactResponse, 0, len(facts))
 for _, f := range facts {
  resp = append(resp, memoryFactResponseFromDetail(f))
 }
 c.JSON(http.StatusOK, gen.ListMemoryFactsResponse{Facts: resp, Total: int64(total)})
}

// GetFact godoc
// GET /memory/facts/:id 事实详情（编辑预填）。
func (h *UserMemoryHandler) GetFact(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 fact, err := h.svc.GetUserFact(c.Request.Context(), tenantID, userID, c.Param("id"))
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, memoryFactResponseFromDetail(fact))
}

// UpdateFact godoc
// PATCH /memory/facts/:id body {content?, importance?, category?}
// 内容/重要度/分类至少一项；向量同步失败仍返回 200 + vector_sync_failed=true。
func (h *UserMemoryHandler) UpdateFact(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 var req gen.UpdateMemoryFactRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 if req.Content == nil && req.Importance == nil && req.Category == nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 fact, vectorSyncFailed, err := h.svc.UpdateUserFact(c.Request.Context(), tenantID, userID, c.Param("id"),
  &application.UpdateUserFactPatch{Content: req.Content, Importance: req.Importance, Category: req.Category})
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"fact": memoryFactResponseFromDetail(fact), "vector_sync_failed": vectorSyncFailed})
}

// DeleteFact godoc
// DELETE /memory/facts/:id 硬删 + 向量清理（best-effort）。
func (h *UserMemoryHandler) DeleteFact(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 if err := h.svc.DeleteUserFact(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.Status(http.StatusNoContent)
}

// DeleteEntity godoc
// DELETE /memory/entities/:id 单条实体删除。
func (h *UserMemoryHandler) DeleteEntity(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 if err := h.svc.DeleteUserEntity(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.Status(http.StatusNoContent)
}

// ListSummaries godoc
// GET /memory/summaries?page=&page_size= 历史摘要分页列表。
func (h *UserMemoryHandler) ListSummaries(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 page, pageSize, err := parsePageParams(c)
 if err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 summaries, total, err := h.svc.ListUserSummaries(c.Request.Context(), tenantID, userID, pageSize, (page-1)*pageSize)
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.MemorySummaryItemResponse, 0, len(summaries))
 for _, s := range summaries {
  resp = append(resp, gen.MemorySummaryItemResponse{
   ID: s.ID, Summary: s.Summary, Tier: s.Tier, Importance: s.Importance,
   ConversationID: s.ConversationID, PeriodEnd: s.PeriodEnd, CreatedAt: s.CreatedAt,
  })
 }
 c.JSON(http.StatusOK, gen.ListMemorySummariesResponse{Summaries: resp, Total: int64(total)})
}

// DeleteSummary godoc
// DELETE /memory/summaries/:id 历史摘要删除。
func (h *UserMemoryHandler) DeleteSummary(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 if err := h.svc.DeleteUserSummary(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.Status(http.StatusNoContent)
}

// ListSnapshots godoc
// GET /memory/snapshots 该用户全部 (user,agent) 快照（含过期，供管理/清空）。
func (h *UserMemoryHandler) ListSnapshots(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 snapshots, err := h.svc.ListUserSnapshots(c.Request.Context(), tenantID, userID)
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.MemorySnapshotResponse, 0, len(snapshots))
 for _, s := range snapshots {
  resp = append(resp, gen.MemorySnapshotResponse{
   AgentID: s.AgentID, WorkContext: s.WorkContext, PersonalContext: s.PersonalContext,
   TopOfMind: s.TopOfMind, ExpiresAt: s.ExpiresAt, UpdatedAt: s.UpdatedAt, Status: s.Status,
  })
 }
 c.JSON(http.StatusOK, gen.ListMemorySnapshotsResponse{Snapshots: resp})
}

// UpdateSnapshot godoc
// PATCH /memory/snapshots/:agent_id body {work_context, personal_context, top_of_mind}
func (h *UserMemoryHandler) UpdateSnapshot(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 var req gen.UpdateMemorySnapshotRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 snapshot, err := h.svc.UpdateUserSnapshot(c.Request.Context(), tenantID, userID, c.Param("agent_id"),
  &application.UpdateUserSnapshotPatch{
   WorkContext: req.WorkContext, PersonalContext: req.PersonalContext, TopOfMind: req.TopOfMind,
  })
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gen.MemorySnapshotResponse{
  AgentID: snapshot.AgentID, WorkContext: snapshot.WorkContext, PersonalContext: snapshot.PersonalContext,
  TopOfMind: snapshot.TopOfMind, ExpiresAt: snapshot.ExpiresAt, UpdatedAt: snapshot.UpdatedAt,
  Status: snapshot.Status,
 })
}

// DeleteSnapshot godoc
// DELETE /memory/snapshots/:agent_id 清空快照。
func (h *UserMemoryHandler) DeleteSnapshot(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 if err := h.svc.DeleteUserSnapshot(c.Request.Context(), tenantID, userID, c.Param("agent_id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.Status(http.StatusNoContent)
}

// ListEntries godoc
// GET /memory/entries?page=&page_size=&q= 原始条目分页列表。
func (h *UserMemoryHandler) ListEntries(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 page, pageSize, err := parsePageParams(c)
 if err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
  return
 }
 entries, total, err := h.svc.ListUserEntries(c.Request.Context(), tenantID, userID, pageSize, (page-1)*pageSize, c.Query("q"))
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.MemoryEntryItemResponse, 0, len(entries))
 for _, e := range entries {
  var expiresAt *time.Time
  if e.ExpiresAt != nil {
   t := *e.ExpiresAt
   expiresAt = &t
  }
  resp = append(resp, gen.MemoryEntryItemResponse{
   ID: e.ID, Role: e.Role, Content: e.Content, Type: e.Type, Scope: e.Scope,
   Importance: e.Importance, CreatedAt: e.CreatedAt, ExpiresAt: expiresAt,
  })
 }
 c.JSON(http.StatusOK, gen.ListMemoryEntriesResponse{Entries: resp, Total: int64(total)})
}

// DeleteEntry godoc
// DELETE /memory/entries/:id 硬删 + 向量清理（best-effort）。
func (h *UserMemoryHandler) DeleteEntry(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
  return
 }
 if err := h.svc.DeleteUserEntry(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.Status(http.StatusNoContent)
}

func memoryFactResponseFromDetail(fact *application.UserFactDetail) gen.MemoryFactResponse {
 return gen.MemoryFactResponse{
  ID: fact.ID, Scope: fact.Scope, Content: fact.Content,
  Importance: fact.Importance, CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
  Confidence: fact.Confidence, Category: fact.Category, Source: fact.Source, Status: fact.Status,
 }
}
```

> 同时把既有 `memoryFactResponse` 扩展为新字段（Task 4 已给 `UserMemory` 加 Category/Confidence/Source/Status）：

```go
func memoryFactResponse(memory *application.UserMemory) gen.MemoryFactResponse {
 return gen.MemoryFactResponse{
  ID: memory.ID, Scope: memory.Scope, Content: memory.Content,
  Importance: memory.Importance, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt,
  Confidence: memory.Confidence, Category: memory.Category, Source: memory.Source, Status: memory.Status,
 }
}
```

> `time` 需加入 `user_memory_handler.go` 的 import。

- [ ] **Step 5: 注册路由**

在 `api/http/router.go` 的 `registerMemory` 内（`g.DELETE("/session/:session_id", ...)` 后）追加：

```go
 g.GET("/facts", userHandler.ListFacts)
 g.GET("/facts/:id", userHandler.GetFact)
 g.PATCH("/facts/:id", userHandler.UpdateFact)
 g.DELETE("/facts/:id", userHandler.DeleteFact)
 g.DELETE("/entities/:id", userHandler.DeleteEntity)
 g.GET("/summaries", userHandler.ListSummaries)
 g.DELETE("/summaries/:id", userHandler.DeleteSummary)
 g.GET("/snapshots", userHandler.ListSnapshots)
 g.PATCH("/snapshots/:agent_id", userHandler.UpdateSnapshot)
 g.DELETE("/snapshots/:agent_id", userHandler.DeleteSnapshot)
 g.GET("/entries", userHandler.ListEntries)
 g.DELETE("/entries/:id", userHandler.DeleteEntry)
```

- [ ] **Step 6: 扩展错误映射**

在 `api/middleware/error_mapping.go` 的 memory 段追加：

```go
 memorydomain.ErrInvalidCategory:         http.StatusBadRequest,
 memorydomain.ErrConfidenceOutOfRange:    http.StatusBadRequest,
 memorydomain.ErrImportanceOutOfRange:    http.StatusBadRequest,
 memorydomain.ErrEmptyFactPatch:          http.StatusBadRequest,
 memorydomain.ErrFactNotEditable:         http.StatusConflict,
 memorydomain.ErrSummaryNotFound:         http.StatusNotFound,
 memorydomain.ErrSnapshotNotFound:        http.StatusNotFound,
 memoryapp.ErrMemoryEmbeddingUnavailable: http.StatusBadGateway,
```

- [ ] **Step 7: wiring 注入 historyRepo/activeSnapshotRepo**

在 `api/wiring/memory.go` 的 `buildMemoryService`（`mem.Service.SetMemoryRepo(memRepo)` 附近）追加：

```go
 mem.Service.SetHistoryRepo(persistence.NewHistoryRepo(db))
 mem.Service.SetActiveSnapshotRepo(persistence.NewActiveSnapshotRepo(db))
```

> 该函数开头已有 `factRepo := persistence.NewFactRepo(db)` 与 `db` 在作用域内；`SetVectorStore` 仍由 `buildMemoryRecall` 注入，此处无需重复。

- [ ] **Step 8: 运行测试**

Run: `go test ./api/... ./internal/memory/... -count=1`
Expected: PASS。若 `SetupMemoryStatsRouter` 之类既有测试因接口扩展而编译失败，检查是 fake 未补齐（Step 1 已补）还是 `NewUserMemoryHandler` 调用处传了不完整 fake —— 保持 fake 结构完整即可。

- [ ] **Step 9: 契约守卫验证**

Run: `make check`
Expected: PASS（`dto-residue-guard.sh` 确认 `api/http/dto/gen` 无残留；`contract_test.go` 不覆盖 memory 路由，无需更新 golden）。

- [ ] **Step 10: 提交**

```bash
git add api/http/handler/ api/http/router.go api/middleware/error_mapping.go api/wiring/memory.go
git commit -m "feat(memory): add REST handlers routes and error mapping"
```

---

### Task 7: 前端数据层（model / api / hooks）

**Files:**

- Modify: `web/src/modules/memory/model/memory.ts`
- Modify: `web/src/modules/memory/api/memory-user.api.ts`
- Create: `web/src/modules/memory/hooks/useFactsTab.ts`、`useEntitiesTab.ts`、`useSummariesTab.ts`、`useSnapshotsTab.ts`、`useEntriesTab.ts`
- Test: `web/src/modules/memory/hooks/__tests__/useFactsTab.test.ts`、`useEntriesTab.test.ts`

**Interfaces:**

- Consumes: Task 1 生成的 `web/src/services/gen/memory.ts`（类型名对齐，不强依赖）、既有 `api`（`@/services/client`）、`usePagination`（`@/shared/hooks`）、`DEFAULT_PAGE_SIZE`。
- Produces: 扩展 `MemoryFact` 类型 + `MemorySummary/MemorySnapshot/MemoryEntry` 类型 + 各分页 schema；`memoryUserApi` 新增 12 个函数；5 个 hook，每个返回 `{ loading, data, pagination, reload, ... }`（命名见各 hook）。Task 8 组件消费这些 hook。

- [ ] **Step 1: 扩展 model 类型**

在 `web/src/modules/memory/model/memory.ts` 中扩展 `memoryFactSchema` 并追加新 schema：

```ts
export const memoryFactSchema = z.object({
  id: z.string(),
  scope: z.string(),
  content: z.string(),
  importance: z.number(),
  created_at: z.string(),
  updated_at: z.string(),
  confidence: z.number(),
  category: z.string(),
  source: z.string(),
  status: z.string(),
});
export type MemoryFact = z.infer<typeof memoryFactSchema>;

export const memoryFactListPageSchema = z.object({
  facts: z.array(memoryFactSchema),
  total: z.number(),
});
export type MemoryFactListPage = z.infer<typeof memoryFactListPageSchema>;

export const updateMemoryFactResponseSchema = z.object({
  fact: memoryFactSchema,
  vector_sync_failed: z.boolean(),
});
export type UpdateMemoryFactResponse = z.infer<typeof updateMemoryFactResponseSchema>;

export const memorySummarySchema = z.object({
  id: z.string(),
  summary: z.string(),
  tier: z.string(),
  importance: z.number(),
  conversation_id: z.string(),
  period_end: z.string(),
  created_at: z.string(),
});
export type MemorySummary = z.infer<typeof memorySummarySchema>;
export const memorySummaryListPageSchema = z.object({
  summaries: z.array(memorySummarySchema),
  total: z.number(),
});

export const memorySnapshotSchema = z.object({
  agent_id: z.string(),
  work_context: z.array(z.string()),
  personal_context: z.array(z.string()),
  top_of_mind: z.array(z.string()),
  expires_at: z.string(),
  updated_at: z.string(),
  status: z.string(),
});
export type MemorySnapshot = z.infer<typeof memorySnapshotSchema>;
export const memorySnapshotListSchema = z.object({
  snapshots: z.array(memorySnapshotSchema),
});

export const memoryEntrySchema = z.object({
  id: z.string(),
  role: z.string(),
  content: z.string(),
  type: z.string(),
  scope: z.string(),
  importance: z.number(),
  created_at: z.string(),
  expires_at: z.string().nullable(),
});
export type MemoryEntry = z.infer<typeof memoryEntrySchema>;
export const memoryEntryListPageSchema = z.object({
  entries: z.array(memoryEntrySchema),
  total: z.number(),
});
```

> 若 `memoryFactSchema` 已是 `export const` 且带注释，保留原注释并只追加字段。若现有文件用 `export interface MemoryFact` 而非 `z.infer`，改为与文件既有风格一致的 schema 扩展（先读文件再改）。

- [ ] **Step 2: 扩展 api 函数**

在 `web/src/modules/memory/api/memory-user.api.ts` 追加：

```ts
export interface MemoryListParams {
  page: number;
  page_size: number;
  q?: string;
  importance_min?: number;
  importance_max?: number;
  category?: string;
}

export const memoryUserApi = {
  // ... 既有方法不变 ...

  listFacts(params: MemoryListParams) {
    return api.get('/memory/facts', { params }).then((res) => memoryFactListPageSchema.parse(res.data));
  },
  getFact(id: string) {
    return api.get(`/memory/facts/${id}`).then((res) => memoryFactSchema.parse(res.data));
  },
  updateFact(id: string, data: { content?: string; importance?: number; category?: string }) {
    return api.patch(`/memory/facts/${id}`, data).then((res) => updateMemoryFactResponseSchema.parse(res.data));
  },
  deleteFact(id: string) {
    return api.delete(`/memory/facts/${id}`);
  },
  deleteEntity(id: string) {
    return api.delete(`/memory/entities/${id}`);
  },
  listSummaries(params: { page: number; page_size: number }) {
    return api.get('/memory/summaries', { params }).then((res) => memorySummaryListPageSchema.parse(res.data));
  },
  deleteSummary(id: string) {
    return api.delete(`/memory/summaries/${id}`);
  },
  listSnapshots() {
    return api.get('/memory/snapshots').then((res) => memorySnapshotListSchema.parse(res.data));
  },
  updateSnapshot(agentId: string, data: { work_context: string[]; personal_context: string[]; top_of_mind: string[] }) {
    return api.patch(`/memory/snapshots/${agentId}`, data).then((res) => memorySnapshotSchema.parse(res.data));
  },
  deleteSnapshot(agentId: string) {
    return api.delete(`/memory/snapshots/${agentId}`);
  },
  listEntries(params: { page: number; page_size: number; q?: string }) {
    return api.get('/memory/entries', { params }).then((res) => memoryEntryListPageSchema.parse(res.data));
  },
  deleteEntry(id: string) {
    return api.delete(`/memory/entries/${id}`);
  },
};
```

> 若 `memoryUserApi` 用对象字面量且内部既有方法，追加到同一对象内；`import` 从 `../model/memory` 补齐新 schema 导入。

- [ ] **Step 3: 创建 `useFactsTab.ts`（含筛选/编辑/删除）**

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryFact } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export interface FactFilters {
  q?: string;
  importanceMin?: number;
  importanceMax?: number;
  category?: string;
}

export const useFactsTab = () => {
  const [facts, setFacts] = useState<MemoryFact[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [filters, setFilters] = useState<FactFilters>({});
  // 请求序号：筛选/翻页时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listFacts({
        page,
        page_size: pageSize,
        ...(filters.q ? { q: filters.q } : {}),
        ...(filters.importanceMin !== undefined ? { importance_min: filters.importanceMin } : {}),
        ...(filters.importanceMax !== undefined ? { importance_max: filters.importanceMax } : {}),
        ...(filters.category ? { category: filters.category } : {}),
      });
      if (seq !== requestSeqRef.current) return;
      setFacts(data.facts);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载事实失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, filters, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const applyFilters = useCallback((next: FactFilters) => {
    setFilters(next);
    // 切到第一页重新查询（分页状态由 usePagination 管理，通过 key 重建最稳）。
  }, []);

  const deleteFact = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteFact(id);
      message.success({ content: '事实已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除事实失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  const updateFact = useCallback(async (id: string, patch: { content?: string; importance?: number; category?: string }) => {
    try {
      const res = await memoryUserApi.updateFact(id, patch);
      if (res.vector_sync_failed) {
        // spec §4：内容已保存，向量同步失败待后台补偿。
        message.error({ content: '内容已保存，但向量同步失败，将在后台补偿', duration: 3 });
      } else {
        message.success({ content: '事实已更新', duration: 2 });
      }
      await load();
      return res.fact;
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '更新事实失败', duration: 3 });
      throw err;
    }
  }, [load]);

  return { facts, loading, deleteLoading, filters, applyFilters, updateFact, deleteFact, pagination: { current: page, pageSize, total, pageSizeOptions, onChange } };
};
```

- [ ] **Step 4: 创建其余 4 个 hook**

`useEntitiesTab.ts`（沿用既有 `useMyMemoriesPage` 的实体加载模式 + 删除）：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryEntity } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useEntitiesTab = () => {
  const [entities, setEntities] = useState<MemoryEntity[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listMyEntities({ page, pageSize });
      if (seq !== requestSeqRef.current) return;
      setEntities(data.entities);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载实体失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteEntity = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteEntity(id);
      message.success({ content: '实体已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除实体失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { entities, loading, deleteLoading, deleteEntity, pagination: { current: page, pageSize, total, pageSizeOptions, onChange } };
};
```

> 若 `listMyEntities` 现签名是 `(params: { page: number; pageSize: number })`，沿用原调用；以现有 api 实际签名为准。

`useSummariesTab.ts`：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemorySummary } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useSummariesTab = () => {
  const [summaries, setSummaries] = useState<MemorySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listSummaries({ page, page_size: pageSize });
      if (seq !== requestSeqRef.current) return;
      setSummaries(data.summaries);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载摘要失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteSummary = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteSummary(id);
      message.success({ content: '摘要已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除摘要失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { summaries, loading, deleteLoading, deleteSummary, pagination: { current: page, pageSize, total, pageSizeOptions, onChange } };
};
```

`useSnapshotsTab.ts`：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemorySnapshot } from '../model/memory';

interface RequestError { response?: { data?: { error?: string } } }

export const useSnapshotsTab = () => {
  const [snapshots, setSnapshots] = useState<MemorySnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [saveLoading, setSaveLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listSnapshots();
      if (seq !== requestSeqRef.current) return;
      setSnapshots(data.snapshots);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载快照失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const updateSnapshot = useCallback(async (agentId: string, data: { work_context: string[]; personal_context: string[]; top_of_mind: string[] }) => {
    setSaveLoading(true);
    try {
      await memoryUserApi.updateSnapshot(agentId, data);
      message.success({ content: '快照已更新', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '更新快照失败', duration: 3 });
    } finally {
      setSaveLoading(false);
    }
  }, [load]);

  const deleteSnapshot = useCallback(async (agentId: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteSnapshot(agentId);
      message.success({ content: '快照已清空', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清空快照失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { snapshots, loading, saveLoading, deleteLoading, updateSnapshot, deleteSnapshot, reload: load };
};
```

`useEntriesTab.ts`：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryEntry } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useEntriesTab = () => {
  const [entries, setEntries] = useState<MemoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [query, setQuery] = useState('');
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listEntries({ page, page_size: pageSize, q: query || undefined });
      if (seq !== requestSeqRef.current) return;
      setEntries(data.entries);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载条目失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, query, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteEntry = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteEntry(id);
      message.success({ content: '条目已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除条目失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { entries, loading, deleteLoading, query, setQuery, deleteEntry, pagination: { current: page, pageSize, total, pageSizeOptions, onChange } };
};
```

- [ ] **Step 5: 写 hook 测试（useFactsTab + useEntriesTab 代表）**

创建 `web/src/modules/memory/hooks/__tests__/useFactsTab.test.ts`（沿用 `useMyMemoriesPage.test.ts` 的 `vi.hoisted`/`vi.mock`/`vi.spyOn` 模式）：

```ts
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { listFactsMock, deleteFactMock } = vi.hoisted(() => ({
  listFactsMock: vi.fn(),
  deleteFactMock: vi.fn(),
}));

vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: {
    listFacts: listFactsMock,
    deleteFact: deleteFactMock,
    updateFact: vi.fn(),
  },
}));

import { message } from 'antd';
import { useFactsTab } from '../useFactsTab';

describe('useFactsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    listFactsMock.mockResolvedValue({
      facts: [{ id: 'fact-1', content: 'dark mode', category: 'preference', confidence: 0.9, status: 'active' }],
      total: 1,
    });
  });

  it('loads facts on mount', async () => {
    const { result } = renderHook(() => useFactsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listFactsMock).toHaveBeenCalled();
    expect(result.current.facts).toHaveLength(1);
  });

  it('deletes a fact and reloads', async () => {
    deleteFactMock.mockResolvedValue(undefined);
    const { result } = renderHook(() => useFactsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.deleteFact('fact-1');
    });
    expect(deleteFactMock).toHaveBeenCalledWith('fact-1');
    expect(message.success).toHaveBeenCalled();
    expect(listFactsMock).toHaveBeenCalledTimes(2);
  });
});
```

创建 `web/src/modules/memory/hooks/__tests__/useEntriesTab.test.ts`（结构同上，mock `listEntries`/`deleteEntry`，断言 `query` 变更后重新请求）：

```ts
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { listEntriesMock, deleteEntryMock } = vi.hoisted(() => ({
  listEntriesMock: vi.fn(),
  deleteEntryMock: vi.fn(),
}));

vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: {
    listEntries: listEntriesMock,
    deleteEntry: deleteEntryMock,
  },
}));

import { message } from 'antd';
import { useEntriesTab } from '../useEntriesTab';

describe('useEntriesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    listEntriesMock.mockResolvedValue({ entries: [{ id: 'e-1', content: 'hello' }], total: 1 });
  });

  it('loads entries and applies query filter', async () => {
    const { result } = renderHook(() => useEntriesTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.entries).toHaveLength(1);

    await act(async () => {
      result.current.setQuery('hello');
    });
    await waitFor(() => expect(listEntriesMock).toHaveBeenCalledWith(expect.objectContaining({ q: 'hello' })));
  });

  it('deletes an entry', async () => {
    deleteEntryMock.mockResolvedValue(undefined);
    const { result } = renderHook(() => useEntriesTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.deleteEntry('e-1');
    });
    expect(deleteEntryMock).toHaveBeenCalledWith('e-1');
  });
});
```

> mock 的返回对象字段要与 Task 7 Step 1 的 schema 对齐（缺字段会 zod parse 失败）；`MemoryEntry` mock 需含 `expires_at: null` 等全部字段。

- [ ] **Step 6: 运行前端测试与类型检查**

Run: `cd web && npx vitest run src/modules/memory/hooks/__tests__/useFactsTab.test.ts src/modules/memory/hooks/__tests__/useEntriesTab.test.ts`
Expected: PASS。

Run: `cd web && npx tsc --noEmit`
Expected: 无类型错误。

- [ ] **Step 7: 提交**

```bash
git add web/src/modules/memory/model/ web/src/modules/memory/api/ web/src/modules/memory/hooks/
git commit -m "feat(memory): add frontend data layer and per-tab hooks"
```

---

### Task 8: 前端 5-Tab 页面 + 组件 + e2e 联动

**Files:**

- Create: `web/src/modules/memory/components/FactTable.tsx`、`EntityTable.tsx`、`SummaryTable.tsx`、`SnapshotPanel.tsx`、`EntryTable.tsx`
- Modify: `web/src/modules/memory/hooks/useMyMemoriesPage.ts`（精简为 stats + clear）
- Modify: `web/src/modules/memory/pages/MyMemoriesPage.tsx`（5 Tab 布局）
- Modify: `web/src/modules/memory/hooks/__tests__/useMyMemoriesPage.test.ts`
- Modify: `test/e2e/memory_lifecycle_test.go`（编辑/删除事实 → 召回联动）

**Interfaces:**

- Consumes: Task 7 的 5 个 hook 与 `memoryUserApi`、既有 `StatCard/DangerPopconfirm/EmptyHint/Pagination`（`web/src/modules/memory/components/` 下已存在）、`useMyMemoriesPage`。
- Produces: 5 个 Tab 组件 + 重构后的页面。e2e 新增服务层链路断言。

- [ ] **Step 1: 创建 `FactTable.tsx`**

```tsx
import { SearchOutlined } from '@ant-design/icons';
import { Button, Descriptions, Drawer, Empty, Form, Input, InputNumber, Modal, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useState } from 'react';

import type { MemoryFact } from '../model/memory';
import { useFactsTab } from '../hooks/useFactsTab';

import { DangerPopconfirm, EmptyHint, Pagination } from '@/shared/components';
import { FACT_CATEGORIES } from '@/constants';

const columns = (onEdit: (f: MemoryFact) => void, onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryFact> => [
  { title: '内容', dataIndex: 'content', ellipsis: true },
  { title: '分类', dataIndex: 'category', width: 100, render: (v: string) => <Tag>{v}</Tag> },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  { title: '来源', dataIndex: 'source', width: 130, ellipsis: true },
  { title: '更新时间', dataIndex: 'updated_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 140,
    render: (_: unknown, record: MemoryFact) => (
      <Space>
        <Button type="link" size="small" onClick={() => onEdit(record)}>编辑</Button>
        <DangerPopconfirm title="删除事实" description="删除后该事实将不再进入记忆上下文，且无法恢复" onConfirm={() => onDelete(record.id)}>
          <Button type="link" size="small" danger loading={deleteLoading}>删除</Button>
        </DangerPopconfirm>
      </Space>
    ),
  },
];

export const FactTable = ({ onChanged }: { onChanged?: () => void }) => {
  const { facts, loading, deleteLoading, filters, applyFilters, updateFact, deleteFact, pagination } = useFactsTab();
  const [detail, setDetail] = useState<MemoryFact | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<MemoryFact | null>(null);
  const [form] = Form.useForm();
  const [saveLoading, setSaveLoading] = useState(false);

  const handleDelete = async (id: string) => {
    await deleteFact(id);
    onChanged?.();
  };

  const openEdit = (f: MemoryFact) => {
    setEditing(f);
    setDetail(null);
    form.setFieldsValue({ content: f.content, importance: f.importance, category: f.category });
    setEditOpen(true);
  };

  const handleSave = async () => {
    const values = await form.validateFields();
    if (!editing) return;
    setSaveLoading(true);
    try {
      await updateFact(editing.id, values);
      setEditOpen(false);
      onChanged?.();
    } finally {
      setSaveLoading(false);
    }
  };

  return (
    <div>
      <Space style={{ marginBottom: 16 }} wrap>
        <Input
          placeholder="搜索事实内容"
          prefix={<SearchOutlined />}
          allowClear
          defaultValue={filters.q}
          onPressEnter={(e) => applyFilters({ ...filters, q: (e.target as HTMLInputElement).value })}
          onBlur={(e) => applyFilters({ ...filters, q: e.target.value })}
          style={{ width: 220 }}
        />
        <Select
          placeholder="分类"
          allowClear
          style={{ width: 130 }}
          options={FACT_CATEGORIES.map((c) => ({ label: c, value: c }))}
          onChange={(v?: string) => applyFilters({ ...filters, category: v })}
        />
        <InputNumber
          placeholder="重要度 ≥"
          min={0}
          max={1}
          step={0.1}
          style={{ width: 110 }}
          onChange={(v?: number) => applyFilters({ ...filters, importanceMin: v ?? undefined })}
        />
      </Space>
      <Table<MemoryFact>
        rowKey="id"
        columns={columns((f) => openEdit(f), (id) => void handleDelete(id), deleteLoading)}
        dataSource={facts}
        loading={loading}
        onRow={(record) => ({ onClick: () => setDetail(record) })}
        pagination={false}
        locale={{ emptyText: <EmptyHint text={facts.length === 0 ? '事实记忆还是空的' : '没有找到匹配的事实'} /> }}
      />
      <Pagination {...pagination} />

      <Drawer open={detail !== null} onClose={() => setDetail(null)} title="事实详情" width={480}>
        {detail && (
          <Descriptions column={1} bordered size="small">
            <Descriptions.Item label="内容">{detail.content}</Descriptions.Item>
            <Descriptions.Item label="分类">{detail.category}</Descriptions.Item>
            <Descriptions.Item label="重要度">{detail.importance}</Descriptions.Item>
            <Descriptions.Item label="置信度">{detail.confidence}</Descriptions.Item>
            <Descriptions.Item label="来源">{detail.source}</Descriptions.Item>
            <Descriptions.Item label="创建时间">{new Date(detail.created_at).toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label="更新时间">{new Date(detail.updated_at).toLocaleString()}</Descriptions.Item>
          </Descriptions>
        )}
      </Drawer>

      <Modal title="编辑事实" open={editOpen} onCancel={() => setEditOpen(false)} onOk={handleSave} confirmLoading={saveLoading} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="content" label="内容" rules={[{ required: true, message: '请输入内容' }]}>
            <Input.TextArea rows={4} maxLength={1000} showCount />
          </Form.Item>
          <Form.Item name="importance" label="重要度（0-1）" rules={[{ required: true, message: '请输入重要度' }]}>
            <InputNumber min={0} max={1} step={0.1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="category" label="分类" rules={[{ required: true, message: '请选择分类' }]}>
            <Select options={FACT_CATEGORIES.map((c) => ({ label: c, value: c }))} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};
```

> 需要确认 `FACT_CATEGORIES` 是否已存在于 `web/src/constants/index.ts`；若不存在，在本 Step 追加（值取后端白名单 `['preference', 'skill', 'event', 'state', 'relationship', 'other']`）。`DangerPopconfirm/EmptyHint/Pagination` 的既有签名以 `web/src/shared/components` 实际导出为准（`DangerPopconfirm` 在本项目已用于 `MyMemoriesPage`，沿用其 props）。

- [ ] **Step 2: 创建 `EntityTable.tsx`**

```tsx
import { Button, Space, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEntitiesTab } from '../hooks/useEntitiesTab';
import type { MemoryEntity } from '../model/memory';

import { DangerPopconfirm, EmptyHint, Pagination } from '@/shared/components';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryEntity> => [
  { title: '名称', dataIndex: 'name' },
  { title: '类型', dataIndex: 'entity_type', width: 120 },
  { title: '关联事实数', dataIndex: 'fact_count', width: 110 },
  { title: '最近出现', dataIndex: 'last_seen_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemoryEntity) => (
      <DangerPopconfirm title="删除实体" description="删除后该实体不再出现在记忆话题中，且无法恢复" onConfirm={() => onDelete(record.id)}>
        <Button type="link" size="small" danger loading={deleteLoading}>删除</Button>
      </DangerPopconfirm>
    ),
  },
];

export const EntityTable = ({ onChanged }: { onChanged?: () => void }) => {
  const { entities, loading, deleteLoading, deleteEntity, pagination } = useEntitiesTab();
  return (
    <div>
      <Table<MemoryEntity>
        rowKey="id"
        columns={columns((id) => void deleteEntity(id), deleteLoading)}
        dataSource={entities}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint text="实体记忆还是空的" /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};
```

- [ ] **Step 3: 创建 `SummaryTable.tsx`**

```tsx
import { Button, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useSummariesTab } from '../hooks/useSummariesTab';
import type { MemorySummary } from '../model/memory';

import { DangerPopconfirm, EmptyHint, Pagination } from '@/shared/components';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemorySummary> => [
  { title: '摘要', dataIndex: 'summary', ellipsis: true },
  { title: '层级', dataIndex: 'tier', width: 130, render: (v: string) => <Tag>{v}</Tag> },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  { title: '覆盖至', dataIndex: 'period_end', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  { title: '创建时间', dataIndex: 'created_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemorySummary) => (
      <DangerPopconfirm title="删除摘要" description="删除后该历史摘要不再进入记忆上下文，且无法恢复" onConfirm={() => onDelete(record.id)}>
        <Button type="link" size="small" danger loading={deleteLoading}>删除</Button>
      </DangerPopconfirm>
    ),
  },
];

export const SummaryTable = ({ onChanged }: { onChanged?: () => void }) => {
  const { summaries, loading, deleteLoading, deleteSummary, pagination } = useSummariesTab();
  return (
    <div>
      <Table<MemorySummary>
        rowKey="id"
        columns={columns((id) => void deleteSummary(id), deleteLoading)}
        dataSource={summaries}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint text="历史摘要还是空的" /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};
```

- [ ] **Step 4: 创建 `SnapshotPanel.tsx`**

```tsx
import { Button, Card, Col, Empty, List, Modal, Row, Space, Spin } from 'antd';
import { useState } from 'react';
import { useSnapshotsTab } from '../hooks/useSnapshotsTab';
import type { MemorySnapshot } from '../model/memory';

import { DangerPopconfirm } from '@/shared/components';

export const SnapshotPanel = ({ onChanged }: { onChanged?: () => void }) => {
  const { snapshots, loading, saveLoading, deleteLoading, updateSnapshot, deleteSnapshot } = useSnapshotsTab();
  const [editing, setEditing] = useState<MemorySnapshot | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [workContext, setWorkContext] = useState<string[]>([]);
  const [personalContext, setPersonalContext] = useState<string[]>([]);
  const [topOfMind, setTopOfMind] = useState<string[]>([]);

  const openEdit = (s: MemorySnapshot) => {
    setEditing(s);
    setWorkContext(s.work_context);
    setPersonalContext(s.personal_context);
    setTopOfMind(s.top_of_mind);
    setEditOpen(true);
  };

  const handleSave = async () => {
    if (!editing) return;
    await updateSnapshot(editing.agent_id, {
      work_context: workContext,
      personal_context: personalContext,
      top_of_mind: topOfMind,
    });
    setEditOpen(false);
    onChanged?.();
  };

  const handleDelete = async (agentId: string) => {
    await deleteSnapshot(agentId);
    onChanged?.();
  };

  return (
    <Spin spinning={loading}>
      {snapshots.length === 0 ? (
        <Empty description="活跃快照还是空的" />
      ) : (
        <Row gutter={[16, 16]}>
          {snapshots.map((s) => (
            <Col key={s.agent_id} xs={24} md={12} xl={8}>
              <Card
                title={`Agent ${s.agent_id}`}
                extra={
                  <Space>
                    <Button size="small" onClick={() => openEdit(s)}>编辑</Button>
                    <DangerPopconfirm title="清空快照" description="清空后该 Agent 的活跃上下文将重置，且无法恢复" onConfirm={() => handleDelete(s.agent_id)}>
                      <Button size="small" danger loading={deleteLoading}>清空</Button>
                    </DangerPopconfirm>
                  </Space>
                }
              >
                <List
                  size="small"
                  header="工作上下文"
                  dataSource={s.work_context}
                  renderItem={(item) => <List.Item>{item}</List.Item>}
                  locale={{ emptyText: <Empty /> }}
                />
                <List
                  size="small"
                  header="个人上下文"
                  dataSource={s.personal_context}
                  renderItem={(item) => <List.Item>{item}</List.Item>}
                  locale={{ emptyText: <Empty /> }}
                />
                <List
                  size="small"
                  header="当前关注"
                  dataSource={s.top_of_mind}
                  renderItem={(item) => <List.Item>{item}</List.Item>}
                  locale={{ emptyText: <Empty /> }}
                />
              </Card>
            </Col>
          ))}
        </Row>
      )}

      <Modal
        title="编辑快照"
        open={editOpen}
        onCancel={() => setEditOpen(false)}
        onOk={() => void handleSave()}
        confirmLoading={saveLoading}
        destroyOnClose
        width={640}
      >
        {editing && (
          <Space direction="vertical" style={{ width: '100%' }}>
            <List
              header="工作上下文"
              dataSource={workContext}
              renderItem={(item, i) => (
                <List.Item
                  actions={[
                    <Button key="rm" type="link" size="small" danger onClick={() => setWorkContext((v) => v.filter((_, j) => j !== i))}>
                      删除
                    </Button>,
                  ]}
                >
                  <List.Item.Meta description={item} />
                </List.Item>
              )}
              footer={
                <Button
                  size="small"
                  onClick={() => {
                    const value = window.prompt('请输入工作上下文条目');
                    if (value) setWorkContext((v) => [...v, value]);
                  }}
                >
                  添加
                </Button>
              }
            />
            <List
              header="个人上下文"
              dataSource={personalContext}
              renderItem={(item, i) => (
                <List.Item
                  actions={[
                    <Button key="rm" type="link" size="small" danger onClick={() => setPersonalContext((v) => v.filter((_, j) => j !== i))}>
                      删除
                    </Button>,
                  ]}
                >
                  <List.Item.Meta description={item} />
                </List.Item>
              )}
              footer={
                <Button
                  size="small"
                  onClick={() => {
                    const value = window.prompt('请输入个人上下文条目');
                    if (value) setPersonalContext((v) => [...v, value]);
                  }}
                >
                  添加
                </Button>
              }
            />
            <List
              header="当前关注"
              dataSource={topOfMind}
              renderItem={(item, i) => (
                <List.Item
                  actions={[
                    <Button key="rm" type="link" size="small" danger onClick={() => setTopOfMind((v) => v.filter((_, j) => j !== i))}>
                      删除
                    </Button>,
                  ]}
                >
                  <List.Item.Meta description={item} />
                </List.Item>
              )}
              footer={
                <Button
                  size="small"
                  onClick={() => {
                    const value = window.prompt('请输入当前关注条目');
                    if (value) setTopOfMind((v) => [...v, value]);
                  }}
                >
                  添加
                </Button>
              }
            />
          </Space>
        )}
      </Modal>
    </Spin>
  );
};
```

> 前端规范禁止 `alert()/confirm()`，但允许 `window.prompt()` 输入（非危险确认）；若项目内已有更合适的行内输入组件（如 `<Input onPressEnter>`），可替换 prompt 以实现无弹窗交互——以 `docs/agent/frontend.md` 为准。保持条目数 ≤8（`ActiveSnapshotSectionMaxItems`）、单条 ≤240 字符（`ActiveSnapshotItemMaxRunes`），在添加时校验。

- [ ] **Step 5: 创建 `EntryTable.tsx`**

```tsx
import { SearchOutlined } from '@ant-design/icons';
import { Button, Input, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEntriesTab } from '../hooks/useEntriesTab';
import type { MemoryEntry } from '../model/memory';

import { DangerPopconfirm, EmptyHint, Pagination } from '@/shared/components';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryEntry> => [
  { title: '内容', dataIndex: 'content', ellipsis: true },
  { title: '角色', dataIndex: 'role', width: 80 },
  { title: '类型', dataIndex: 'type', width: 100 },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  { title: '过期时间', dataIndex: 'expires_at', width: 170, render: (v: string | null) => (v ? new Date(v).toLocaleString() : '不过期') },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemoryEntry) => (
      <DangerPopconfirm title="删除条目" description="删除后该条原始消息不再进入召回上下文，且无法恢复" onConfirm={() => onDelete(record.id)}>
        <Button type="link" size="small" danger loading={deleteLoading}>删除</Button>
      </DangerPopconfirm>
    ),
  },
];

export const EntryTable = ({ onChanged }: { onChanged?: () => void }) => {
  const { entries, loading, deleteLoading, query, setQuery, deleteEntry, pagination } = useEntriesTab();
  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Input
          placeholder="搜索条目内容"
          prefix={<SearchOutlined />}
          allowClear
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ width: 220 }}
        />
        <Button onClick={() => setQuery('')}>重置</Button>
      </Space>
      <Table<MemoryEntry>
        rowKey="id"
        columns={columns((id) => void deleteEntry(id), deleteLoading)}
        dataSource={entries}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint text={entries.length === 0 ? '原始条目还是空的' : '没有找到匹配的条目'} /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};
```

- [ ] **Step 6: 精简 `useMyMemoriesPage` 为 stats + clear**

将 `web/src/modules/memory/hooks/useMyMemoriesPage.ts` 整体替换为（删除 memories/entities 的列表逻辑；clear 后通过 `reloadKey` 通知页面刷新所有 tab）：

```ts
import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryStats } from '../model/memory';

interface RequestError { response?: { data?: { error?: string } } }

export const useMyMemoriesPage = () => {
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(true);
  const [clearLoading, setClearLoading] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  const loadStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const next = await memoryUserApi.getStats();
      setStats(next);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载记忆统计失败', duration: 3 });
    } finally {
      setStatsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadStats();
  }, [loadStats]);

  const handleClearAll = useCallback(async () => {
    setClearLoading(true);
    try {
      await memoryUserApi.clearMyMemories();
      message.success({ content: '记忆已清空', duration: 2 });
      setReloadKey((k) => k + 1); // 各 Tab 监听 reloadKey 重新加载
      await loadStats();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清空记忆失败', duration: 3 });
    } finally {
      setClearLoading(false);
    }
  }, [loadStats]);

  return { stats, statsLoading, clearLoading, handleClearAll, reloadKey, reloadStats: loadStats };
};
```

- [ ] **Step 7: 重构 `MyMemoriesPage.tsx` 为 5 Tab**

将页面替换为（`StatCard`/`DangerPopconfirm` 沿用既有组件）：

```tsx
import { Tabs } from 'antd';

import { FactTable } from '../components/FactTable';
import { EntityTable } from '../components/EntityTable';
import { SummaryTable } from '../components/SummaryTable';
import { SnapshotPanel } from '../components/SnapshotPanel';
import { EntryTable } from '../components/EntryTable';
import { useMyMemoriesPage } from '../hooks/useMyMemoriesPage';

import { PageContainer } from '@/shared/components';
import { DangerPopconfirm, StatCard } from '@/shared/components';

export const MyMemoriesPage = () => {
  const { stats, statsLoading, clearLoading, handleClearAll, reloadKey, reloadStats } = useMyMemoriesPage();

  return (
    <PageContainer>
      <div style={{ display: 'flex', gap: 16, marginBottom: 24 }}>
        <StatCard title="事实记忆" loading={statsLoading} value={stats?.memory_count ?? 0} />
        <StatCard title="话题实体" loading={statsLoading} value={stats?.entity_count ?? 0} />
        {stats?.embed_model_configured === false && <StatCard title="嵌入模型" loading={statsLoading} value="未配置" warning />}
        <div style={{ marginLeft: 'auto' }}>
          <DangerPopconfirm
            title="清空全部记忆"
            description="将删除该用户的全部事实、实体、摘要、快照与原始条目，并同步清理向量，无法恢复"
            onConfirm={() => void handleClearAll()}
          >
            <Button danger loading={clearLoading}>清空全部</Button>
          </DangerPopconfirm>
        </div>
      </div>

      <Tabs
        defaultActiveKey="facts"
        items={[
          { key: 'facts', label: '事实', children: <FactTable onChanged={reloadStats} /> },
          { key: 'entities', label: '实体', children: <EntityTable onChanged={reloadStats} /> },
          { key: 'summaries', label: '摘要', children: <SummaryTable onChanged={reloadStats} /> },
          { key: 'snapshots', label: '快照', children: <SnapshotPanel onChanged={reloadStats} /> },
          { key: 'entries', label: '条目', children: <EntryTable onChanged={reloadStats} /> },
        ]}
      />
    </PageContainer>
  );
};
```

> 各 Tab 组件若需监听清空后的 `reloadKey`（Task 8 Step 6 引入），在组件内 `useEffect(() => { reload() }, [reloadKey])`——组件 props 增加可选 `reloadKey` 并透传给 hook。为最小改动，本页 Step 7 仅接线 `onChanged`；`reloadKey` 联动在 Step 8 完成。

- [ ] **Step 8: 让 5 个 Tab 组件监听 `reloadKey`**

给每个组件 props 加 `reloadKey?: number`，并在内部 `useEffect(() => { void reload(); }, [reloadKey])`（hook 需暴露 `reload`；`useFactsTab/useEntitiesTab/useSummariesTab/useEntriesTab` 返回对象里已有 `load`，改名为 `reload` 或新增别名，`useSnapshotsTab` 已返回 `reload`）。页面把 `reloadKey` 传给每个 Tab：

```tsx
children: <FactTable onChanged={reloadStats} reloadKey={reloadKey} />,
// 其余 Tab 同理
```

并更新 `web/src/modules/memory/hooks/__tests__/useMyMemoriesPage.test.ts`：删除对 `memories/entities` 的断言，仅保留 `stats/clearAll/reloadKey` 断言（沿用现有 `vi.mock` 结构）。

- [ ] **Step 9: 运行前端全量检查**

Run: `cd web && npx tsc --noEmit && npx vitest run src/modules/memory`
Expected: PASS（类型 + 全部 memory 测试）。

Run: `make fe-lint && make fe-build`
Expected: PASS（含新组件 lint）。

- [ ] **Step 10: 扩展 e2e —— 编辑/删除事实 → 召回联动**

在 `test/e2e/memory_lifecycle_test.go` 的 Step 3（召回含 dark mode）后插入编辑链路，并把原 Step 4 的 `env.FactRepo.Delete` 改为服务层 `DeleteUserFact`：

```go
 // Step 3.5: 编辑事实 → 向量同步 → 召回命中新内容（spec §5 e2e 链路）
 factID := recallResp.Facts[0].ID
 lightMode := "I prefer light mode for coding"
 updated, vectorSyncFailed, err := env.MemoryService.UpdateUserFact(ctx, env.TenantID, env.UserID, factID,
  &application.UpdateUserFactPatch{Content: &lightMode})
 require.NoError(t, err, "edit fact")
 require.False(t, vectorSyncFailed, "vector sync should succeed in e2e env")
 require.Equal(t, lightMode, updated.Content)

 recallResp1b, err := env.MemoryService.RecallMemory(ctx, recallReq)
 require.NoError(t, err, "recall after edit")
 foundLightMode := false
 for _, fact := range recallResp1b.Facts {
  if contains(fact.Content, "light mode") {
   foundLightMode = true
   break
  }
 }
 require.True(t, foundLightMode, "should recall updated preference after edit")

 // Step 4: Delete specific fact via service（归属校验 + 向量清理 best-effort）
 err = env.MemoryService.DeleteUserFact(ctx, env.TenantID, env.UserID, factID)
 require.NoError(t, err, "forget memory")
```

> 删除原 Step 4 的 `factID := recallResp.Facts[0].ID`（重复声明）与 `env.FactRepo.Delete` 调用。`env.MemoryService` 已由 `newMemoryService` 注入 `mockEmbedClient`（`Model()="text-embedding-v3"`）与 `mockVectorStore`，`UpdateUserFact` 的嵌入/向量路径可用。

- [ ] **Step 11: 运行后端全量测试**

Run: `go test ./... -count=1`
Expected: PASS（含 e2e；e2e 需 infra，若环境无 PostgreSQL/Redis 则跳过或按 `-short` 约定处理——以仓库既有 e2e 运行方式为准）。

- [ ] **Step 12: 提交**

```bash
git add web/src/modules/memory/ test/e2e/memory_lifecycle_test.go
git commit -m "feat(memory): render 5-tab management page and e2e recall linkage"
```

---

## Self-Review

**1. Spec 覆盖检查：**

| spec 要求 | 实现任务 |
|---|---|
| §1 事实 GET /facts 带筛选分页 | Task 1 proto、2 repo、4 service、6 handler、7-8 前端 |
| §1 事实 GET/PATCH/DELETE /facts/:id | Task 4（GetUserFact/UpdateUserFact/DeleteUserFact）+ Task 6 handler |
| §1 实体 DELETE /entities/:id | Task 3（EntityRepo.Delete）+ Task 4（DeleteUserEntity）+ Task 6 |
| §1 摘要 GET/DELETE /summaries | Task 3（HistoryRepo）+ Task 5（ListUserSummaries/DeleteUserSummary）+ Task 6 |
| §1 快照 GET/PATCH/DELETE /snapshots/:agent_id | Task 3（ListUser）+ Task 5（List/Update/DeleteUserSnapshot）+ Task 6 |
| §1 条目 GET/DELETE /entries | Task 3（MemoryRepo）+ Task 5（List/DeleteUserEntry）+ Task 6 |
| §1 DTO 变更（MemoryFactResponse + 新类型） | Task 1（proto）+ Task 6 handler 渲染 |
| §2 服务层新增方法表（12 个） | Task 4（facts 5 个）+ Task 5（7 个） |
| §2 仓库层新增方法表（5 个 repo） | Task 2（FactRepo）+ Task 3（其余 4 个） |
| §2 归属校验统一 404 | Task 4/5 每个 `:id` 方法 + handler 透传 |
| §3 事实编辑向量同步顺序（embed→删旧→update→upsert） | Task 4 `UpdateUserFact` 精确顺序 |
| §3 删除「PG 成功=操作成功」best-effort | Task 4 `DeleteUserFact`、Task 5 `DeleteUserEntry` |
| §3 无向量类型（实体/摘要/快照） | 对应方法不做向量操作（已核对） |
| §4 前端 5 Tab 组件 | Task 8 Step 1-5 |
| §4 详情 Drawer / 编辑 Modal / DangerPopconfirm | Task 8 FactTable |
| §4 向量同步失败提示文案 | Task 7 `updateFact` + Task 8 接线 |
| §5 错误映射表 | Task 6 Step 6（8 条新映射） |
| §5 仅 active 可编辑 409 | Task 4 `ErrFactNotEditable` + Task 6 映射 |
| §5 快照 UpdatedAt=now 绕过守卫 | Task 5 `UpdateUserSnapshot` |
| §5 测试（service/handler/parity/e2e/前端） | Task 2-8 各任务含测试；parity 由 Task 1 生成物 + Task 6 `make check` 守护 |

**2. 占位符扫描：** 计划中无 TBD/TODO/「类似上文」表述；每步含完整代码。Task 3/7 中两处「以实际编译/既有文件为准」是防生成器/风格差异的明确校准指令，非占位符。

**3. 类型一致性核对：**

- `UpdateUserFact` 返回值 `(*UserFactDetail, bool, error)` 在 Task 4 定义、Task 6 handler 使用、Task 7 前端 `updateMemoryFactResponseSchema{fact, vector_sync_failed}` 对齐。
- `port.FactRepo.ListUserFactsFiltered(ctx, tenantID, userID, filter domain.FactListFilter, limit, offset)` 在 Task 2 定义，Task 4 service 调用、Task 2 的 pgxmock 测试断言同签名。
- `domain.FactListFilter` 字段名在 Task 2（`Query/ImportanceMin/ImportanceMax/Category`）与 Task 4 `ListUserFactsFilteredRequest` 映射一致。
- `HistoryRepo.Delete(ctx, tenantID, userID, id)` 在 Task 3 定义（SQL 带 `user_id=$2` 归属），Task 5 `DeleteUserSummary` 原样透传。
- `MemoryRepo.ListUserEntries(ctx, tenantID, userID, limit, offset, query)` 与 `CountUserEntries(ctx, tenantID, userID, query)` 在 Task 3 定义，Task 5 service 与 Task 3 pgxmock 测试一致。
- handler 12 个方法名在 Task 6 定义与 Task 6 router 注册一致；前端 api 函数名在 Task 7 定义、hook 与 Task 8 组件消费一致。
- `factsCollectionName(tenantID, model)`、`constants.ActiveSnapshotTTL`、`domain.SnapshotStatusActive`、`domain.FactStatusActive` 均引用既有导出，无拼写漂移。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-27-user-memory-management.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
