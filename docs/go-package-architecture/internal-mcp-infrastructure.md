# internal/mcp/infrastructure

该包以自有 JSON-RPC 协议实现 MCP stdio/HTTP 客户端、连接管理/健康检查、能力缓存、端口适配和 Agent 工具桥接。

完整导入路径：`github.com/byteBuilderX/stratum/internal/mcp/infrastructure`

```mermaid
flowchart TB
  cache["cache.go<br/>CapabilityCache / CacheEntry<br/>TTL、容量上限、任意 map 条目淘汰与失效"]
  client["client.go<br/>BaseClient / MCPClient<br/>stdio 子进程或 HTTP JSON 请求<br/>Connect·ListTools·CallTool·ListResources"]
  manager["client_manager.go<br/>ClientManager<br/>服务器注册、连接池、健康检查、关闭"]
  adapters["port_adapters.go<br/>ServerManagerAsPort / SkillRegistryAsPort<br/>RegistryAsAgentToolProvider"]
  skill["skill_adapter.go<br/>MCPSkillAdapter / MCPSkillRegistry / MCPSkillWrapper<br/>工具发现与执行桥接"]
  types["types.go<br/>MCPConfig / Request·Response / ToolCall·Result<br/>ConnectionPoolConfig / MonitoringConfig / filters"]
  domain["internal/mcp/domain"]
  ports["internal/mcp/domain/port"]
  agentport["internal/agent/domain/port"]
  constants["pkg/constants"]
  tenantdb["pkg/tenantdb"]
  protocol["自定义 MCP JSON-RPC<br/>stdio 管道 / HTTP POST"]
  pg["外部：pgx/v5 + PostgreSQL"]
  zap["外部：zap"]
  tests["测试汇总<br/>integration_test.go / mcp_test.go"]
  manager --> client
  manager --> cache
  manager --> types
  skill --> manager
  skill --> domain
  adapters -.适配.-> ports
  adapters -.适配.-> agentport
  manager --> tenantdb
  manager --> pg
  client --> protocol
  client --> constants
  client --> zap
  manager --> zap
  pkg -.-> tests
```

`BaseClient` 自行构造 `MCPRequest`/`MCPResponse`：stdio 模式启动子进程并通过标准输入输出交换逐行 JSON，HTTP/streamable-http 模式发送 JSON POST，并处理认证与 session header。`BaseClient` 和 `ClientManager` 直接使用 zap 记录连接、请求、健康检查与错误；管理器维护按租户和服务器 ID 的客户端及配置。`CapabilityCache` 以 TTL 缓存能力，满容量时删除 Go map 遍历遇到的任意首项，并非 LRU。适配函数将具体管理器/注册表收窄成领域和 Agent 端口，技能适配器再把 MCP 工具映射为可执行能力。
