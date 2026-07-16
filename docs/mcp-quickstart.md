# MCP 快速开始

## 1. 启动 Stratum

```bash
make infra-up
make infra-wait
make run
```

MCP API 需要已登录用户和 tenant context。服务器配置不从 YAML 自动加载，而是通过前端 MCP 页面或 `/mcp/servers` API 持久化。

## 2. 添加服务器

管理员提交 `ServerConfig`。stdio 服务器使用 `transport` + `command` + `args`；HTTP/SSE 服务器使用 `transport` + `url`。不在命令行、文档或截图中暴露认证字段。

## 3. 检查连接

```text
GET /mcp/servers
GET /mcp/status
GET /mcp/servers/:id/tools
GET /mcp/servers/:id/resources
```

## 4. 配置并执行工具

输入 schema 以 `/mcp/servers/:id/tools` 返回的 `inputSchema` 为准。平台不提供通用 HTTP 工具执行路由；工具由 Agent ReAct 运行时通过内部 `MCPToolExecutor` 调用。

管理员可用 `PUT /mcp/tool-policies/:serverId/:toolName` 设置工具风险级别。危险或未分类工具会暂停执行并进入 `/agents/tool-approvals`，管理员决策后再恢复对应 Agent 执行。

## 5. 供 Agent 使用

在 Agent 配置中选择相应 MCP server。Agent 执行时会把该服务器已发现的工具加入 ReAct 工具集。

完整路由、权限和架构见 [mcp-integration.md](mcp-integration.md)。
