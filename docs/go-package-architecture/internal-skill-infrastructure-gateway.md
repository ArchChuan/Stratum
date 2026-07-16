# internal/skill/infrastructure/gateway（已移除包）

当前仓库不存在该目录。旧 Skill Gateway、pipeline、provider、circuit breaker 与 audit 实现已随 capability-boundary 重构移除；Agent 现在通过 `internal/agent/domain/port.CapabilityGateway` 路由 LLM，并通过 MCP provider/executor 调用外部工具。

当前相关架构见 [`internal/agent/infrastructure/capability`](internal-agent-infrastructure-capability.md) 与 [`internal/mcp/infrastructure`](internal-mcp-infrastructure.md)。

本页保留原路径索引，避免旧链接失效；它不计入当前 Go package 总数。
