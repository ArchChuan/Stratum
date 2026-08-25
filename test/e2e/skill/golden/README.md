# Golden 评测集：skill 单点评测

本目录是 skill 单点评测（`cmd/e2e-eval-check --kind skill`）的首期 golden 数据集。skill 评测通过一个临时 carrier agent 执行：CLI 从 point 快照解析 skill 名，创建带 `allowedSkills: [skill-name]` 的 carrier agent，再把用户 query 交给它执行，最后按 case 模式断言或 judge 输出。

## 数据集用途

- 开发/测试态对 skill 行为做单点抽查与回归：修改 skill 内容、carrier agent 指令或模型后，用固定基线对比，防止回归。
- 每个 case 是对「该 skill 在给定 query 下应产生正确行为」的行为契约。

## 快照语义：声明值 vs 运行时

point 的 `snapshot` 区分两类字段，指纹与运行时语义不同：

- **`snapshot.skill.name`（运行时生效）**：carrier agent 的 `allowedSkills` 只携带 skill 名。运行时执行的是**注册表里同名的 live skill**，而非快照中的 content 副本。
- **`snapshot.skill.content`（声明值）**：快照中的 skill 正文是**声明值**，不直接传输给 carrier agent；它用于记录「评测针对的 skill 内容版本」并参与配置指纹。改 content 会触发指纹漂移 → 基线对比标 `non_comparable` → 强制人工决策，防止「改了 skill 正文但评测静默沿用旧基线」。
- **`snapshot.agent.model` / `snapshot.agent.system_prompt`（运行时生效）**：carrier agent 的 `llmModel` 与 `systemPrompt`，同样参与指纹。

因此：修改 skill 正文后，若确实要作为新基线，必须先 review 受影响 case 的标注，再 `--record-baseline --confirm-record` 显式重录，禁止隐式更新基线。

## 更新流程

- 新增用例：提交到本目录供人工 review，确认 query 是自然提问、`note` 写明标注理由后再合入。
- 修改 skill 正文或 carrier agent 配置：同步 review 受影响 case 的标注是否仍成立；指纹漂移会显式暴露这类变更，禁止静默绕过。

## 防过拟合红线

- 固定基线对比：评测以录制基线为准，禁止针对本数据集动态调参或特判。
- 标注可 review：所有 case 均写明 `note`，供复核，禁止静默改动。
- 禁止为过测扭曲实现：skill 执行逻辑不得因 golden 数据集的 case 而做特判；golden 只用于度量，不驱动实现。
