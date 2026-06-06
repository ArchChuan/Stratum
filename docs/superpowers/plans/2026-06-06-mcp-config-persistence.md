# MCP配置持久化与Agents表DDL修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 tenant_schema.sql 中 agents/mcp_configs 两张表的 DDL 与运行时代码的结构不一致，并让 MCP 服务器配置在重启后从数据库自动恢复连接。

**Architecture:** schema-per-tenant 架构下，所有表不含 tenant_id 列；通过 `ExecTenant`/`SET LOCAL search_path` 在事务层做隔离。修复分三步：先改 DDL（tenant_schema.sql），再改 ClientManager 的持久化和恢复逻辑，最后在 router 启动时调用恢复。

**Tech Stack:** Go 1.22+, pgx v5, Gin, Zap, PostgreSQL schema-per-tenant

---

## 文件变更总览

| 文件 | 操作 | 说明 |
|------|------|------|
| `pkg/tenantdb/tenant_schema.sql` | 修改 | 替换 agents/mcp_configs 两张表 DDL |
| `internal/mcp/client_manager.go` | 修改 | persistConnect 写全量字段；新增 RestoreFromDB |
| `api/router.go` | 修改 | mcpManager 初始化后调用 RestoreFromDB |

---

### Task 1: 修复 tenant_schema.sql 中的 agents 表 DDL

**Files:**
- Modify: `pkg/tenantdb/tenant_schema.sql`

**背景：** 现有 `agents` 表只有 `id UUID, name TEXT, description TEXT, config JSONB`，而 `internal/agent/registry.go` 的 INSERT/SELECT 操作 `id TEXT, name, type, description, persona, system_prompt, llm_model, max_iterations` 独立列，导致运行时 SQL 失败。

- [ ] **Step 1: 替换 agents 表 DDL**

将 `pkg/tenantdb/tenant_schema.sql` 中的 agents 表定义从：

