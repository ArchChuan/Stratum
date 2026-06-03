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


---

### Task 5: Knowledge Ingest 改造

**Files:**
- Modify: `internal/knowledge/knowledge_ingest.go`
- Modify: `internal/knowledge/knowledge_ingest_test.go`

持久化目标：`IngestDocument` 在向量写入前先写 `knowledge_docs` 表记录文档元数据；Milvus 集合名改用 `tenantdb.TenantCollection`；Neo4j 节点用 `tenantdb.TenantLabel` 和 `tenantdb.TenantSubject`。

- [ ] **Step 1: 写失败的 integration 测试**

在 `internal/knowledge/knowledge_ingest_test.go` 顶部加 build tag，末尾追加：

```go
//go:build integration

package knowledge_test

import (
    "context"
    "os"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.uber.org/zap"
)

func testPoolIngest(t *testing.T) *pgxpool.Pool {
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

func TestIngestDocumentPersistence(t *testing.T) {
    pool := testPoolIngest(t)
    tenantID := "test_tenant_ki1"
    ctx := context.Background()
    _, _ = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS tenant_"+tenantID)
    _, _ = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenant_`+tenantID+`.knowledge_docs (
        id TEXT PRIMARY KEY, filename TEXT NOT NULL, workspace TEXT NOT NULL,
        chunk_count INT NOT NULL DEFAULT 0, ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
    t.Cleanup(func() {
        pool.Exec(context.Background(), "DROP SCHEMA tenant_"+tenantID+" CASCADE")
    })

    logger, _ := zap.NewDevelopment()
    // nil vectorStore/graphRAG: test only the DB persistence path
    ki := knowledge.NewKnowledgeIngest(nil, nil, nil, nil, nil, logger, pool)

    req := knowledge.IngestDocumentRequest{
        TenantID:     tenantID,
        Workspace:    "ws1",
        DocumentData: []byte("hello world content"),
        FileName:     "test.txt",
        DocumentID:   "doc-1",
    }
    // Should not panic on nil stores when pool is set
    _, err := ki.PersistDocMeta(ctx, req, 3)
    if err != nil {
        t.Fatalf("PersistDocMeta: %v", err)
    }

    // Verify row exists
    var id string
    err = pool.QueryRow(ctx,
        "SELECT id FROM tenant_"+tenantID+".knowledge_docs WHERE id = 'doc-1'").Scan(&id)
    if err != nil || id != "doc-1" {
        t.Fatalf("expected row doc-1, got err=%v id=%s", err, id)
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -tags=integration ./internal/knowledge/ -run TestIngestDocumentPersistence -v 2>&1 | head -15
```

预期：`FAIL` — `NewKnowledgeIngest` 签名不接受 pool，`PersistDocMeta` 不存在。

- [ ] **Step 3: 改造 `KnowledgeIngest`**

在 `internal/knowledge/knowledge_ingest.go` 中：

1. 给 `KnowledgeIngest` struct 加字段：
```go
pool *pgxpool.Pool
```

2. `NewKnowledgeIngest` 函数签名末尾加 `pool *pgxpool.Pool`，并赋值。

3. 在 `IngestDocumentRequest` struct 加字段：
```go
TenantID string
```

4. 新增内部方法 `PersistDocMeta`（供 `IngestDocument` 和测试调用）：

```go
// PersistDocMeta 将文档元信息写入 tenant_{tenantID}.knowledge_docs 表。
func (ki *KnowledgeIngest) PersistDocMeta(ctx context.Context, req IngestDocumentRequest, chunkCount int) error {
    if ki.pool == nil || req.TenantID == "" {
        return nil
    }
    return tenantdb.ExecTenant(ctx, ki.pool, req.TenantID, func(tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `INSERT INTO knowledge_docs (id, filename, workspace, chunk_count)
             VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO UPDATE
             SET chunk_count = EXCLUDED.chunk_count`,
            req.DocumentID, req.FileName, req.Workspace, chunkCount,
        )
        return err
    })
}
```

5. 在 `IngestDocument` 方法的 `ki.vectorStore.Flush` 之后调用：

```go
if err := ki.PersistDocMeta(ctx, req, len(chunks)); err != nil {
    ki.logger.Warn("failed to persist doc meta", zap.Error(err))
    result.Errors = append(result.Errors, fmt.Sprintf("doc meta persist failed: %v", err))
}
```

6. 将 Milvus 集合名 `fmt.Sprintf("%s_kb", req.Workspace)` 替换为：

```go
// Before (two occurrences):
collectionName := fmt.Sprintf("%s_kb", req.Workspace)

// After:
collectionName := req.Workspace + "_kb"
if req.TenantID != "" {
    collectionName = tenantdb.TenantCollection(req.TenantID, "kb")
}
```

