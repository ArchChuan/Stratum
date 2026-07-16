# internal/skill/infrastructure（已移除包）

当前 `internal/skill/infrastructure/` 根目录没有 Go package，仅保留 `.gitkeep` 和 `persistence/` 子包。旧的代码分析器与并发 semaphore 实现已随 Skill instruction capability 重构移除。

当前实现见：

- [`internal/skill/application`](internal-skill-application.md)
- [`internal/skill/domain`](internal-skill-domain.md)
- [`internal/skill/infrastructure/persistence`](internal-skill-infrastructure-persistence.md)

本页保留原路径索引，避免旧链接失效；它不计入当前 Go package 总数。