```sql
CREATE TABLE IF NOT EXISTS agents (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    description  TEXT,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

替换为：

```sql
CREATE TABLE IF NOT EXISTS agents (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    type           TEXT NOT NULL DEFAULT 'react',
    description    TEXT NOT NULL DEFAULT '',
    persona        TEXT NOT NULL DEFAULT '',
    system_prompt  TEXT NOT NULL DEFAULT '',
    llm_model      TEXT NOT NULL DEFAULT '',
    max_iterations INT  NOT NULL DEFAULT 10,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: 运行现有测试确认无编译错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go vet ./internal/agent/... && go test -short ./internal/agent/...
```

预期：`ok` 或仅跳过集成测试，无编译错误。

- [ ] **Step 3: 提交**

```bash
git add pkg/tenantdb/tenant_schema.sql
git commit -m "fix(schema): replace agents JSONB config with explicit columns matching registry SQL"
```

---

### Task 2: 修复 tenant_schema.sql 中的 mcp_configs 表 DDL

**Files:**
- Modify: `pkg/tenantdb/tenant_schema.sql`

**背景：** 现有 `mcp_configs` 表有 `agent_id` 外键且只有 `server_id/transport/config JSONB` 几列，`persistConnect` 只写5个字段丢失 `args/env/capabilities/timeout`，重启后无法重建连接。

- [ ] **Step 1: 替换 mcp_configs 表 DDL**

将 `pkg/tenantdb/tenant_schema.sql` 中的 mcp_configs 表定义从：

```sql
CREATE TABLE IF NOT EXISTS mcp_configs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    server_id    TEXT NOT NULL,
    transport    TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

替换为：

```sql
CREATE TABLE IF NOT EXISTS mcp_configs (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL DEFAULT '',
    transport     TEXT NOT NULL,
    command       TEXT NOT NULL DEFAULT '',
    url           TEXT NOT NULL DEFAULT '',
    args          JSONB NOT NULL DEFAULT '[]',
    env           JSONB NOT NULL DEFAULT '{}',
    capabilities  JSONB NOT NULL DEFAULT '[]',
    timeout_sec   INT  NOT NULL DEFAULT 30,
    enabled       BOOL NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: 运行现有测试确认无编译错误**

```bash
go vet ./internal/mcp/... && go test -short ./internal/mcp/...
```

预期：`ok` 或跳过集成测试，无编译错误。

- [ ] **Step 3: 提交**

```bash
git add pkg/tenantdb/tenant_schema.sql
git commit -m "fix(schema): replace mcp_configs with full-field columns for connection restore"
```

---

### Task 3: 修复 ClientManager.persistConnect 写全量字段

**Files:**
- Modify: `internal/mcp/client_manager.go`

**背景：** 现有 `persistConnect` 只写 `id/name/transport/command/url/enabled`，`args/env/capabilities/timeout` 全部丢失。

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/mcp_test.go` 末尾追加：

```go
func TestPersistConnectFullFields(t *testing.T) {
    // 验证 persistConnect 使用正确的列数（通过检查 SQL 参数数量）
    // 这里用 nil pool 确认函数不 panic，实际列验证在集成测试
    logger := zap.NewNop()
    m := NewClientManager(logger, nil, nil)
    cfg := &MCPServerConfig{
        ID:           "test-id",
        Name:         "Test Server",
        Transport:    "stdio",
        Command:      "node",
        Args:         []string{"--arg1", "val"},
        Env:          map[string]string{"KEY": "VAL"},
        Capabilities: []string{"tools"},
        Timeout:      30 * time.Second,
    }
    // pool is nil → persistConnect returns early without panic
    ctx := context.Background()
    m.persistConnect(ctx, cfg) // must not panic
}
```

- [ ] **Step 2: 运行测试确认可以编译且通过（pool=nil 早返回）**

```bash
go test -short -run TestPersistConnectFullFields ./internal/mcp/...
```

预期：PASS（pool=nil 时 persistConnect 直接 return）

- [ ] **Step 3: 替换 persistConnect 实现**

将 `internal/mcp/client_manager.go` 中的 `persistConnect` 方法替换为：

```go
func (m *ClientManager) persistConnect(ctx context.Context, cfg *MCPServerConfig) {
	if m.pool == nil {
		return
	}
	argsJSON, _ := json.Marshal(cfg.Args)
	envJSON, _ := json.Marshal(cfg.Env)
	capsJSON, _ := json.Marshal(cfg.Capabilities)
	timeoutSec := int(cfg.Timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	_ = tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO mcp_configs (id, name, transport, command, url, args, env, capabilities, timeout_sec, enabled, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, NOW())
			ON CONFLICT (id) DO UPDATE SET
				name=$2, transport=$3, command=$4, url=$5,
				args=$6, env=$7, capabilities=$8, timeout_sec=$9,
				enabled=true, updated_at=NOW()`,
			cfg.ID, cfg.Name, cfg.Transport, cfg.Command, cfg.URL,
			argsJSON, envJSON, capsJSON, timeoutSec)
		return err
	})
}
```

在文件顶部 import 中加入 `"encoding/json"`（如尚未导入）。

- [ ] **Step 4: 运行测试**

```bash
go vet ./internal/mcp/... && go test -short ./internal/mcp/...
```

预期：PASS

- [ ] **Step 5: 提交**

```bash
git add internal/mcp/client_manager.go internal/mcp/mcp_test.go
git commit -m "fix(mcp): persist full MCPServerConfig fields on connect"
```

---

### Task 4: 新增 ClientManager.RestoreFromDB

**Files:**
- Modify: `internal/mcp/client_manager.go`
- Modify: `internal/mcp/mcp_test.go`

**背景：** 应用重启后内存中的 client map 为空，需从 DB 读出 `enabled=true` 的配置逐一重连。连接失败只记 warn，不阻塞启动。

- [ ] **Step 1: 写失败测试**

在 `internal/mcp/mcp_test.go` 追加：

```go
func TestRestoreFromDB_NilPool(t *testing.T) {
    logger := zap.NewNop()
    m := NewClientManager(logger, nil, nil)
    // pool=nil → RestoreFromDB should return nil immediately
    err := m.RestoreFromDB(context.Background())
    if err != nil {
        t.Errorf("expected nil error with nil pool, got %v", err)
    }
}
```

- [ ] **Step 2: 运行测试确认编译失败（RestoreFromDB 未定义）**

```bash
go test -short -run TestRestoreFromDB_NilPool ./internal/mcp/...
```

预期：编译错误 `m.RestoreFromDB undefined`

- [ ] **Step 3: 实现 RestoreFromDB**

在 `internal/mcp/client_manager.go` 的 `Stop` 方法之前追加：

```go
// RestoreFromDB 从数据库读取 enabled=true 的 MCP 配置并重建连接。
// 连接失败只记录 warn，不返回错误，确保启动不被单个不可用服务器阻塞。
func (m *ClientManager) RestoreFromDB(ctx context.Context) error {
	if m.pool == nil {
		return nil
	}

	type row struct {
		id          string
		name        string
		transport   string
		command     string
		url         string
		args        []byte
		env         []byte
		caps        []byte
		timeoutSec  int
	}

	var rows []row
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		pgRows, err := tx.Query(ctx, `
			SELECT id, name, transport, command, url, args, env, capabilities, timeout_sec
			FROM mcp_configs WHERE enabled = true`)
		if err != nil {
			return fmt.Errorf("restore mcp_configs query: %w", err)
		}
		defer pgRows.Close()
		for pgRows.Next() {
			var r row
			if err := pgRows.Scan(&r.id, &r.name, &r.transport, &r.command, &r.url,
				&r.args, &r.env, &r.caps, &r.timeoutSec); err != nil {
				return fmt.Errorf("restore mcp_configs scan: %w", err)
			}
			rows = append(rows, r)
		}
		return pgRows.Err()
	})
	if err != nil {
		return fmt.Errorf("RestoreFromDB: %w", err)
	}

	for _, r := range rows {
		var args []string
		var env map[string]string
		var caps []string
		_ = json.Unmarshal(r.args, &args)
		_ = json.Unmarshal(r.env, &env)
		_ = json.Unmarshal(r.caps, &caps)

		cfg := &MCPServerConfig{
			ID:           r.id,
			Name:         r.name,
			Transport:    r.transport,
			Command:      r.command,
			URL:          r.url,
			Args:         args,
			Env:          env,
			Capabilities: caps,
			Timeout:      time.Duration(r.timeoutSec) * time.Second,
		}

		if err := m.Connect(ctx, cfg); err != nil {
			m.logger.Warn("RestoreFromDB: failed to reconnect MCP server",
				zap.String("server_id", cfg.ID),
				zap.Error(err))
		} else {
			m.logger.Info("RestoreFromDB: reconnected MCP server",
				zap.String("server_id", cfg.ID))
		}
	}

	return nil
}
```

- [ ] **Step 4: 运行测试**

```bash
go vet ./internal/mcp/... && go test -short ./internal/mcp/...
```

预期：PASS

- [ ] **Step 5: 提交**

```bash
git add internal/mcp/client_manager.go internal/mcp/mcp_test.go
git commit -m "feat(mcp): add RestoreFromDB to reconnect enabled servers on startup"
```

---

### Task 5: router.go 启动时调用 RestoreFromDB

**Files:**
- Modify: `api/router.go`

**背景：** `mcpManager` 初始化后需调用 `RestoreFromDB`，让已持久化的 MCP 服务器在应用重启后自动上线。连接超时设 10s。

- [ ] **Step 1: 修改 router.go**

在 `api/router.go` 找到以下代码段：

```go
	// Initialize MCP system
	mcpManager := mcp.NewClientManager(logger, nil, db)
	mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
	mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)