7. 将 `graphRAG.CreateNode` 中的 Neo4j label 替换为租户化标签：

```go
// Before:
ki.graphRAG.CreateNode(ctx, "Document", docNodeProps)
ki.graphRAG.CreateNode(ctx, "DocumentChunk", chunkProps)

// After:
docLabel := "Document"
chunkLabel := "DocumentChunk"
if req.TenantID != "" {
    docLabel   = tenantdb.TenantLabel(req.TenantID, "Document")
    chunkLabel = tenantdb.TenantLabel(req.TenantID, "DocumentChunk")
    docNodeProps["id"] = tenantdb.TenantSubject(req.TenantID, req.DocumentID)
}
ki.graphRAG.CreateNode(ctx, docLabel, docNodeProps)
// ... chunkLabel 同理 ...
```

8. 在 `knowledge_ingest.go` 加 import：

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
go test -tags=integration ./internal/knowledge/ -run TestIngestDocumentPersistence -v
```

预期：`PASS`

- [ ] **Step 5: go vet**

```bash
go vet ./internal/knowledge/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/knowledge/knowledge_ingest.go internal/knowledge/knowledge_ingest_test.go
git commit -m "feat(knowledge): persist docs to PostgreSQL, use TenantCollection/Label for Milvus/Neo4j"
```


---

### Task 6: RAG Service 改造

**Files:**
- Modify: `internal/knowledge/rag_service.go`
- Modify: `internal/knowledge/rag_service.go` (RAGQueryRequest 加 TenantID)
- Modify: `api/handler/rag_handler.go`
- Modify: `api/handler/rag_handler_test.go`

改造目标：`RAGQueryRequest` 加 `TenantID` 字段；`Query` 方法中集合名改用 `TenantCollection`；graph 查询节点 label 改用 `TenantLabel`。

- [ ] **Step 1: 写失败测试**

在 `api/handler/rag_handler_test.go` 末尾追加：

```go
func TestUploadDocument_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    h := NewRAGHandler(nil, nil, logger)
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = httptest.NewRequest("POST", "/knowledge/ingest", nil)
    // 不注入 tenant_id
    h.UploadDocument(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}

