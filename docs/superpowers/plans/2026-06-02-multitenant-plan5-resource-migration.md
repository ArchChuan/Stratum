# Multi-Tenant Plan 5: 资源持久化 — 现有模块接入多租户

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Agent/Memory/Knowledge/MCP/Skill 五个模块的内存 Registry/Manager 替换为 PostgreSQL 持久化，同时通过 `tenantdb.ExecTenant` 实现 per-tenant schema 数据隔离。

**Architecture:** 每个模块的写操作通过 `pkg/tenantdb.ExecTenant(ctx, fn)` 在 `tenant_{id}` schema 下执行 pgx 查询；读操作通过 `SET search_path` 切换到对应 schema。向量操作（Milvus）使用 `tenantdb.TenantCollection(tenantID, baseName)` 生成租户专属集合名；图操作（Neo4j）使用 `tenantdb.TenantLabel(tenantID, label)` 和 `tenantdb.TenantSubject(tenantID, id)` 区分租户节点。Handler 层从 `c.Request.Context()` 取出 `TenantID`（由 Plan 3 提供的 `TenantMiddleware` 注入），向下传递给 service 层。

**Tech Stack:** Go 1.24 · Gin v1.9 · `jackc/pgx/v5` · `pkg/tenantdb`（Plan 3）· `pkg/postgres`（Plan 1）· Milvus · Neo4j · `go.uber.org/zap` · `github.com/google/uuid`

---

## 前置条件

- Plan 1 已落地：`pkg/postgres` 包存在，`postgres.Pool` 可注入
- Plan 3 已落地：`pkg/tenantdb` 包存在，提供以下函数：
  ```go
  // 在 tenant_{tenantID} schema 下执行 fn
  func ExecTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error

  // 生成租户专属 Milvus 集合名，如 "tenant_abc_kb"
  func TenantCollection(tenantID, base string) string

  // 生成租户专属 Neo4j 标签，如 "T_abc_Document"
  func TenantLabel(tenantID, label string) string

  // 生成租户专属 Neo4j 节点 subject，如 "tenant_abc::doc_id"
  func TenantSubject(tenantID, id string) string
  ```
- `api/middleware` 中存在 `TenantMiddleware`，会将 `tenantID` 注入 `context.Context`（key: `"tenant_id"`）
- 运行 `docker compose up -d` 启动 Postgres、Milvus、Neo4j

---

## File Map

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/agent/registry.go` | Modify | `map[string]Agent` → pgx 查询 `agents` 表；方法签名首参改为 `context.Context` |
| `internal/agent/registry_test.go` | Modify | 替换为 integration 测试，使用真实 pgx |
| `api/handler/agent_handler.go` | Modify | 从 ctx 取 tenantID，传入 registry 方法 |
| `api/handler/agent_handler_test.go` | Modify | 更新 mock 调用，加 tenantID |
| `internal/memory/manager.go` | Modify | `Add`/`Delete`/`Clear` 持久化到 `memory_entries` 表；长期向量用 `TenantCollection` |
| `api/handler/memory_handler.go` | Modify | 从 ctx 取 tenantID，构造 `SessionContext.TenantID` |
| `api/handler/memory_handler_test.go` | Modify | 同上 |
| `internal/knowledge/knowledge_ingest.go` | Modify | `IngestDocument` 写 `knowledge_docs` 表；`TenantCollection` 替换硬编码集合名；`TenantLabel`/`TenantSubject` 替换 Neo4j 节点 |
| `internal/knowledge/rag_service.go` | Modify | `Query` 中集合名改用 `TenantCollection`；graph 查询用 `TenantLabel` |
| `api/handler/rag_handler.go` | Modify | 从 ctx 取 tenantID，传入 ingest/query |
| `api/handler/rag_handler_test.go` | Modify | 更新测试 |
| `internal/mcp/client_manager.go` | Modify | `Connect`/`Disconnect`/`GetAllClients` 读写 `mcp_configs` 表 |
| `api/handler/mcp_handler.go` | Modify | 从 ctx 取 tenantID，传入 manager |
| `api/handler/mcp_handler_test.go` | Modify | 更新测试 |
| `internal/orchestrator/registry.go` | Modify | `map[string]Skill` → pgx 查询 `skills` 表；方法签名首参改为 `context.Context` |
| `api/handler/skill_handler.go` | Modify | 从 ctx 取 tenantID，传入 registry |
| `api/handler/skill_handler_test.go` | Modify | 更新测试 |
| `api/router.go` | Modify | 注入 `*pgxpool.Pool`；各路由组加 `TenantMiddleware` |

---

## 共享辅助函数约定

所有 handler 从 context 提取 tenantID 的方式统一为：

```go
// api/handler/tenant.go  (新建，Task 9 之前手动创建，或 Task 1 创建)
package handler

