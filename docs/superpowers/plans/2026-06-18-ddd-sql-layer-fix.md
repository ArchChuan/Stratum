# DDD SQL/Query Layer Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除所有 SQL/Cypher/DB 操作在 `domain/`、`application/`、`handler/` 层的违规，将其归位到 `infrastructure/` adapter。

**Architecture:** 按 DDD 分层，DB I/O 只能出现在 `infrastructure/` 实现 `domain/port/` 接口。`application/` 只持有接口引用，`handler/` 完全不知道 DB 存在。每个违规独立为一个 Task，顺序修改，每步可独立验证。

**Tech Stack:** Go 1.22, pgx v5, neo4j-go-driver v5, Gin v1.9, go test

## Global Constraints

- `domain/` 零第三方依赖（仅 stdlib + `pkg/constants`）
- `application/` 不 import `pgx`/`redis`/`neo4j`/`gin`
- `handler/` 不 import `pgx`/`redis`/`milvus`/`internal/*/infrastructure`
- SQL/Cypher 字符串只能出现在 `infrastructure/` 文件中
- 不改业务行为，只移动代码到正确层
- 每个 Task 结束后 `go vet ./...` 必须通过（忽略 agent_exec_handler.go 的已知未完成错误）
- 测试命令：`go test -short ./internal/knowledge/... ./api/http/handler/...`

---

## 文件变更总览

### Task 1 — graphrag_service.go 移到 infrastructure

| 操作 | 文件 |
|------|------|
| 新建 | `internal/knowledge/domain/port/graph.go` — GraphStore 接口 |
| 新建 | `internal/knowledge/infrastructure/neo4j/graph_adapter.go` — 原 GraphRAG 实现 |
| 删除 | `internal/knowledge/application/graphrag_service.go` |
| 修改 | `internal/knowledge/application/mocks.go` — MockGraphRAG 改为 MockGraphStore，实现新接口 |
| 修改 | `internal/knowledge/application/ingest_service.go` — 字段类型改接口 |
| 修改 | `internal/knowledge/application/rag_service.go` — 字段类型改接口 |
| 修改 | `api/wiring/knowledge.go` — 改用 infrastructure 构造函数 |

### Task 2 — ingest_service / rag_service 内残余 Cypher 移到 infrastructure

| 操作 | 文件 |
|------|------|
| 修改 | `internal/knowledge/domain/port/graph.go` — 追加 GetWorkspaceDocCount、GetWorkspaceNames 方法 |
| 修改 | `internal/knowledge/infrastructure/neo4j/graph_adapter.go` — 实现新方法 |
| 修改 | `internal/knowledge/application/ingest_service.go` — GetWorkspaceStats 去掉内联 Cypher |
| 修改 | `internal/knowledge/application/rag_service.go` — GetWorkspaceCollections 去掉内联 Cypher |

### Task 3 — agent_handler 内联 SQL 移到 skill port

| 操作 | 文件 |
|------|------|
| 修改 | `internal/agent/domain/port/skill_lookup.go`（已存在或新建）— SkillLookup 接口 |
| 新建 | `internal/agent/infrastructure/persistence/skill_lookup.go` — SQL 实现 |
| 修改 | `api/http/handler/agent_handler.go` — 去掉 db 字段和内联 SQL |
| 修改 | `api/wiring/wiring.go` 或 `api/http/router.go` — 注入 SkillLookup adapter |

### Task 4 — admin_handler PgxPool 接口清理

| 操作 | 文件 |
|------|------|
| 修改 | `api/http/handler/admin_handler.go` — 删除 PgxPool 接口定义和 pgxpool import |
| 修改 | `api/http/handler/agent_handler.go` — db 字段改为 AgentHandler 已有的 SkillLookup（Task 3 后） |

---

## Task 1: GraphRAG 从 application/ 移到 infrastructure/

**Files:**

- Create: `internal/knowledge/domain/port/graph.go`
- Create: `internal/knowledge/infrastructure/neo4j/graph_adapter.go`
- Delete: `internal/knowledge/application/graphrag_service.go`（内容迁移后清空后删）
- Modify: `internal/knowledge/application/mocks.go`
- Modify: `internal/knowledge/application/ingest_service.go`
- Modify: `internal/knowledge/application/rag_service.go`
- Modify: `api/wiring/knowledge.go`