func TestQuery_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    h := NewRAGHandler(nil, nil, logger)
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    body := `{"question":"q","workspace":"ws","mode":"vector"}`
    c.Request = httptest.NewRequest("POST", "/knowledge/query", strings.NewReader(body))
    c.Request.Header.Set("Content-Type", "application/json")
    // 不注入 tenant_id
    h.Query(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test ./api/handler/ -run "TestUploadDocument_MissingTenant|TestQuery_MissingTenant" -v 2>&1 | head -15
```

预期：`FAIL`

- [ ] **Step 3: 改造 `rag_service.go`**

在 `RAGQueryRequest` 加字段：

```go
type RAGQueryRequest struct {
    Question  string
    Workspace string
    TenantID  string // 新增
    Mode      string
    TopK      int
}
```

在 `Query` 方法中，集合名构建替换：

```go
// Before:
collectionName := fmt.Sprintf("%s_kb", req.Workspace)

// After:
collectionName := req.Workspace + "_kb"
if req.TenantID != "" {
    collectionName = tenantdb.TenantCollection(req.TenantID, "kb")
}
```

在 `queryGraph` 调用后，将 `graphEntities` 的 label 加租户前缀（通过 `TenantLabel` 过滤，不改 label 赋值，仅在 FullTextSearch 时传 label 参数即可）。如果 `GraphRAG.FullTextSearch` 支持 label 过滤，则传入：

```go
// 若 req.TenantID 非空，限定 label 查询范围
labelFilter := "Entity"
if req.TenantID != "" {
    labelFilter = tenantdb.TenantLabel(req.TenantID, "Entity")
}
records, err := rs.graphRAG.FullTextSearch(ctx, req.Question, 20)
// (当前 FullTextSearch 无 label 参数，此处记录 TODO，不破坏现有签名)
```

在 `rag_service.go` 加 import：

```go
"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
```

- [ ] **Step 4: 改造 `rag_handler.go`**

`UploadDocument` 和 `Query` 方法加 tenantID 提取：

```go
func (h *RAGHandler) UploadDocument(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req UploadDocumentRequest
    if err := c.ShouldBind(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    // ... 文件读取逻辑不变 ...
    ingestReq := knowledge.IngestDocumentRequest{
        TenantID:     tenantID,          // 注入
        Workspace:    req.Workspace,
        DocumentData: fileData,
        FileName:     req.File.Filename,
        DocumentID:   documentID,
    }
    result, err := h.ingestSvc.IngestDocument(c.Request.Context(), ingestReq)
    // ... 响应逻辑不变 ...
}

func (h *RAGHandler) Query(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req QueryRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }
    // ...
    ragReq := knowledge.RAGQueryRequest{
        Question:  req.Question,
        Workspace: req.Workspace,
        TenantID:  tenantID,             // 注入
        Mode:      req.Mode,
        TopK:      req.TopK,
    }
    result, err := h.ragService.Query(c.Request.Context(), ragReq)
    // ... 响应逻辑不变 ...
}
```

- [ ] **Step 5: 运行测试**

```bash
go test ./api/handler/ -run "TestUploadDocument_MissingTenant|TestQuery_MissingTenant" -v
```

预期：`PASS`

- [ ] **Step 6: go vet**

```bash
go vet ./internal/knowledge/... ./api/handler/...
```

- [ ] **Step 7: Commit**

```bash
git add internal/knowledge/rag_service.go api/handler/rag_handler.go api/handler/rag_handler_test.go
git commit -m "feat(rag): inject tenant isolation into RAG query and ingest"
```


---

### Task 7: MCP Config 持久化

**Files:**
- Modify: `internal/mcp/client_manager.go`
- Modify: `internal/mcp/mcp_test.go`
- Modify: `api/handler/mcp_handler.go`
- Modify: `api/handler/mcp_handler_test.go`

持久化目标：`Connect`（注册配置时）写 `mcp_configs` 表；`Disconnect` 删除行；`GetAllServerInfo` 从 DB 读取（当有 pool 时）。handler 层 `ListServers`/`GetServer` 加 tenantID 检查。

- [ ] **Step 1: 写失败 integration 测试**

在 `internal/mcp/mcp_test.go` 顶部加 build tag，末尾追加：

```go
//go:build integration

package mcp_test

import (
    "context"
    "os"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.uber.org/zap"
)

func testPoolMCP(t *testing.T) *pgxpool.Pool {
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

func TestMCPClientManagerPersist(t *testing.T) {
    pool := testPoolMCP(t)
    tenantID := "test_tenant_mcp1"
    ctx := context.Background()
    _, _ = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS tenant_"+tenantID)
    _, _ = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenant_`+tenantID+`.mcp_configs (
        id TEXT PRIMARY KEY, name TEXT NOT NULL, transport TEXT NOT NULL,
        endpoint TEXT, version TEXT, config_json JSONB NOT NULL DEFAULT '{}',
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
    t.Cleanup(func() {
        pool.Exec(context.Background(), "DROP SCHEMA tenant_"+tenantID+" CASCADE")
    })

    logger, _ := zap.NewDevelopment()
    manager := mcp.NewClientManager(logger, nil, pool)

    // PersistConfig: persist without actual TCP connect
    cfg := &mcp.MCPServerConfig{ID: "srv-1", Name: "TestServer", Transport: "http", Endpoint: "http://localhost:9000"}
    if err := manager.PersistConfig(ctx, tenantID, cfg); err != nil {
        t.Fatalf("PersistConfig: %v", err)
    }

    cfgs, err := manager.ListConfigs(ctx, tenantID)
    if err != nil || len(cfgs) != 1 {
        t.Fatalf("ListConfigs: err=%v, len=%d", err, len(cfgs))
    }

    if err := manager.DeleteConfig(ctx, tenantID, "srv-1"); err != nil {
        t.Fatalf("DeleteConfig: %v", err)
    }
    cfgs2, _ := manager.ListConfigs(ctx, tenantID)
    if len(cfgs2) != 0 {
        t.Fatalf("expected 0 after delete, got %d", len(cfgs2))
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -tags=integration ./internal/mcp/ -run TestMCPClientManagerPersist -v 2>&1 | head -15
```

预期：`FAIL` — `NewClientManager` 不接受 pool，`PersistConfig`/`ListConfigs`/`DeleteConfig` 不存在。

- [ ] **Step 3: 改造 `client_manager.go`**

1. 给 `ClientManager` struct 加字段：
```go
pool *pgxpool.Pool
```

2. `NewClientManager` 函数签名末尾加 `pool *pgxpool.Pool`，并赋值。

3. 新增三个方法：

```go
// PersistConfig 将 MCP server 配置持久化到 tenant schema。
func (m *ClientManager) PersistConfig(ctx context.Context, tenantID string, cfg *MCPServerConfig) error {
    if m.pool == nil || tenantID == "" {
        return nil
    }
    return tenantdb.ExecTenant(ctx, m.pool, tenantID, func(tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `INSERT INTO mcp_configs (id, name, transport, endpoint, version, config_json)
             VALUES ($1,$2,$3,$4,$5,$6::jsonb)
             ON CONFLICT (id) DO UPDATE
             SET name=EXCLUDED.name, transport=EXCLUDED.transport,
                 endpoint=EXCLUDED.endpoint, version=EXCLUDED.version`,
            cfg.ID, cfg.Name, cfg.Transport, cfg.Endpoint, cfg.Version, "{}",
        )
        return err
    })
}

// ListConfigs 从 tenant schema 读取所有 MCP server 配置。
func (m *ClientManager) ListConfigs(ctx context.Context, tenantID string) ([]*MCPServerConfig, error) {
    if m.pool == nil || tenantID == "" {
        return nil, nil
    }
    var cfgs []*MCPServerConfig
    err := tenantdb.ExecTenant(ctx, m.pool, tenantID, func(tx pgx.Tx) error {
        rows, err := tx.Query(ctx,
            `SELECT id, name, transport, endpoint, version FROM mcp_configs ORDER BY created_at`)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            c := &MCPServerConfig{}
            if err := rows.Scan(&c.ID, &c.Name, &c.Transport, &c.Endpoint, &c.Version); err != nil {
                continue
            }
            cfgs = append(cfgs, c)
        }
        return rows.Err()
    })
    return cfgs, err
}

// DeleteConfig 从 tenant schema 删除 MCP server 配置。
func (m *ClientManager) DeleteConfig(ctx context.Context, tenantID, serverID string) error {
    if m.pool == nil || tenantID == "" {
        return nil
    }
    return tenantdb.ExecTenant(ctx, m.pool, tenantID, func(tx pgx.Tx) error {
        tag, err := tx.Exec(ctx, `DELETE FROM mcp_configs WHERE id = $1`, serverID)
        if err != nil {
            return err
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("mcp config %s not found", serverID)
        }
        return nil
    })
}
```

4. 在 `Connect` 末尾调用 `PersistConfig`（tenantID 从调用方传入，`Connect` 签名暂不改——在 handler 层单独调用 `PersistConfig`）。

5. 在 `client_manager.go` 加 import：

```go
import (
    // 现有 import ...
    "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 4: 改造 `mcp_handler.go`**

`ListServers`、`GetServer`、`ExecuteTool` 加 tenantID 检查：

```go
func (h *MCPHandler) ListServers(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    // 优先从 DB 读配置列表
    cfgs, err := h.manager.ListConfigs(c.Request.Context(), tenantID)
    if err != nil {
        h.logger.Warn("failed to list configs from DB, falling back", zap.Error(err))
    }
    if cfgs != nil {
        c.JSON(http.StatusOK, gin.H{"servers": cfgs, "count": len(cfgs)})
        return
    }
    // Fallback: in-memory
    servers := h.manager.GetAllServerInfo()
    c.JSON(http.StatusOK, gin.H{"servers": servers, "count": len(servers)})
}

func (h *MCPHandler) GetServer(c *gin.Context) {
    _, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    // 以下逻辑不变
    serverID := c.Param("id")
    server := h.manager.GetServerInfo(serverID)
    if server == nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "server not found"})
        return
    }
    c.JSON(http.StatusOK, server)
}
```

`ExecuteTool` 同样加 tenantID 检查（只校验，不需要传递给 skill 执行层）：

```go
func (h *MCPHandler) ExecuteTool(c *gin.Context) {
    _, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    // 以下逻辑不变
    // ...
}
```

- [ ] **Step 5: 写 handler 单元测试并运行**

在 `api/handler/mcp_handler_test.go` 末尾追加：

```go
func TestListServers_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    manager := mcp.NewClientManager(logger, nil, nil)
    registry := mcp.NewMCPSkillRegistry(manager, logger)
    h := NewMCPHandler(registry, manager, logger)
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = httptest.NewRequest("GET", "/api/v1/mcp/servers", nil)
    h.ListServers(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}
```

```bash
go test ./api/handler/ -run TestListServers_MissingTenant -v
```

预期：`PASS`

- [ ] **Step 6: 运行 integration 测试**

```bash
export TEST_DATABASE_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
go test -tags=integration ./internal/mcp/ -run TestMCPClientManagerPersist -v
```

预期：`PASS`

- [ ] **Step 7: go vet**

```bash
go vet ./internal/mcp/... ./api/handler/...
```

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/client_manager.go internal/mcp/mcp_test.go \
        api/handler/mcp_handler.go api/handler/mcp_handler_test.go
git commit -m "feat(mcp): persist configs to PostgreSQL with tenant isolation"
```


---

### Task 8: Skill Registry 持久化 + Handler 改造

**Files:**
- Modify: `internal/orchestrator/registry.go`
- Modify: `internal/orchestrator/registry_test.go`
- Modify: `api/handler/skill_handler.go`
- Modify: `api/handler/skill_handler_test.go`

持久化目标：`orchestrator.Registry` 改为 DB-backed；`Register`/`Get`/`GetAll`/`Remove` 读写 `skills` 表；handler 加 tenantID 检查。

- [ ] **Step 1: 写失败 integration 测试**

替换 `internal/orchestrator/registry_test.go`（加 build tag）：

```go
//go:build integration

package orchestrator_test

import (
    "context"
    "os"
    "testing"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
    "github.com/jackc/pgx/v5/pgxpool"
)

func testPoolOrch(t *testing.T) *pgxpool.Pool {
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

func TestSkillRegistryCRUD(t *testing.T) {
    pool := testPoolOrch(t)
    tenantID := "test_tenant_sk1"
    ctx := context.Background()
    _, _ = pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS tenant_"+tenantID)
    _, _ = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS tenant_`+tenantID+`.skills (
        id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT,
        type TEXT NOT NULL, code TEXT, language TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
    t.Cleanup(func() {
        pool.Exec(context.Background(), "DROP SCHEMA tenant_"+tenantID+" CASCADE")
    })

    reg := orchestrator.NewRegistry(pool)

    if err := reg.Register(ctx, tenantID, "sk-1", &orchestrator.SkillRecord{
        Name: "TestSkill", Description: "desc", Type: "code",
        Code: "print('hi')", Language: "python",
    }); err != nil {
        t.Fatalf("Register: %v", err)
    }

    s, ok := reg.Get(ctx, tenantID, "sk-1")
    if !ok || s.GetName() != "TestSkill" {
        t.Fatalf("Get failed: ok=%v", ok)
    }

    all := reg.GetAll(ctx, tenantID)
    if len(all) != 1 {
        t.Fatalf("GetAll want 1, got %d", len(all))
    }

    if err := reg.Remove(ctx, tenantID, "sk-1"); err != nil {
        t.Fatalf("Remove: %v", err)
    }
    if _, ok := reg.Get(ctx, tenantID, "sk-1"); ok {
        t.Fatal("skill should be deleted")
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -tags=integration ./internal/orchestrator/ -run TestSkillRegistryCRUD -v 2>&1 | head -15
```

预期：`FAIL`

- [ ] **Step 3: 改造 `internal/orchestrator/registry.go`**

新增辅助类型 `SkillRecord`（在同文件定义，存储 DB 序列化字段）和 DB-backed `Registry`：

```go
package orchestrator

import (
    "context"
    "fmt"

    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
    "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

// SkillRecord 是 DB 序列化中间类型。
type SkillRecord struct {
    ID          string
    Name        string
    Description string
    Type        string
    Code        string
    Language    string
}

func (r *SkillRecord) GetID() string          { return r.ID }
func (r *SkillRecord) GetName() string        { return r.Name }
func (r *SkillRecord) GetDescription() string { return r.Description }
func (r *SkillRecord) GetType() string        { return r.Type }

// Registry 通过 PostgreSQL 持久化 Skills，按 tenant schema 隔离。
type Registry struct {
    pool *pgxpool.Pool
}

// NewRegistry 创建 Registry，pool 不可为 nil。
func NewRegistry(pool *pgxpool.Pool) *Registry {
    return &Registry{pool: pool}
}

// Register 将 skill 写入 tenant schema skills 表。
func (r *Registry) Register(ctx context.Context, tenantID, id string, s skill.Skill) error {
    rec, _ := s.(*SkillRecord)
    name, desc, typ, code, lang := s.GetName(), s.GetDescription(), s.GetType(), "", ""
    if rec != nil {
        code, lang = rec.Code, rec.Language
    }
    if cs, ok := s.(interface{ GetCode() string }); ok {
        code = cs.GetCode()
    }
    return tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `INSERT INTO skills (id, name, description, type, code, language)
             VALUES ($1,$2,$3,$4,$5,$6)`,
            id, name, desc, typ, code, lang,
        )
        return err
    })
}

// Get 通过 ID 查询 skill。
func (r *Registry) Get(ctx context.Context, tenantID, id string) (skill.Skill, bool) {
    var rec SkillRecord
    err := tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        return tx.QueryRow(ctx,
            `SELECT id, name, description, type, code, language FROM skills WHERE id = $1`, id).
            Scan(&rec.ID, &rec.Name, &rec.Description, &rec.Type, &rec.Code, &rec.Language)
    })
    if err != nil {
        return nil, false
    }
    return &rec, true
}

// GetAll 返回 tenant 下所有 skill。
func (r *Registry) GetAll(ctx context.Context, tenantID string) []skill.Skill {
    var skills []skill.Skill
    _ = tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        rows, err := tx.Query(ctx,
            `SELECT id, name, description, type, code, language FROM skills ORDER BY created_at`)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var rec SkillRecord
            if err := rows.Scan(&rec.ID, &rec.Name, &rec.Description, &rec.Type, &rec.Code, &rec.Language); err != nil {
                continue
            }
            skills = append(skills, &rec)
        }
        return rows.Err()
    })
    return skills
}

// Remove 从 tenant schema 删除 skill。
func (r *Registry) Remove(ctx context.Context, tenantID, id string) error {
    return tenantdb.ExecTenant(ctx, r.pool, tenantID, func(tx pgx.Tx) error {
        tag, err := tx.Exec(ctx, `DELETE FROM skills WHERE id = $1`, id)
        if err != nil {
            return err
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("skill not found: %s", id)
        }
        return nil
    })
}
```

注意：`SkillRecord` 实现了 `skill.Skill` 接口（`GetID`/`GetName`/`GetDescription`/`GetType`）；现有 `skill.CodeSkill`/`skill.LLMSkill` 持续可以通过接口传入 `Register`，DB 只保存元数据。

- [ ] **Step 4: 改造 `skill_handler.go`**

`CreateSkill`、`GetSkill`、`GetAllSkills`、`UpdateSkill`、`DeleteSkill` 加 tenantID 检查，方法签名改为传 ctx + tenantID：

```go
func (h *SkillHandler) CreateSkill(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    var req model.CreateSkillRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
        return
    }
    id := uuid.New().String()
    var s skill.Skill
    switch req.Type {
    case "code":
        s = skill.NewCodeSkill(id, req.Name, req.Description, req.Code, req.Language)
    case "llm":
        s = skill.NewLLMSkill(id, req.Name, req.Description, h.gateway, h.logger)
    default:
        s = &skill.BaseSkill{ID: id, Name: req.Name, Description: req.Description, Type: req.Type}
    }
    if err := h.registry.Register(c.Request.Context(), tenantID, id, s); err != nil {
        c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: err.Error()})
        return
    }
    c.JSON(http.StatusCreated, model.SkillResponse{
        ID: id, Name: req.Name, Description: req.Description,
        Type: req.Type, CreatedAt: time.Now().Format(time.RFC3339),
    })
}

