# MCP 实现摘要

> 当前状态摘要，不是早期实施计划。实际契约以 `internal/mcp/` 和 `api/http/handler/mcp_handler.go` 为准。

## 已实现

- DDD 分层：`domain` / `application` / `infrastructure`
- tenant-scoped MCP server 配置持久化与启动恢复
- stdio 与 HTTP/SSE 客户端路径
- bearer、API-key 和 OAuth2 配置结构
- 连接、断开、重连、更新、删除配置
- tool / resource 发现与 capability cache
- MCP tool 投影为 Agent 可用 skill
- member/admin 读写权限分离
- 并发、超时、重试和连接状态测试

## 关键文件

| 路径 | 职责 |
|------|------|
| `internal/mcp/domain/mcp.go` | ServerConfig、Tool、Resource、ServerInfo |
| `internal/mcp/application/mcp_service.go` | HTTP 用例编排 |
| `internal/mcp/infrastructure/client.go` | MCP transport 与 JSON-RPC |
| `internal/mcp/infrastructure/client_manager.go` | 连接生命周期与持久化 |
| `internal/mcp/infrastructure/cache.go` | tool / resource cache |
| `internal/mcp/infrastructure/skill_adapter.go` | MCP tool 到 Agent skill 适配 |
| `api/http/handler/mcp_handler.go` | REST API 与角色边界 |
| `api/wiring/mcp.go` | 组合根与启动恢复 |

## 运行时约束

- 不存在 `config/mcp.yaml`；配置通过 REST API / 前端管理并落库。
- 断开连接不等于删除配置；两者使用不同 API。
- 工具执行需要 active tenant；服务器管理需要 admin+。
- MCP 工具名由服务器提供，不使用平台 Skill 的 tenant 前缀规则。
- 认证字段属敏感数据，不得记录原值或放入版本库。

## 验证

```bash
go test -short ./internal/mcp/... ./api/http/handler/...
go test -race ./internal/mcp/...
```

使用方法见 [mcp-quickstart.md](mcp-quickstart.md)，完整 API 见 [mcp-integration.md](mcp-integration.md)。