```

替换为：

```go
	// Initialize MCP system
	mcpManager := mcp.NewClientManager(logger, nil, db)
	mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
	mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)

	// Restore persisted MCP connections from DB
	if db != nil {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer restoreCancel()
		if err := mcpManager.RestoreFromDB(restoreCtx); err != nil {
			logger.Warn("failed to restore MCP connections from DB", zap.Error(err))
		}
	}
```

注意：`context` 和 `time` 已在文件顶部导入，无需额外 import。

- [ ] **Step 2: 编译确认**

```bash
go build ./...
```

预期：无编译错误。

- [ ] **Step 3: 运行全量 short 测试**

```bash
go vet ./... && go test -short ./...
```

预期：所有测试 PASS。

- [ ] **Step 4: 提交**

```bash
git add api/router.go
git commit -m "feat(router): restore MCP server connections from DB on startup"
```

---

## 验证清单

完成所有任务后检查：

- [ ] `go build ./...` 无错误
- [ ] `go test -short ./...` 全部通过
- [ ] `pkg/tenantdb/tenant_schema.sql` 中 `agents` 表列与 `internal/agent/registry.go` SQL 完全匹配
- [ ] `pkg/tenantdb/tenant_schema.sql` 中 `mcp_configs` 表含 `args/env/capabilities/timeout_sec/enabled` 列
- [ ] `ClientManager.persistConnect` 写入 10 个字段（含 JSONB）
- [ ] `ClientManager.RestoreFromDB` 存在且 nil pool 时返回 nil
- [ ] `api/router.go` 在 `db != nil` 时调用 `RestoreFromDB`