func (h *SkillHandler) GetAllSkills(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    skills := h.registry.GetAll(c.Request.Context(), tenantID)
    responses := make([]model.SkillResponse, 0, len(skills))
    for _, s := range skills {
        responses = append(responses, model.SkillResponse{
            ID: s.GetID(), Name: s.GetName(),
            Description: s.GetDescription(), Type: s.GetType(),
            CreatedAt: time.Now().Format(time.RFC3339),
        })
    }
    c.JSON(http.StatusOK, gin.H{"skills": responses})
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    s, ok2 := h.registry.Get(c.Request.Context(), tenantID, id)
    if !ok2 {
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "skill not found"})
        return
    }
    c.JSON(http.StatusOK, model.SkillResponse{
        ID: s.GetID(), Name: s.GetName(),
        Description: s.GetDescription(), Type: s.GetType(),
        CreatedAt: time.Now().Format(time.RFC3339),
    })
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    if err := h.registry.Remove(c.Request.Context(), tenantID, id); err != nil {
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "skill not found"})
        return
    }
    c.JSON(http.StatusOK, gin.H{"message": "skill deleted successfully"})
}
```

`UpdateSkill`：加 tenantID 检查，然后 `Get`→修改字段→`Register`（upsert）模式：

```go
func (h *SkillHandler) UpdateSkill(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    id := c.Param("id")
    _, ok2 := h.registry.Get(c.Request.Context(), tenantID, id)
    if !ok2 {
        c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "skill not found"})
        return
    }
    var req model.CreateSkillRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
        return
    }
    var s skill.Skill
    switch req.Type {
    case "code":
        s = skill.NewCodeSkill(id, req.Name, req.Description, req.Code, req.Language)
    case "llm":
        s = skill.NewLLMSkill(id, req.Name, req.Description, h.gateway, h.logger)
    default:
        s = &skill.BaseSkill{ID: id, Name: req.Name, Description: req.Description, Type: req.Type}
    }
    // Register with ON CONFLICT DO UPDATE (upsert) — see registry.go
    if err := h.registry.Register(c.Request.Context(), tenantID, id, s); err != nil {
        c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: err.Error()})
        return
    }
    c.JSON(http.StatusOK, model.SkillResponse{
        ID: id, Name: req.Name, Description: req.Description,
        Type: req.Type, CreatedAt: time.Now().Format(time.RFC3339),
    })
}
```

同步更新 `internal/orchestrator/registry.go` 中 `Register` 的 INSERT 改为 UPSERT：

```sql
INSERT INTO skills (id, name, description, type, code, language)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (id) DO UPDATE
SET name=EXCLUDED.name, description=EXCLUDED.description,
    type=EXCLUDED.type, code=EXCLUDED.code, language=EXCLUDED.language