import (
    "fmt"
    "github.com/gin-gonic/gin"
)

func tenantIDFromCtx(c *gin.Context) (string, bool) {
    v, exists := c.Get("tenant_id")
    if !exists {
        return "", false
    }
    s, ok := v.(string)
    return s, ok && s != ""
}

func respondMissingTenant(c *gin.Context) {
    c.JSON(401, gin.H{"error": "tenant context required"})
}
```

---

## SQL Schema 约定

每个 tenant schema 在 Plan 1 的 `MigrateSchema` 中创建。本 Plan 假设各表已由 Plan 1 建好，或在本 Plan 中补充 migration SQL。各表 DDL 如下（在 per-tenant schema 下执行，无需 tenant_id 列）：

```sql
-- agents 表
CREATE TABLE IF NOT EXISTS agents (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    description TEXT,
    persona     TEXT,
    system_prompt TEXT,
    llm_model   TEXT NOT NULL,
    max_iterations INT NOT NULL DEFAULT 5,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- memory_entries 表
CREATE TABLE IF NOT EXISTS memory_entries (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    agent_id    TEXT,
    importance  DOUBLE PRECISION DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ
);

-- knowledge_docs 表
CREATE TABLE IF NOT EXISTS knowledge_docs (
    id          TEXT PRIMARY KEY,
    filename    TEXT NOT NULL,
    workspace   TEXT NOT NULL,
    chunk_count INT NOT NULL DEFAULT 0,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- mcp_configs 表
CREATE TABLE IF NOT EXISTS mcp_configs (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    transport   TEXT NOT NULL,
    endpoint    TEXT,
    version     TEXT,
    config_json JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- skills 表
CREATE TABLE IF NOT EXISTS skills (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    type        TEXT NOT NULL,
    code        TEXT,
    language    TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

### Task 1: Tenant Helper + Agent Registry 持久化

**Files:**
- Create: `api/handler/tenant.go`
- Modify: `internal/agent/registry.go`
- Modify: `internal/agent/registry_test.go`

- [ ] **Step 1: 创建 tenant helper**

新建文件 `api/handler/tenant.go`：

```go
package handler

import "github.com/gin-gonic/gin"

func tenantIDFromCtx(c *gin.Context) (string, bool) {
    v, exists := c.Get("tenant_id")
    if !exists {
        return "", false
    }
    s, ok := v.(string)
    return s, ok && s != ""
}

func respondMissingTenant(c *gin.Context) {
    c.JSON(401, gin.H{"error": "tenant context required"})
}
```

- [ ] **Step 2: 写 Agent Registry 失败测试**

替换 `internal/agent/registry_test.go` 为 integration 测试：

```go
//go:build integration

package agent_test

import (
    "context"
    "os"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.uber.org/zap"
)

func testPool(t *testing.T) *pgxpool.Pool {
    t.Helper()
    dsn := os.Getenv("TEST_DATABASE_URL")
    if dsn == "" {
        t.Skip("TEST_DATABASE_URL not set")
    }
    pool, err := pgxpool.New(context.Background(), dsn)
    if err != nil {
        t.Fatalf("pgxpool.New: %v", err)
    }
    t.Cleanup(pool.Close)
    return pool
}

func TestAgentRegistryCRUD(t *testing.T) {
    pool := testPool(t)
    tenantID := "test_tenant_r1"
    // provision schema + table
    ctx := context.Background()
    _, _ = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS tenant_"+tenantID)
    _, _ = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenant_`+tenantID+`.agents (
        id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL,
        description TEXT, persona TEXT, system_prompt TEXT,
        llm_model TEXT NOT NULL, max_iterations INT NOT NULL DEFAULT 5,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
    t.Cleanup(func() {
        pool.Exec(context.Background(), "DROP SCHEMA tenant_"+tenantID+" CASCADE")
    })

    logger, _ := zap.NewDevelopment()
    reg := agent.NewRegistry(pool, logger)

    cfg := &agent.AgentConfig{
        ID: "agent-1", Name: "Test", Type: agent.ReActAgent,
        LLMModel: "gpt-4o", MaxIterations: 5,
    }
    a := agent.NewBaseAgent(cfg, logger)

    if err := reg.Register(ctx, tenantID, a); err != nil {
        t.Fatalf("Register: %v", err)
    }

    got, ok := reg.Get(ctx, tenantID, "agent-1")
    if !ok || got.GetConfig().Name != "Test" {
        t.Fatalf("Get failed: ok=%v", ok)
    }

    all := reg.GetAll(ctx, tenantID)
    if len(all) != 1 {
        t.Fatalf("GetAll want 1, got %d", len(all))
    }

    if err := reg.Remove(ctx, tenantID, "agent-1"); err != nil {
        t.Fatalf("Remove: %v", err)
    }
    if _, ok := reg.Get(ctx, tenantID, "agent-1"); ok {
        t.Fatal("agent should be deleted")
    }
}
```

- [ ] **Step 3: 运行失败的测试确认预期**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -tags=integration ./internal/agent/ -run TestAgentRegistryCRUD -v 2>&1 | head -20
```

预期：`FAIL` — `NewRegistry` 签名不匹配。

- [ ] **Step 4: 改造 `internal/agent/registry.go`**

完整替换文件内容：

```go
package agent

import (
    "context"
    "fmt"

    "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.uber.org/zap"
)

// Registry 通过 PostgreSQL 持久化 Agent，按 tenant schema 隔离。
type Registry struct {
    pool   *pgxpool.Pool
    logger *zap.Logger
}

// NewRegistry 创建 Registry，pool 不可为 nil。
func NewRegistry(pool *pgxpool.Pool, logger *zap.Logger) *Registry {
    return &Registry{pool: pool, logger: logger}
}

// Register 将 agent 配置写入 tenant_{tenantID}.agents 表。
// 若 ID 已存在则返回错误。
func (r *Registry) Register(ctx context.Context, tenantID string, a Agent) error {
    cfg := a.GetConfig()
    return tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `INSERT INTO agents (id, name, type, description, persona, system_prompt, llm_model, max_iterations)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
            cfg.ID, cfg.Name, string(cfg.Type), cfg.Description,
            cfg.Persona, cfg.SystemPrompt, cfg.LLMModel, cfg.MaxIterations,
        )
        if err != nil {
            return fmt.Errorf("agent with ID %s already registered: %w", cfg.ID, err)
        }
        r.logger.Info("agent registered",
            zap.String("tenant", tenantID),
            zap.String("agent_id", cfg.ID))
        return nil
    })
}

// Get 通过 ID 查询 agent 配置，返回 BaseAgent 实例。
func (r *Registry) Get(ctx context.Context, tenantID, id string) (Agent, bool) {
    var cfg AgentConfig
    err := tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        return tx.QueryRow(ctx,
            `SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations
             FROM agents WHERE id = $1`, id).
            Scan(&cfg.ID, &cfg.Name, &cfg.Type, &cfg.Description,
                &cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations)
    })
    if err != nil {
        return nil, false
    }
    return NewBaseAgent(&cfg, r.logger), true
}

// GetAll 返回 tenant 下所有 agent 配置（封装为 BaseAgent）。
func (r *Registry) GetAll(ctx context.Context, tenantID string) []Agent {
    var agents []Agent
    _ = tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        rows, err := tx.Query(ctx,
            `SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations
             FROM agents ORDER BY created_at`)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var cfg AgentConfig
            if err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.Type, &cfg.Description,
                &cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations); err != nil {
                continue
            }
            agents = append(agents, NewBaseAgent(&cfg, r.logger))
        }
        return rows.Err()
    })
    return agents
}

// Remove 从 tenant schema 删除 agent。
func (r *Registry) Remove(ctx context.Context, tenantID, id string) error {
    return tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        tag, err := tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
        if err != nil {
            return err
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("agent with ID %s not found", id)
        }
        r.logger.Info("agent removed",
            zap.String("tenant", tenantID),
            zap.String("agent_id", id))
        return nil
    })
}
```

- [ ] **Step 5: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
export TEST_DATABASE_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
go test -tags=integration ./internal/agent/ -run TestAgentRegistryCRUD -v
```

预期：`PASS`

- [ ] **Step 6: 运行 go vet**

```bash
go vet ./internal/agent/...
```

预期：无错误

- [ ] **Step 7: Commit**

```bash
git add api/handler/tenant.go internal/agent/registry.go internal/agent/registry_test.go
git commit -m "feat(agent): persist registry to PostgreSQL with tenant isolation"
```


---

### Task 2: Agent Handler 改造

**Files:**
- Modify: `api/handler/agent_handler.go`
- Modify: `api/handler/agent_handler_test.go`

- [ ] **Step 1: 写失败单元测试**

在 `api/handler/agent_handler_test.go` 中添加（不替换现有测试，在文件末尾追加）：

```go
func TestGetAllAgents_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    // Registry 需要 pool，传 nil 触发 middleware 检查前先 panic → 测试 handler 拦截
    h := &AgentHandler{agentRegistry: nil, logger: logger}
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = httptest.NewRequest("GET", "/agents", nil)
    // 不注入 tenant_id
    h.GetAllAgents(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test ./api/handler/ -run TestGetAllAgents_MissingTenant -v
```

预期：`FAIL` — handler 目前不检查 tenant。

- [ ] **Step 3: 改造 `agent_handler.go` 中的方法**

修改 `AgentHandler` struct，将 `agentRegistry *agent.Registry` 类型不变（签名已改，pool 在 router 层注入）。
修改以下四个方法，仅在头部加 tenantID 提取逻辑，其余不动：

```go
func (h *AgentHandler) GetAllAgents(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    agents := h.agentRegistry.GetAll(c.Request.Context(), tenantID)
    // ... 原有 responses 构建逻辑不变 ...
    responses := make([]AgentResponse, 0, len(agents))
    for _, a := range agents {
        cfg := a.GetConfig()
        responses = append(responses, AgentResponse{
            ID: cfg.ID, Name: cfg.Name, Type: string(cfg.Type),
            Description: cfg.Description, Persona: cfg.Persona,
            SystemPrompt: cfg.SystemPrompt, LLMModel: cfg.LLMModel,
            MaxIterations: cfg.MaxIterations, AllowedSkills: []string{},
            CreatedAt: time.Now().Format(time.RFC3339),
        })
    }
    c.JSON(http.StatusOK, gin.H{"agents": responses})
}

func (h *AgentHandler) GetAgent(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    a, ok2 := h.agentRegistry.Get(c.Request.Context(), tenantID, id)
    if !ok2 {
        h.logger.Warn("agent not found", zap.String("id", id))
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "agent not found"})
        return
    }
    cfg := a.GetConfig()
    c.JSON(http.StatusOK, AgentResponse{
        ID: cfg.ID, Name: cfg.Name, Type: string(cfg.Type),
        Description: cfg.Description, Persona: cfg.Persona,
        SystemPrompt: cfg.SystemPrompt, LLMModel: cfg.LLMModel,
        MaxIterations: cfg.MaxIterations, AllowedSkills: []string{},
        CreatedAt: time.Now().Format(time.RFC3339),
    })
}

func (h *AgentHandler) CreateAgent(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req CreateAgentRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
        return
    }
    id := uuid.New().String()
    agentType := agent.ReActAgent
    switch req.Type {
    case "react":   agentType = agent.ReActAgent
    case "cot":     agentType = agent.CoTAgent
    case "planning": agentType = agent.PlanningAgent
    case "tool_calling": agentType = agent.ToolCallingAgent
    case "rag":     agentType = agent.RAGAgent
    case "swarm":   agentType = agent.SwarmAgent
    }
    cfg := &agent.AgentConfig{
        ID: id, Name: req.Name, Type: agentType,
        Description: req.Description, Persona: req.Persona,
        SystemPrompt: req.SystemPrompt, LLMModel: req.LLMModel,
        MaxIterations: req.MaxIterations, Capabilities: []agent.AgentCapability{},
    }
    a := agent.NewBaseAgent(cfg, h.logger).WithMetrics(h.metrics)
    if err := h.agentRegistry.Register(c.Request.Context(), tenantID, a); err != nil {
        c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: fmt.Sprintf("failed to create agent: %v", err)})
        return
    }
    c.JSON(http.StatusCreated, AgentResponse{
        ID: id, Name: req.Name, Type: string(agentType),
        Description: req.Description, Persona: req.Persona,
        SystemPrompt: req.SystemPrompt, LLMModel: req.LLMModel,
        MaxIterations: req.MaxIterations, AllowedSkills: req.AllowedSkills,
        CreatedAt: time.Now().Format(time.RFC3339),
    })
}

func (h *AgentHandler) DeleteAgent(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    if err := h.agentRegistry.Remove(c.Request.Context(), tenantID, id); err != nil {
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "agent not found"})
        return
    }
    c.JSON(http.StatusOK, gin.H{"message": "agent deleted successfully"})
}
```

`ExecuteAgent` 也加 tenantID 检查，但 `Get` 调用改为传 ctx 和 tenantID：

```go
func (h *AgentHandler) ExecuteAgent(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    a, ok2 := h.agentRegistry.Get(c.Request.Context(), tenantID, id)
    if !ok2 {
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "agent not found"})
        return
    }
    // 以下逻辑不变 (ShouldBindJSON, Execute, 返回结果)
    var req ExecuteAgentRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
        return
    }
    options := []agent.ExecutionOption{agent.WithMaxSteps(a.GetConfig().MaxIterations)}
    if req.Options != nil {
        if maxSteps, ok := req.Options["maxSteps"].(float64); ok {
            options = append(options, agent.WithMaxSteps(int(maxSteps)))
        }
        if timeout, ok := req.Options["timeout"].(float64); ok {
            options = append(options, agent.WithTimeout(time.Duration(timeout)*time.Second))
        }
    }
    result, err := a.Execute(c.Request.Context(), req.Query, options...)
    if err != nil {
        c.JSON(http.StatusOK, AgentExecutionResult{AgentID: id, Input: req.Query, Error: err.Error()})
        return
    }
    thoughtsJSON, _ := json.Marshal(result.Thoughts)
    toolCallsJSON, _ := json.Marshal(result.ToolCalls)
    c.JSON(http.StatusOK, AgentExecutionResult{
        AgentID: id, Input: req.Query, Output: result.Output,
        Steps: result.Steps, TokensUsed: result.TokensUsed,
        Duration: result.Duration.String(),
        Thoughts: result.Thoughts, ToolCalls: result.ToolCalls,
        Metadata: map[string]interface{}{
            "thoughtsJSON":  string(thoughtsJSON),
            "toolCallsJSON": string(toolCallsJSON),
        },
    })
}
```

- [ ] **Step 4: 运行测试**

```bash
go test ./api/handler/ -run TestGetAllAgents_MissingTenant -v
```

预期：`PASS`

- [ ] **Step 5: go vet**

```bash
go vet ./api/handler/...
```

- [ ] **Step 6: Commit**

```bash
git add api/handler/agent_handler.go api/handler/agent_handler_test.go api/handler/tenant.go
git commit -m "feat(handler/agent): add tenant isolation to agent handler"
```


---

### Task 3: Memory Manager 持久化

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/manager_test.go`

