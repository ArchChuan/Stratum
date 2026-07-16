# internal/skill/infrastructure/gateway/providers（已移除包）

当前仓库不存在该目录。旧 code/LLM/MCP/database Skill provider 适配器已随 capability-boundary 重构移除；当前 published Skill 是 instruction bundle，不再构建动态 executable provider。

MCP 工具通过 Agent 的 `MCPToolProvider`/`MCPToolExecutor` 端口接入，LLM 通过 Agent capability gateway 接入。对应架构见 [`internal/agent/domain/port`](internal-agent-domain-port.md)。

本页保留原路径索引，避免旧链接失效；它不计入当前 Go package 总数。