```

- [ ] **Step 5: 写 handler 失败测试并运行**

在 `api/handler/skill_handler_test.go` 末尾追加：

```go
func TestCreateSkill_MissingTenant(t *testing.T) {
    logger, _ := zap.NewDevelopment()
    h := &SkillHandler{registry: nil, logger: logger}
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    body := `{"name":"s","type":"code","description":"d"}`
    c.Request = httptest.NewRequest("POST", "/skills", strings.NewReader(body))
    c.Request.Header.Set("Content-Type", "application/json")
    h.CreateSkill(c)
    if w.Code != 401 {
        t.Fatalf("want 401, got %d", w.Code)
    }
}
```

```bash
go test ./api/handler/ -run TestCreateSkill_MissingTenant -v
```

预期：`PASS`

- [ ] **Step 6: 运行 integration 测试**

```bash
export TEST_DATABASE_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
go test -tags=integration ./internal/orchestrator/ -run TestSkillRegistryCRUD -v
```

预期：`PASS`

- [ ] **Step 7: go vet**

```bash
go vet ./internal/orchestrator/... ./api/handler/...
```

- [ ] **Step 8: Commit**

```bash
git add internal/orchestrator/registry.go internal/orchestrator/registry_test.go \
        api/handler/skill_handler.go api/handler/skill_handler_test.go