- [ ] **Step 1: 写失败的 integration 测试**

在 `internal/memory/manager_test.go` 顶部添加 build tag，末尾追加：

```go
//go:build integration
```

追加测试：

```go
func TestMemoryManagerPersist(t *testing.T) {
    pool := testPoolMemory(t) // 见下
    tenantID := "test_tenant_m1"
    ctx := context.Background()
    _, _ = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS tenant_"+tenantID)
    _, _ = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenant_`+tenantID+`.memory_entries (
        id TEXT PRIMARY KEY, type TEXT NOT NULL, role TEXT NOT NULL,
        content TEXT NOT NULL, session_id TEXT NOT NULL, user_id TEXT NOT NULL,
        agent_id TEXT, importance DOUBLE PRECISION DEFAULT 0,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), expires_at TIMESTAMPTZ)`)
    t.Cleanup(func() {
        pool.Exec(context.Background(), "DROP SCHEMA tenant_"+tenantID+" CASCADE")
    })

    logger, _ := zap.NewDevelopment()
    cfg := memory.DefaultMemoryConfig()
    mgr := memory.NewMemoryManager(cfg, logger, nil, nil, nil, pool)

    entry := &memory.MemoryEntry{
        ID: "e1", Type: memory.ShortTermMemory, Role: "user",
        Content: "hello", SessionID: "s1", UserID: "u1", TenantID: tenantID,
    }
    if err := mgr.Add(ctx, entry); err != nil {
        t.Fatalf("Add: %v", err)
    }
    got, err := mgr.Get(ctx, "e1")
    if err != nil || got.Content != "hello" {
        t.Fatalf("Get: err=%v, entry=%v", err, got)
    }
    if err := mgr.Delete(ctx, "e1"); err != nil {
        t.Fatalf("Delete: %v", err)
    }
}