**Interfaces:**

- Produces: `port.GraphStore` 接口（被 Task 2 追加方法）

- [ ] **Step 1: 新建 domain/port/graph.go — 定义 GraphStore 接口**

```go
// internal/knowledge/domain/port/graph.go
package port

import "context"

// GraphStore abstracts graph database operations for the knowledge context.
// All Cypher/query strings are encapsulated in infrastructure implementations.
type GraphStore interface {
 Connect(ctx context.Context) error
 CreateNode(ctx context.Context, label string, properties map[string]interface{}) error
 CreateRelationship(ctx context.Context, fromID, toID, relType string) error
 Query(ctx context.Context, query string, params map[string]interface{}) (interface{}, error)
 GetNeighborNodes(ctx context.Context, nodeID string, maxDepth int) ([]map[string]interface{}, error)
 FullTextSearch(ctx context.Context, searchTerm string, limit int) ([]map[string]interface{}, error)
 QueryWorkspaceDocumentIDs(ctx context.Context, workspace string) ([]string, error)
 DeleteWorkspaceNodes(ctx context.Context, workspace string) error
 Close() error
}
```

- [ ] **Step 2: 新建 infrastructure/neo4j/ 目录并创建 graph_adapter.go**

将 `application/graphrag_service.go` 的全部内容复制到此文件，修改 package 声明和 import：

```go
// internal/knowledge/infrastructure/neo4j/graph_adapter.go
package neo4j

import (
 "context"
 "fmt"
 "regexp"
 "strings"
 "sync"

 "github.com/neo4j/neo4j-go-driver/v5/neo4j"
 knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
 "go.uber.org/zap"
)

// validCypherIdentifier, escapeLucene, luceneSpecial — 原样复制
// GraphAdapter 即原 GraphRAG struct，改名
// NewGraphAdapter 即原 NewGraphRAG，返回 knowledgeport.GraphStore

type GraphAdapter struct {
 mu      sync.RWMutex
 driver  neo4j.DriverWithContext
 uri     string
 user    string
 passwd  string
 logger  *zap.Logger
 session neo4j.SessionWithContext
}

// 确保编译期满足接口
var _ knowledgeport.GraphStore = (*GraphAdapter)(nil)

func NewGraphAdapter(uri, user, password string, logger *zap.Logger) knowledgeport.GraphStore {
 return &GraphAdapter{uri: uri, user: user, passwd: password, logger: logger}
}
```

其余所有方法签名和实现与原 graphrag_service.go 完全一致，只是 receiver 由 `*GraphRAG` 改为 `*GraphAdapter`。

- [ ] **Step 3: 修改 application/mocks.go — MockGraphRAG 改为 MockGraphStore**

```go
// internal/knowledge/application/mocks.go
// 删除 MockGraphRAG struct 及其所有方法
// 新增：

type MockGraphStore struct {
 queryResult interface{}
 queryErr    error
}

func NewMockGraphStore() *MockGraphStore {
 return &MockGraphStore{}
}

func (m *MockGraphStore) Connect(_ context.Context) error                                    { return nil }
func (m *MockGraphStore) CreateNode(_ context.Context, _ string, _ map[string]interface{}) error { return nil }
func (m *MockGraphStore) CreateRelationship(_ context.Context, _, _, _ string) error        { return nil }
func (m *MockGraphStore) Query(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
 return m.queryResult, m.queryErr
}
func (m *MockGraphStore) GetNeighborNodes(_ context.Context, _ string, _ int) ([]map[string]interface{}, error) {
 return []map[string]interface{}{}, nil
}
func (m *MockGraphStore) FullTextSearch(_ context.Context, _ string, _ int) ([]map[string]interface{}, error) {
 return []map[string]interface{}{}, nil
}
func (m *MockGraphStore) QueryWorkspaceDocumentIDs(_ context.Context, _ string) ([]string, error) {
 return []string{}, nil
}
func (m *MockGraphStore) DeleteWorkspaceNodes(_ context.Context, _ string) error { return nil }
func (m *MockGraphStore) Close() error                                            { return nil }
func (m *MockGraphStore) SetQueryResult(r interface{})                            { m.queryResult = r }
func (m *MockGraphStore) SetQueryError(e error)                                   { m.queryErr = e }
```