git commit -m "feat(skill): persist registry to PostgreSQL with tenant isolation"
```


---

### Task 9: Router 接入 TenantMiddleware + 注入 Pool

**Files:**
- Modify: `api/router.go`

改造目标：`SetupRouter` 接受 `*pgxpool.Pool`；将 pool 注入各 Registry/Manager 构造函数；五个资源路由组（`/skills`、`/agents`、`/knowledge`、`/memory`、`/api/v1/mcp`）加 `TenantMiddleware`。

- [ ] **Step 1: 写失败测试**

在 `api/handler/handler_test.go` 末尾追加：

```go
func TestRouterRequiresTenantMiddleware(t *testing.T) {
    // 确认 /agents 路由在无 Authorization 头时返回 401
    logger, _ := zap.NewDevelopment()
    cfg := &config.Config{}
    // 传入 nil pool：SetupRouter 应接受 *pgxpool.Pool 参数
    router := api.SetupRouter(cfg, logger, nil, nil, nil)
    w := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/agents", nil)
    router.ServeHTTP(w, req)
    if w.Code != 401 {
        t.Fatalf("want 401 from tenant middleware, got %d", w.Code)
    }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test ./api/... -run TestRouterRequiresTenantMiddleware -v 2>&1 | head -15
```

预期：`FAIL` — `SetupRouter` 签名不接受 pool，router 无 tenant middleware。

- [ ] **Step 3: 改造 `api/router.go`**

修改 `SetupRouter` 函数签名，加 `pool *pgxpool.Pool` 参数（第三个位置，在 registry 之前），并将 pool 传入各构造函数：

```go
func SetupRouter(
    cfg *config.Config,
    logger *zap.Logger,
    pool *pgxpool.Pool,             // 新增
    registry *orchestrator.Registry, // 改：现在 registry 由外部传入（已含 pool）
    gateway *llmgateway.Gateway,
) *gin.Engine {
```

注意：由于 `orchestrator.Registry` 的构造已改为需要 `pool`，router 层不再自己 `NewRegistry()`，而是接收外部构造好的实例（在 `main.go` 中初始化）。同理，`agent.Registry`、`mcp.ClientManager`、`memory.MemoryManager`、`knowledge.KnowledgeIngest` 都在外部构造并传入（或在 router 内用 pool 初始化）。

router 内初始化改为：

```go
// Agent registry — pool-backed
agentRegistry := agent.NewRegistry(pool, logger)
agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway, metrics)

// Memory manager — pool-backed
memoryManager := memory.NewMemoryManager(memoryConfig, logger, nil, nil, nil, pool)
memoryHandler := handler.NewMemoryHandler(memoryManager, logger)

// MCP — pool-backed
mcpManager := mcp.NewClientManager(logger, nil, pool)
mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)