func testPoolMemory(t *testing.T) *pgxpool.Pool {
    t.Helper()
    dsn := os.Getenv("TEST_DATABASE_URL")
    if dsn == "" {
        t.Skip("TEST_DATABASE_URL not set")
    }
    pool, err := pgxpool.New(context.Background(), dsn)
    if err != nil {
        t.Fatalf("pgxpool.New: %v", err)
    }
    t.Cleanup(pool.Close)
    return pool
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test -tags=integration ./internal/memory/ -run TestMemoryManagerPersist -v 2>&1 | head -10
```

预期：`FAIL` — `NewMemoryManager` 签名不接受 pool。

- [ ] **Step 3: 改造 `NewMemoryManager` 加 pool 参数**

在 `internal/memory/manager.go` 中：

1. 给 `MemoryManager` struct 加字段：
```go
pool *pgxpool.Pool
```

2. `NewMemoryManager` 函数签名末尾加 `pool *pgxpool.Pool`，并在返回前赋值 `m.pool = pool`。

3. 改造 `Add` 方法，在成功写短期内存后，持久化到 DB：

```go
func (m *MemoryManager) Add(ctx context.Context, entry *MemoryEntry) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if err := m.shortTerm.Add(ctx, entry); err != nil {
        m.logger.Warn("failed to add to short-term memory", zap.Error(err))
    }

    if m.config.EnableVectorSearch && m.longTerm != nil {
        if err := m.longTerm.AddWithVector(ctx, entry, entry.Vector); err != nil {
            m.logger.Warn("failed to add to long-term memory", zap.Error(err))
        }
    }

    if m.config.EnableEntityExtraction && m.entity != nil {
        sessionCtx := &SessionContext{
            TenantID: entry.TenantID, UserID: entry.UserID,
            SessionID: entry.SessionID, AgentID: entry.AgentID,
        }
        if _, err := m.entity.ExtractEntities(ctx, entry.Content, sessionCtx); err != nil {
            m.logger.Warn("failed to extract entities", zap.Error(err))
        }
    }

    // 持久化到 DB（仅当 pool 和 tenantID 可用时）
    if m.pool != nil && entry.TenantID != "" {
        if err := tenantdb.ExecTenant(ctx, m.pool, entry.TenantID, func(tx pgx.Tx) error {
            _, err := tx.Exec(ctx,
                `INSERT INTO memory_entries (id, type, role, content, session_id, user_id, agent_id, importance, expires_at)
                 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
                 ON CONFLICT (id) DO NOTHING`,
                entry.ID, string(entry.Type), entry.Role, entry.Content,
                entry.SessionID, entry.UserID, entry.AgentID,
                entry.Importance, entry.ExpiresAt,
            )
            return err
        }); err != nil {
            m.logger.Warn("failed to persist memory entry", zap.Error(err))
        }
    }

    return nil
}
```

4. 改造 `Delete` 方法，DB 侧删除（需要从 entry 取 tenantID，所以先 Get，再删）：

```go
func (m *MemoryManager) Delete(ctx context.Context, id string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 先从短期记忆取出 entry 以获得 tenantID
    entry, err := m.shortTerm.Get(ctx, id)

    if err := m.shortTerm.Delete(ctx, id); err != nil {
        m.logger.Warn("failed to delete from short-term memory", zap.Error(err))
    }

    if m.pool != nil && err == nil && entry != nil && entry.TenantID != "" {
        if dbErr := tenantdb.ExecTenant(ctx, m.pool, entry.TenantID, func(tx pgx.Tx) error {
            _, e := tx.Exec(ctx, `DELETE FROM memory_entries WHERE id = $1`, id)
            return e
        }); dbErr != nil {
            m.logger.Warn("failed to delete memory from DB", zap.Error(dbErr))
        }
    }

    return nil
}
```

5. 在 `manager.go` 加 import：

```go
import (
    // 现有 import ...
    "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 4: 运行测试**

```bash
export TEST_DATABASE_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
go test -tags=integration ./internal/memory/ -run TestMemoryManagerPersist -v
```

预期：`PASS`

- [ ] **Step 5: go vet**

```bash
go vet ./internal/memory/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/memory/manager.go internal/memory/manager_test.go
git commit -m "feat(memory): persist entries to PostgreSQL with tenant isolation"
```


---

### Task 4: Memory Handler 改造

**Files:**
- Modify: `api/handler/memory_handler.go`
- Modify: `api/handler/memory_handler_test.go`

- [ ] **Step 1: 写失败测试**

在 `api/handler/memory_handler_test.go` 末尾追加：

```go
func TestAddMemory_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    cfg := memory.DefaultMemoryConfig()
    mgr := memory.NewMemoryManager(cfg, logger, nil, nil, nil, nil)
    h := NewMemoryHandler(mgr, logger)

    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    body := `{"content":"hi","role":"user","type":"short_term","session_id":"s1","user_id":"u1"}`
    c.Request = httptest.NewRequest("POST", "/memory", strings.NewReader(body))
    c.Request.Header.Set("Content-Type", "application/json")
    // 不注入 tenant_id
    h.AddMemory(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./api/handler/ -run TestAddMemory_MissingTenant -v
```

预期：`FAIL`

- [ ] **Step 3: 改造 `memory_handler.go`**

找到 `AddMemory`、`GetMemory`、`DeleteMemory`、`SearchMemory`、`GetStats`、`ClearSession` 方法，在每个方法开头加 tenantID 提取，并将 tenantID 注入到 `MemoryEntry.TenantID` 或 `SessionContext.TenantID`。

`AddMemory` 关键改动：

```go
func (h *MemoryHandler) AddMemory(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req AddMemoryRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    entry := &memory.MemoryEntry{
        ID:        uuid.New().String(),
        Type:      memory.MemoryType(req.Type),
        Role:      req.Role,
        Content:   req.Content,
        TenantID:  tenantID,          // 注入 tenantID
        UserID:    req.UserID,
        SessionID: req.SessionID,
        AgentID:   req.AgentID,
        Timestamp: time.Now(),
    }
    if err := h.memoryManager.Add(c.Request.Context(), entry); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusCreated, gin.H{"id": entry.ID, "status": "added"})
}
```

`SearchMemory` 关键改动（SessionContext 注入 TenantID）：

```go
func (h *MemoryHandler) SearchMemory(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req SearchMemoryRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    searchReq := &memory.MemorySearchRequest{
        Query: req.Query,
        Limit: req.Limit,
        Context: &memory.SessionContext{
            TenantID:  tenantID,
            SessionID: req.SessionID,
            UserID:    req.UserID,
        },
    }
    results, err := h.memoryManager.Search(c.Request.Context(), searchReq)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"results": results, "count": len(results)})
}
```

其余方法（`GetMemory`、`DeleteMemory`、`GetStats`、`ClearSession`、`GetEntities`、`ExtractEntities`、`GetSummary`）均在方法头加相同的 tenantID 检查块；不修改业务逻辑。

- [ ] **Step 4: 运行测试**

```bash
go test ./api/handler/ -run TestAddMemory_MissingTenant -v
```

预期：`PASS`

- [ ] **Step 5: go vet**

```bash
go vet ./api/handler/...
```

- [ ] **Step 6: Commit**

```bash
git add api/handler/memory_handler.go api/handler/memory_handler_test.go
git commit -m "feat(handler/memory): inject tenant isolation into memory handler"
```