- [ ] **Step 4: 修改 ingest_service.go — 字段类型改为接口**

```go
// 在 import 块加：
knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"

// KnowledgeIngest struct 中：
// 旧: graphRAG *GraphRAG
// 新:
graphRAG knowledgeport.GraphStore

// NewKnowledgeIngest 参数：
// 旧: graphRAG *GraphRAG
// 新:
graphRAG knowledgeport.GraphStore,
```

函数体不变。

- [ ] **Step 5: 修改 rag_service.go — 字段类型改为接口**

```go
// RAGService struct 中：
// 旧: graphRAG *GraphRAG
// 新:
graphRAG knowledgeport.GraphStore

// NewRAGService 参数：
// 旧: graphRAG *GraphRAG
// 新:
graphRAG knowledgeport.GraphStore,
```

- [ ] **Step 6: 修改 api/wiring/knowledge.go — 改用 infrastructure 构造函数**

```go
// 新增 import：
neo4jadapter "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/neo4j"
// 删除 import：
// knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"  (中 GraphRAG 部分)

// 旧:
graphRAG := knowledge.NewGraphRAG(c.Config.Neo4jURI, c.Config.Neo4jUser, c.Config.Neo4jPassword, c.Logger)
if err := graphRAG.Connect(ctx); err != nil { ... }
c.shutdown = append(c.shutdown, func(_ context.Context) error { return graphRAG.Close() })

// 新:
graphRAG := neo4jadapter.NewGraphAdapter(c.Config.Neo4jURI, c.Config.Neo4jUser, c.Config.Neo4jPassword, c.Logger)
if err := graphRAG.Connect(ctx); err != nil { ... }
c.shutdown = append(c.shutdown, func(_ context.Context) error { return graphRAG.Close() })

// Knowledge struct 字段类型：
// 旧: GraphRAG *knowledge.GraphRAG
// 新: GraphRAG knowledgeport.GraphStore
```

注意：`Knowledge.GraphRAG` 若被其他地方引用需同步修改类型。

- [ ] **Step 7: 删除 application/graphrag_service.go**

```bash
rm internal/knowledge/application/graphrag_service.go
```

- [ ] **Step 8: 验证**

```bash
go build ./internal/knowledge/...
go build ./api/wiring/...
go test -short ./internal/knowledge/...
```

期望：编译通过，测试通过。

- [ ] **Step 9: Commit**

```bash
git add internal/knowledge/domain/port/graph.go \
        internal/knowledge/infrastructure/neo4j/graph_adapter.go \
        internal/knowledge/application/mocks.go \
        internal/knowledge/application/ingest_service.go \
        internal/knowledge/application/rag_service.go \
        api/wiring/knowledge.go
git rm internal/knowledge/application/graphrag_service.go
git commit -m "refactor(knowledge): move GraphRAG impl to infrastructure/neo4j adapter"
```

---

## Task 2: 清除 application 层残余内联 Cypher

ingest_service.GetWorkspaceStats 和 rag_service.GetWorkspaceCollections 仍在 application 层构造 Cypher 字符串传给 `graphRAG.Query()`。将这两个查询封装为接口方法。

**Files:**

- Modify: `internal/knowledge/domain/port/graph.go` — 追加两个语义方法
- Modify: `internal/knowledge/infrastructure/neo4j/graph_adapter.go` — 实现新方法
- Modify: `internal/knowledge/application/ingest_service.go` — GetWorkspaceStats 去掉 Cypher
- Modify: `internal/knowledge/application/rag_service.go` — GetWorkspaceCollections 去掉 Cypher
- Modify: `internal/knowledge/application/mocks.go` — MockGraphStore 补充新方法

**Interfaces:**

