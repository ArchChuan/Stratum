# Recent Bug Lessons Design

## Goal

将 2026-06-27 至 2026-07-16 的 Git 历史与 claude-mem 记录交叉整理为可长期执行的防复发规则，同时保持根目录 `CLAUDE.md` 和 `AGENTS.md` 为薄入口。

## Sources and inclusion

- 检索时间窗内的 Git `fix`、CI、部署、测试和修复型重构记录。
- 检索同一时间窗内 claude-mem 的 bugfix、失败、根因和验证记录。
- 纳入业务代码、前端、测试与迁移、并发与可观测性、CI/CD、K3s/Helm、远端排障。
- 只沉淀已修复且有根因或验证证据支撑的经验；未修复审计发现、临时命令和提交时间线不升级为规则。
- 合并同根因的多次修复，避免将一次排障过程写成多条重复规则。

## Output

- 新增 `docs/agent/bug-lessons.md`，按主题记录背景、根因、防复发规则和验证要求。
- 在 `CLAUDE.md` 与 `AGENTS.md` 增加高频硬规则摘要及 `docs/agent/bug-lessons.md` 索引。
- 已由专项规则覆盖的内容采用引用方式，避免与 `backend-go.md`、`migration-tenant.md`、`frontend.md`、`deployment-architecture.md` 等产生两套规范。

## Validation

- 建立来源到主题的覆盖检查，确认时间窗内可复用的修复均被归类或明确排除。
- 检查两个入口的摘要语义一致、链接有效、Markdown 格式正确。
- 使用 `git diff --check` 和范围化差异检查，确保不改动无关文件。
