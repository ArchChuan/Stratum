# MCP 集成指南

本文档说明如何在 ClawHermes AI Go 项目中使用 MCP (Model Context Protocol) 集成。

## 概述

MCP 集成允许 ClawHermes 连接到多个 MCP 服务器，并将 MCP 工具转换为 Skill 对象，从而无缝集成到现有的 Skill 系统中。

## 架构

```
MCP Servers (stdio/SSE/HTTP)
         ↓
    ClientManager (连接管理)
         ↓
    CapabilityCache (缓存)
         ↓
    MCPSkillAdapter (工具→Skill转换)
         ↓
    MCPSkillRegistry (技能注册表)
         ↓
    MCPHandler (REST API)
         ↓
    Agent System (技能使用)
```

## 配置

### 1. MCP 服务器配置 (`config/mcp.yaml`)

```yaml
mcp:
  servers:
    - id: "github-mcp"
      name: "GitHub MCP Server"
      version: "1.0.0"
      transport: "stdio"
      command: "node"
      args:
        - "/opt/mcp-servers/github/dist/index.js"
      env:
        GITHUB_TOKEN: "${GITHUB_TOKEN}"
      capabilities:
        - tools
        - resources
      timeout: 30s

    - id: "web-mcp"
      name: "Web MCP Server"
      version: "1.0.0"
      transport: "sse"
      url: "http://localhost:3001/mcp"
      capabilities:
        - tools
        - resources
      timeout: 30s

  connection_pool:
    max_connections: 10
    idle_timeout: 300s
    max_retries: 3
    retry_backoff: 1s

  cache:
    enabled: true
    ttl: 3600s
    max_size: 1000

  monitoring:
    enabled: true
    metrics_interval: 30s
    health_check_interval: 30s
```

### 2. 支持的传输方式

#### stdio
- 用于本地命令行工具
- 通过标准输入/输出通信
- 配置示例：
  ```yaml
  transport: "stdio"
  command: "node"
  args: ["/path/to/server.js"]
  ```

#### SSE (Server-Sent Events)
- 用于 HTTP 服务器
- 基于事件流的单向通信
- 配置示例：
  ```yaml
  transport: "sse"
  url: "http://localhost:3001/mcp"
  ```

#### HTTP
- 用于 REST API 服务器
- 基于 HTTP POST 的请求-响应通信
- 配置示例：
  ```yaml
  transport: "http"
  url: "http://localhost:3000"
  ```

## REST API 端点

### 服务器管理

- `GET /api/v1/mcp/servers` - 列出所有 MCP 服务器
- `GET /api/v1/mcp/servers/:id` - 获取服务器详情
- `GET /api/v1/mcp/servers/:id/tools` - 列出服务器的工具
- `GET /api/v1/mcp/servers/:id/resources` - 列出服务器的资源

### 工具执行

- `POST /api/v1/mcp/tools/:toolId/execute` - 执行工具

请求体：
```json
{
  "param1": "value1",
  "param2": "value2"
}
```

响应：
```json
{
  "result": {
    "output": "execution result"
  }
}
```

### 技能管理

- `GET /api/v1/mcp/skills` - 列出所有 MCP 技能
- `GET /api/v1/mcp/skills/:id` - 获取技能详情
- `POST /api/v1/mcp/skills/refresh` - 刷新所有技能

### 状态查询

- `GET /api/v1/mcp/status` - 获取 MCP 系统状态

响应：
```json
{
  "total": 3,
  "connected": 2,
  "disconnected": 0,
  "error": 1
}
```

## 代码示例

### 初始化 MCP 系统

```go
import "github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"

// 创建客户端管理器
manager := mcp.NewClientManager(logger, nil)

// 创建技能注册表
registry := mcp.NewMCPSkillRegistry(manager, logger)

// 创建 REST API 处理器
handler := handler.NewMCPHandler(registry, manager, logger)

// 注册路由
handler.RegisterRoutes(router)
```

### 连接 MCP 服务器

```go
// 创建服务器配置
config := &mcp.MCPServerConfig{
    ID:        "github-mcp",
    Name:      "GitHub MCP Server",
    Transport: "stdio",
    Command:   "node",
    Args:      []string{"/opt/mcp-servers/github/dist/index.js"},
    Timeout:   30 * time.Second,
}

// 创建客户端
client := mcp.NewBaseClient(config, logger)

// 连接到服务器
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := client.Connect(ctx); err != nil {
    logger.Error("failed to connect", zap.Error(err))
}

// 列出工具
tools, err := client.ListTools(ctx)
if err != nil {
    logger.Error("failed to list tools", zap.Error(err))
}

// 调用工具
result, err := client.CallTool(ctx, "tool_name", map[string]interface{}{
    "param": "value",
})
```

### 使用 MCP 技能

```go
// 获取技能
skill := registry.GetSkill("mcp:github-mcp:get_user")

// 执行技能
result, err := skill.(mcp.SkillExecutor).Execute(map[string]interface{}{
    "username": "octocat",
})
```

## 企业级功能

### 1. 连接池管理
- 自动连接复用
- 空闲超时管理
- 最大连接数限制
- 自动重试机制

### 2. 能力缓存
- TTL 过期机制
- LRU 驱逐策略
- 可配置的缓存大小
- 线程安全的并发访问

### 3. 健康检查
- 定期服务器健康检查
- 自动故障检测
- 自动重连机制
- 详细的错误日志

### 4. 监控和指标
- 连接状态监控
- 工具执行统计
- 缓存命中率
- 错误率追踪

## 测试

运行 MCP 测试套件：

```bash
# 运行所有 MCP 测试
go test -v ./internal/mcp

# 运行特定测试
go test -v ./internal/mcp -run TestMCPIntegration

# 运行基准测试
go test -bench=. ./internal/mcp
```

## 故障排除

### 连接失败

1. 检查服务器是否运行
2. 验证配置中的 URL/命令是否正确
3. 检查防火墙和网络连接
4. 查看日志获取详细错误信息

### 工具执行失败

1. 验证工具名称是否正确
2. 检查输入参数格式
3. 查看服务器日志
4. 验证服务器权限

### 缓存问题

1. 检查缓存 TTL 设置
2. 验证缓存大小限制
3. 监控缓存命中率
4. 考虑手动刷新技能

## 最佳实践

1. **错误处理**：始终检查连接和执行错误
2. **超时设置**：为长时间运行的操作设置合理的超时
3. **资源清理**：在应用关闭时断开连接
4. **日志记录**：启用详细日志用于调试
5. **监控**：定期检查系统状态和指标
6. **缓存策略**：根据使用模式调整缓存参数

## 性能优化

- 启用缓存以减少网络调用
- 调整连接池大小以匹配工作负载
- 使用 HTTP 传输而不是 stdio 以获得更好的性能
- 定期刷新技能以保持最新状态

## 安全考虑

- 使用环境变量存储敏感信息（如 API 密钥）
- 验证 MCP 服务器的身份
- 限制工具执行权限
- 监控异常活动
- 定期审计日志

## 相关文档

- [MCP 协议规范](https://modelcontextprotocol.io)
- [Skill 系统文档](../docs/agent/agent.md)
- [API 端点文档](../docs/agent/api.md)