// Knowledge — pool-backed
ingestSvc := knowledge.NewKnowledgeIngest(parser, chunker, embedSvc, vectorStore, graphRAG, logger, pool)
ragService := knowledge.NewRAGService(embedSvc, vectorStore, graphRAG, logger)
ragHandler := handler.NewRAGHandler(ingestSvc, ragService, logger)
```

加 import：
```go
"github.com/jackc/pgx/v5/pgxpool"
```

在五个路由组加 `TenantMiddleware`（由 Plan 3 提供，`api/middleware/tenant.go`）：

```go
// Skill endpoints
skills := router.Group("/skills")
skills.Use(middleware.TenantMiddleware(logger))  // 新增
{
    skills.GET("", skillHandler.GetAllSkills)
    // ...
}

// Agent endpoints
agents := router.Group("/agents")
agents.Use(middleware.TenantMiddleware(logger))
{
    agents.GET("", agentHandler.GetAllAgents)
    // ...
}

// Knowledge endpoints
knowledge := router.Group("/knowledge")
knowledge.Use(middleware.TenantMiddleware(logger))
{
    knowledge.POST("/ingest", ragHandler.UploadDocument)
    knowledge.POST("/query", ragHandler.Query)
}

// Memory endpoints
mem := router.Group("/memory")
mem.Use(middleware.TenantMiddleware(logger))
{
    // ... 所有 memory 路由不变 ...
}
```

对于 MCP，将 `mcpHandler.RegisterRoutes` 改为接受路由组，以便加 middleware：

```go
// MCP 路由：手动注册以加 middleware
v1mcp := router.Group("/api/v1/mcp")
v1mcp.Use(middleware.TenantMiddleware(logger))
{
    v1mcp.GET("/servers", mcpHandler.ListServers)
    v1mcp.GET("/servers/:id", mcpHandler.GetServer)
    v1mcp.GET("/servers/:id/tools", mcpHandler.ListTools)
    v1mcp.GET("/servers/:id/resources", mcpHandler.ListResources)
    v1mcp.POST("/tools/:toolId/execute", mcpHandler.ExecuteTool)
    v1mcp.GET("/skills", mcpHandler.ListSkills)
    v1mcp.GET("/skills/:id", mcpHandler.GetSkill)
    v1mcp.POST("/skills/refresh", mcpHandler.RefreshSkills)
    v1mcp.GET("/status", mcpHandler.GetServerStatus)
}
// 不再调用 mcpHandler.RegisterRoutes(router)
```

- [ ] **Step 4: 同步更新 `main.go`**

在 `main.go` 中初始化 pool 并传入 `SetupRouter`（注意：`main.go` 路径为 `cmd/server/main.go` 或项目根目录，按实际位置修改）：

```go
// 初始化 pgxpool
pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
if err != nil {
    logger.Fatal("failed to connect to postgres", zap.Error(err))
}
defer pool.Close()