- Consumes: Task 1 产出的 `port.GraphStore`
- Produces: 扩展后的 `port.GraphStore`（追加 GetWorkspaceDocCount、GetWorkspaceNames）

- [ ] **Step 1: 在 domain/port/graph.go 追加两个方法**

```go
// 追加到 GraphStore 接口：
// GetWorkspaceDocCount returns the count of Document nodes for the given workspace.
GetWorkspaceDocCount(ctx context.Context, workspace string) (int, error)
// GetWorkspaceNames returns distinct workspace names from all Document nodes.
GetWorkspaceNames(ctx context.Context) ([]string, error)
```

- [ ] **Step 2: 在 infrastructure/neo4j/graph_adapter.go 实现新方法**

```go
func (g *GraphAdapter) GetWorkspaceDocCount(ctx context.Context, workspace string) (int, error) {
 if err := g.ensureConnected(ctx); err != nil {
  return 0, fmt.Errorf("neo4j not available: %w", err)
 }
 result, err := g.session.Run(ctx,
  `MATCH (d:Document) WHERE d.workspace = $workspace RETURN count(d) AS doc_count`,
  map[string]interface{}{"workspace": workspace},
 )
 if err != nil {
  return 0, fmt.Errorf("graph: workspace doc count: %w", err)
 }
 records, err := result.Collect(ctx)
 if err != nil {
  return 0, err
 }
 if len(records) == 0 {
  return 0, nil
 }
 val, _ := records[0].Get("doc_count")
 if c, ok := val.(int64); ok {
  return int(c), nil
 }
 return 0, nil
}

func (g *GraphAdapter) GetWorkspaceNames(ctx context.Context) ([]string, error) {
 if err := g.ensureConnected(ctx); err != nil {
  return nil, fmt.Errorf("neo4j not available: %w", err)
 }
 result, err := g.session.Run(ctx,
  `MATCH (d:Document) WITH d.workspace AS workspace RETURN DISTINCT workspace ORDER BY workspace`,
  nil,
 )
 if err != nil {
  return nil, fmt.Errorf("graph: workspace names: %w", err)
 }
 records, err := result.Collect(ctx)
 if err != nil {
  return nil, err
 }
 names := make([]string, 0, len(records))
 for _, rec := range records {
  if v, ok := rec.Get("workspace"); ok {
   if s, ok := v.(string); ok {
    names = append(names, s)
   }
  }
 }
 return names, nil
}
```

- [ ] **Step 3: 在 mocks.go MockGraphStore 补充新方法**

```go
func (m *MockGraphStore) GetWorkspaceDocCount(_ context.Context, _ string) (int, error) {
 return 0, nil
}
func (m *MockGraphStore) GetWorkspaceNames(_ context.Context) ([]string, error) {
 return []string{}, nil
}
```

- [ ] **Step 4: 修改 ingest_service.go — GetWorkspaceStats 去掉内联 Cypher**

旧代码（约第 304-315 行）：

```go
cypher := `MATCH (d:Document) WHERE d.workspace = $workspace RETURN count(d) as doc_count`
docCountResult, err := ki.graphRAG.Query(ctx, cypher, map[string]interface{}{"workspace": workspace})
if err != nil { return nil, err }
docCount := 0
if resultList, ok := docCountResult.([]interface{}); ok && len(resultList) > 0 {
    if m, ok := resultList[0].(map[string]interface{}); ok {
        if c, ok := m["doc_count"].(int64); ok { docCount = int(c) }
    }
}
```

新代码：

```go
docCount, err := ki.graphRAG.GetWorkspaceDocCount(ctx, workspace)
if err != nil {
    return nil, err
}
```

- [ ] **Step 5: 修改 rag_service.go — GetWorkspaceCollections 去掉内联 Cypher**

旧代码（约第 350-370 行）：

```go
cypher := `MATCH (d:Document) WITH d.workspace as workspace RETURN DISTINCT workspace ORDER BY workspace`
results, err := rs.graphRAG.Query(ctx, cypher, nil)
if err != nil { return nil, err }
var workspaces []string
if resultList, ok := results.([]interface{}); ok {
    for _, r := range resultList {
        if workspace, ok := r.(map[string]interface{}); ok { ... }
    }
}
```

