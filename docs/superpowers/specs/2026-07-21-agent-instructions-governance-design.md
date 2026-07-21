# Agent 指令单一事实源治理设计

## 目标

将根目录 `AGENTS.md` 与 `CLAUDE.md` 从两份独立维护的本地说明，改造成由同一公共事实源生成、受 Git 跟踪并由 CI 校验的工具入口，消除项目规则漂移，同时保留 Codex 与 Claude Code 必要的工具专属说明。

## 当前事实与证据边界

- 当前本地 `AGENTS.md` 为 311 行，`CLAUDE.md` 为 211 行；两者存在大量压缩、增补和顺序差异。
- 两个文件在提交 `202a2b0` 中以“local AI tooling artifacts”名义停止跟踪，随后被 `.gitignore` 重复忽略，因此本地修改不会进入 PR 或 CI。
- 仓库已有设计 `docs/superpowers/specs/2026-07-20-generated-report-governance-design.md` 要求两个入口保留一致的高风险检查表和同一个守卫入口，但当前只能依赖人工同步。
- Obsidian 检索到的相关任务简报观点标记为 `status: provisional`、`verification: pending`，只作为未核验线索，不作为本设计的关键依据。
- 本次尝试读取 OpenAI 与 Anthropic 官方说明时，Codex 手册 helper 被主工作区 hook 阻断，通用 fetch MCP 又因代理参数错误失败。因此，不能把“入口文件会自动递归加载其引用文档”当作已核验能力；生成后的入口必须自包含公共正文。

## 设计原则

1. `docs/agent/instructions.md` 是项目公共 Agent 指令的唯一事实源。
2. 工具入口是受版本控制的生成产物，而不是第二事实源。
3. 工具专属内容只描述加载语义、命令或 hook 差异，不重复项目架构、测试、安全和产品规则。
4. 生成过程必须确定、无网络依赖、可在开发机与 CI 中复现。
5. CI 只检查漂移，不修改工作区；本地命令可以显式重新生成。
6. 现有高风险守卫继续是确定性执行入口，文档生成不得引入 AI 判断。

## 文件结构

```text
docs/agent/instructions.md
docs/agent/templates/agents-prefix.md
docs/agent/templates/claude-prefix.md
scripts/quality/generate-agent-instructions.sh
scripts/quality/generate-agent-instructions-test.sh
AGENTS.md
CLAUDE.md
```

### 公共事实源

`docs/agent/instructions.md` 保存当前两个入口中所有跨工具规则，包括：

- 知识输入与证据协议；
- 技术栈、目录和架构决策；
- 多租户 DDL 与 repository 约束；
- DDD 分层和跨 context 依赖规则；
- Git/worktree、开发命令与验证要求；
- Go、前端、日志、常量和安全规则；
- 风险防回归 Harness；
- 产品设计原则；
- 分层上下文索引。

迁移时以“当前仍然正确且可由仓库事实支持”为准，不机械合并所有历史文字。两份入口有冲突时，必须选定一个明确规则；无法从代码、测试、设计或运行证据判断的冲突，不在迁移中静默裁决。

### 工具专属模板

`docs/agent/templates/agents-prefix.md` 仅保存 Codex/`AGENTS.md` 的入口说明，例如作用域、嵌套 `AGENTS.md` 的优先级或 Codex 专属工作流提示。

`docs/agent/templates/claude-prefix.md` 仅保存 Claude Code 专属入口说明，例如 `.claude/hooks/`、Claude Code 命令或本地加载注意事项。

模板不得出现公共架构、测试、安全、风险或产品规则。脚本测试通过禁止标题或关键标记检查这一边界，详细语义仍由代码评审把关。

## 生成契约

生成脚本按固定顺序组成入口文件：

1. 自动生成声明；
2. 对应工具专属模板；
3. 公共事实源全文。

自动生成声明必须包含：

```text
本文件由 scripts/quality/generate-agent-instructions.sh 生成。
请修改 docs/agent/instructions.md 或对应模板，不要直接编辑本文件。
```

脚本支持两种模式：

- 默认模式：先完整生成并校验两个临时文件，再逐个原子替换目标；内容未变时不触碰文件时间。第二个目标替换失败时，脚本用替换前备份恢复第一个目标并返回非零，避免长期留下半更新状态。
- `--check`：在临时目录生成并逐字比较；任一入口漂移时列出文件名并返回非零，不修改文件。

脚本只使用仓库约定可用的 POSIX/Bash 基础工具，不访问网络，不读取用户目录，不依赖 Python、Node 或 AI 服务。所有输入和输出路径从仓库根目录解析，允许从任意当前目录调用。