// skill registry 现在由 main 构造
skillRegistry := orchestrator.NewRegistry(pool)

router := api.SetupRouter(cfg, logger, pool, skillRegistry, gateway)
```

- [ ] **Step 5: 运行测试**

```bash
go test ./api/... -run TestRouterRequiresTenantMiddleware -v
```

预期：`PASS`

- [ ] **Step 6: go vet + go build**

```bash
go vet ./...
go build ./...
```

预期：无错误。

- [ ] **Step 7: 全量 short test**

```bash
go test -short ./... 2>&1 | tail -20
```

预期：所有包 `ok` 或 `no test files`（integration 测试因无 build tag 被跳过）。

- [ ] **Step 8: Commit**

```bash
git add api/router.go
git commit -m "feat(router): inject pool, add TenantMiddleware to all resource route groups"
```

---

## 完成标准

全部 9 个 Task 的 commit 均已推送后，执行以下验证：

```bash
# 1. 短测试全绿
go test -short ./...

# 2. 集成测试（需 TEST_DATABASE_URL）
export TEST_DATABASE_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
go test -tags=integration ./internal/agent/ ./internal/memory/ ./internal/knowledge/ \
        ./internal/mcp/ ./internal/orchestrator/ -v

# 3. 构建无错误
go build ./...

# 4. 手工验证：POST /agents 不带 JWT → 401
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/agents
# 期望：401

# 5. 手工验证：POST /agents 带合法 JWT → 201
curl -s -X POST http://localhost:8080/agents \
  -H "Authorization: Bearer <valid_jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name":"test","type":"react","llmModel":"gpt-4o","maxIterations":5}' \
  -w "\nHTTP %{http_code}\n"
# 期望：201
```