新代码：

```go
workspaces, err := rs.graphRAG.GetWorkspaceNames(ctx)
if err != nil {
    return nil, err
}
return workspaces, nil
```

- [ ] **Step 6: 验证**

```bash
go build ./internal/knowledge/...
go test -short ./internal/knowledge/...
```

- [ ] **Step 7: Commit**

```bash
git add internal/knowledge/domain/port/graph.go \
        internal/knowledge/infrastructure/neo4j/graph_adapter.go \
        internal/knowledge/application/mocks.go \
        internal/knowledge/application/ingest_service.go \
        internal/knowledge/application/rag_service.go
git commit -m "refactor(knowledge): move inline Cypher into GraphStore port methods"
```

---

## Task 3: agent_handler 内联 SQL 移到 infrastructure

`agent_handler.go:156-163` 直接执行 `SELECT name, description FROM %s.skills WHERE id=$1`。需定义一个 `SkillLookup` port，在 infrastructure 实现，handler 持有接口。

**Files:**

- Modify: `internal/agent/domain/port/mcp_tools.go`（或新建 skill_lookup.go）— 追加 SkillLookup 接口
- Create: `internal/agent/infrastructure/persistence/skill_lookup.go` — SQL 实现
- Modify: `api/http/handler/agent_handler.go` — 替换 db 字段为 SkillLookup 接口
- Modify: `api/http/router.go` 或 `api/wiring/wiring.go` — 注入 SkillLookup

**Interfaces:**

- Produces: `agentport.SkillLookup` 接口

- [ ] **Step 1: 检查 agent/domain/port/ 现有文件，确认追加位置**

```bash
ls internal/agent/domain/port/
cat internal/agent/domain/port/mcp_tools.go
```

- [ ] **Step 2: 在 agent/domain/port/ 新建或追加 SkillLookup 接口**

新建 `internal/agent/domain/port/skill_lookup.go`：

```go
package port

import "context"

// SkillInfo carries the display fields for a skill.
type SkillInfo struct {
 Name        string
 Description string
}

// SkillLookup resolves skill metadata for a given tenant.
type SkillLookup interface {
 GetSkillInfo(ctx context.Context, tenantID, skillID string) (SkillInfo, error)
}
```

- [ ] **Step 3: 新建 infrastructure/persistence/skill_lookup.go**

```go
// internal/agent/infrastructure/persistence/skill_lookup.go
package persistence

import (
 "context"
 "fmt"

 agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/jackc/pgx/v5/pgxpool"
)

type SkillLookupAdapter struct {
 pool *pgxpool.Pool
}

func NewSkillLookupAdapter(pool *pgxpool.Pool) agentport.SkillLookup {
 return &SkillLookupAdapter{pool: pool}
}

func (a *SkillLookupAdapter) GetSkillInfo(ctx context.Context, tenantID, skillID string) (agentport.SkillInfo, error) {
 schema := fmt.Sprintf(`"tenant_%s"`, tenantID)
 row := a.pool.QueryRow(ctx,
  fmt.Sprintf(`SELECT name, description FROM %s.skills WHERE id=$1`, schema),
  skillID,
 )
 var info agentport.SkillInfo
 if err := row.Scan(&info.Name, &info.Description); err != nil {
  // 找不到时退化为 skillID 本身，和原逻辑一致
  info.Name = skillID
  info.Description = skillID
 }
 return info, nil
}
```

- [ ] **Step 4: 修改 agent_handler.go — 替换 db 字段**

