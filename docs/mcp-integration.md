# MCP 集成指南

## 当前架构

MCP 是独立 bounded context：

```text
api/http/handler/mcp_handler.go
  → internal/mcp/application/MCPService
  → internal/mcp/domain/port
  → internal/mcp/infrastructure/{ClientManager,BaseClient,MCPSkillRegistry}
  → tenant schema mcp_servers
```

`api/wiring/mcp.go` 在启动时构造 manager / registry / service，并从数据库恢复租户 MCP 服务器。服务器配置通过 REST API 管理，仓库中没有 `config/mcp.yaml`。

## 传输与配置

`domain.ServerConfig` 支持 stdio 和 HTTP/SSE 形态，主要字段包括：

- `id` / `name` / `version`
- `transport`
- stdio：`command` / `args` / `env`
- HTTP：`url` / `headers`
- `capabilities` / `timeout`
- `auth`：`none` / `bearer` / `api_key` / `oauth2`
- `retry`：重试开关、次数和指数退避参数

不要把真实 token、API key 或 OAuth client secret 写入文档、日志或版本库。

## HTTP API

所有 `/mcp` 路由都需要 JWT 与 tenant context。普通 `member` 可读取配置和执行工具；服务器管理和 skill 刷新需要 `admin` 或更高角色。

| 方法 | 路径 | 用途 | 最低角色 |
|------|------|------|----------|
| GET | `/mcp/servers` | 列出服务器 | member |
| POST | `/mcp/servers` | 保存并连接服务器 | admin |
| GET | `/mcp/servers/:id` | 服务器状态 | member |
| GET | `/mcp/servers/:id/config` | 读取完整配置 | member |
| PUT | `/mcp/servers/:id` | 更新并重连 | admin |
| DELETE | `/mcp/servers/:id` | 断开当前连接 | admin |
| DELETE | `/mcp/servers/:id/config` | 永久删除配置 | admin |
| POST | `/mcp/servers/:id/reconnect` | 重连 | admin |
| GET | `/mcp/servers/:id/tools` | 列出工具 | member |
| GET | `/mcp/servers/:id/resources` | 列出资源 | member |
| POST | `/mcp/tools/:toolId/execute` | 执行工具 | active member |
| GET | `/mcp/skills` | 列出 MCP skill 投影 | member |
| GET | `/mcp/skills/:id` | 查看 MCP skill | member |
| POST | `/mcp/skills/refresh` | 重建 skill registry | admin |
| GET | `/mcp/status` | 连接状态汇总 | member |

## Agent 集成

`api/wiring/agent.go` 把 MCP tool provider 注入 `AgentService`。Agent 配置中的 `MCPServerIDs` 限定可用服务器；执行时 `buildExtraTools` 合并 MCP 工具与已允许的平台 Skill，同名内置 `stratum_*` 工具不会被覆盖。

## 验证

```bash
go test -short ./internal/mcp/... ./api/http/handler/...
go vet ./...
```

需要真实 MCP server 时，使用非生产测试实例，并在验证后删除临时配置。
