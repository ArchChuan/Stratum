# internal/skill/infrastructure/executors（已移除包）

当前仓库不存在该目录。旧 HTTP、LLM 与 prompt Skill executor 已随 Skill instruction capability 重构移除；Skill 不再通过直接执行 HTTP API 运行。

当前 Skill 由已发布 instruction bundle 激活 Agent ReAct 行为，并通过 requirements 限定 MCP 工具、知识工作区和 memory scope。当前架构见 [`internal/skill/application`](internal-skill-application.md) 与 [`internal/skill/domain`](internal-skill-domain.md)。

本页保留原路径索引，避免旧链接失效；它不计入当前 Go package 总数。