```go
// 在 AgentHandler struct 中：
// 旧: db PgxPool
// 新: skillLookup agentport.SkillLookup

// 在 NewAgentHandler 参数中：
// 旧: db PgxPool
// 新: skillLookup agentport.SkillLookup

// 在原 SQL 段（约第 152-169 行）：
// 旧:
for _, skillID := range allowedSkills {
    name := skillID
    description := skillID
    if h.db != nil {
        if tc, ok := tenantdb.FromContext(ctx); ok && tc.TenantID != "" {
            schema := `"tenant_` + tc.TenantID + `"`
            _ = h.db.QueryRow(ctx,
                fmt.Sprintf(`SELECT name, description FROM %s.skills WHERE id=$1`, schema),
                skillID,
            ).Scan(&name, &description)
        }
    }
    tools = append(tools, ...)
}
// 新:
for _, skillID := range allowedSkills {
    name := skillID
    description := skillID
    if h.skillLookup != nil {
        if tc, ok := tenantdb.FromContext(ctx); ok && tc.TenantID != "" {
            if info, err := h.skillLookup.GetSkillInfo(ctx, tc.TenantID, skillID); err == nil {
                name = info.Name
                description = info.Description
            }
        }
    }
    tools = append(tools, port.ToolDefinition{
        Name:        skillID,
        Description: name + ": " + description,
        InputSchema: map[string]interface{}{"type": "object"},
    })
}
```

同时删除 `fmt` import（若仅用于 SQL 拼接）。

- [ ] **Step 5: 修改 router.go 注入 SkillLookup**

```go
// 在 NewAgentHandler 调用处（router.go）：
// 找到原来传 db 的位置，替换为：
import agentpersistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"

// 在构造 AgentHandler 时：
skillLookup := agentpersistence.NewSkillLookupAdapter(c.DB())
// 传入 NewAgentHandler(... skillLookup ...)
```

- [ ] **Step 6: 验证**

```bash
go build ./internal/agent/...
go build ./api/http/handler/...
go build ./api/...
go test -short ./internal/agent/...
```

- [ ] **Step 7: Commit**

```bash
git add internal/agent/domain/port/skill_lookup.go \
        internal/agent/infrastructure/persistence/skill_lookup.go \
        api/http/handler/agent_handler.go \
        api/http/router.go
git commit -m "refactor(agent): move inline SQL to SkillLookup infrastructure adapter"
```

---

## Task 4: 清理 admin_handler 的残留 PgxPool 接口

`admin_handler.go` 中 `PgxPool` 接口和 pgxpool import 只是为了给 `agent_handler` 提供类型，Task 3 完成后 `agent_handler` 不再依赖它。

**Files:**

- Modify: `api/http/handler/admin_handler.go` — 删除 PgxPool 定义和 pgxpool/pgx/pgconn imports

**Interfaces:**

- Consumes: Task 3 完成（agent_handler 不再用 PgxPool）

- [ ] **Step 1: 确认 PgxPool 不再被引用**

```bash
grep -rn "PgxPool" api/http/handler/ --include="*.go"
```

期望：只有 admin_handler.go 自身有定义，agent_handler.go 不再使用。

- [ ] **Step 2: 删除 admin_handler.go 中 PgxPool 相关代码**

删除以下代码块（约第 14-31 行）：

```go
import (
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"
)

// PgxPool is the minimal pgxpool interface ...
type PgxPool interface {
    QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
    Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
    Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// Ensure *pgxpool.Pool satisfies PgxPool at compile time.
var _ PgxPool = (*pgxpool.Pool)(nil)
```

- [ ] **Step 3: 验证**

```bash
go vet ./api/http/handler/...
go test -short ./api/http/handler/...
```

- [ ] **Step 4: Commit**

```bash
git add api/http/handler/admin_handler.go
git commit -m "refactor(handler): remove stale PgxPool interface from admin_handler"
```

---

## 自检

**Spec 覆盖：**

- ✅ graphrag_service.go（application 层 neo4j 直依赖）→ Task 1
- ✅ ingest_service/rag_service 内联 Cypher → Task 2
- ✅ agent_handler 内联 SQL → Task 3
- ✅ admin_handler PgxPool 接口 → Task 4

**Placeholder 扫描：** 无 TBD / TODO / "类似 Task N"。

**类型一致性：**

- `port.GraphStore` 在 Task 1 定义，Task 2 追加方法，Task 1 的 mock 在 Task 2 Step 3 中同步补充。
- `agentport.SkillLookup` 在 Task 3 Step 2 定义，Step 3/4/5 使用，类型名一致。