## Git 与自动化接入

### Git 跟踪

- 从 `.gitignore` 删除所有 `AGENTS.md` 和 `CLAUDE.md` 忽略规则，包括重复条目。
- 将公共源、模板、生成脚本、测试以及生成后的两个入口一并提交。
- 不改变 `.claude/`、`.codex/`、`.agents/` 等本地工具目录的忽略策略。

### Makefile

新增两个稳定入口：

```make
agent-instructions:
 /bin/bash scripts/quality/generate-agent-instructions.sh

agent-instructions-check:
 /bin/bash scripts/quality/generate-agent-instructions-test.sh
 /bin/bash scripts/quality/generate-agent-instructions.sh --check
```

### pre-commit

pre-commit 对公共源、模板、生成脚本或两个入口发生变更时执行 `--check`。它不自动修改暂存内容，避免工作区与 index 状态难以理解。开发者先运行 `make agent-instructions`，再重新暂存生成结果。

### CI

CI 运行 `make agent-instructions-check`。若生成结果与已提交入口不同，job 失败并提示执行本地生成命令。CI 不提交或上传修正文件。

## 测试设计

`scripts/quality/generate-agent-instructions-test.sh` 使用临时目录和最小 fixture，覆盖：

1. 默认模式能生成两个入口；
2. 两个入口都包含完全相同的公共正文；
3. 每个入口只包含自己的工具专属前缀；
4. 未发生内容变化时不改写输出；
5. 修改公共源后，`--check` 返回非零并同时报告两个入口漂移；
6. 只修改一个模板时，`--check` 只报告对应入口漂移；
7. 重新生成后 `--check` 恢复成功；
8. 从子目录调用时仍能正确定位仓库根目录；
9. 缺失公共源或模板时明确失败，不生成部分入口；
10. `AGENTS.md` 与 `CLAUDE.md` 均被 Git 跟踪且不再被 ignore。

现有 `scripts/quality/risk-regression-guard-test.sh` 中关于高风险区块一致性的测试，应改为验证公共事实源包含该区块，并验证生成后的两个入口与公共源一致，避免维护第二套同步逻辑。

## 失败处理

- 任一输入文件缺失或不可读时，生成脚本在写入前失败，保留两个旧入口不变。
- 任一临时文件生成或比较失败时，在替换目标前返回非零。目标替换阶段发生失败时执行回滚；回滚也失败时同时报告原始错误、回滚错误和受影响文件，不静默声称工作区一致。
- `--check` 输出只报告漂移或缺失文件，不打印可能包含本地专属敏感内容的完整 diff；开发者可自行运行 `git diff` 查看受跟踪内容。
- Markdown lint、生成测试或风险守卫失败时原样传播退出码，不降级为成功。

## 迁移步骤

1. 提取两份现有入口的共同规则并解决已知冲突，形成公共事实源。
2. 将确有必要的差异收敛进两个前缀模板。
3. 编写生成脚本及失败优先的测试。
4. 生成新的 `AGENTS.md` 与 `CLAUDE.md`。
5. 删除 `.gitignore` 中全部入口忽略规则并重新纳入 Git。
6. 接入 Makefile、pre-commit、CI 和现有风险守卫测试。
7. 运行 Markdown、脚本、风险守卫及生成漂移验证。

## 非目标

- 不在本次治理中重写所有 `docs/agent/*.md` 模块文档。
- 不把 `.claude/`、`.codex/` 或 `.agents/` 整体纳入 Git。
- 不依赖符号链接，因为不同工具、操作系统和托管执行环境对链接处理可能不同。
- 不让入口文件只保留外部引用；在官方递归加载行为未核验前，生成文件保持自包含。
- 不顺带修改业务代码、架构规则内容或风险守卫的业务判定。

## 验收标准

- `docs/agent/instructions.md` 是公共项目规则的唯一手写来源。
- `AGENTS.md` 与 `CLAUDE.md` 已被 Git 跟踪，并带有禁止直接编辑的生成声明。
- 修改公共源后，两个入口均产生确定性变化；修改单个模板只影响对应入口。
- `make agent-instructions-check` 在一致状态下成功，在任一入口漂移时失败。
- pre-commit 和 CI 使用同一 `--check` 入口。
- 两个入口中的高风险检查表和 `make risk-guardrails` 命令完全一致。
- `.claude/`、`.codex/`、`.agents/` 等本地目录继续保持忽略。
- Markdown lint、生成脚本测试和相关风险守卫测试全部通过。
