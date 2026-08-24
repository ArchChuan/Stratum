# Platform 配置版本管理 —— Review 决策记录

> 范围：`feat/platform-config-versions`（平台配置版本化 + trace 完整性 + 配置变更审计展示）。
> 本文记录多 agent review（正确性 / 安全 / 测试 / 产品体验 四维度）后的**已采纳修复**与**明确不做的取舍**，作为 review 证据与后续维护参考。

## 一、已采纳修复（全部有代码 + 测试支撑）

| # | 维度 | 缺陷 | 修复 |
|---|------|------|------|
| Fix A | 产品体验 | 前端 `isCurrent` 用 `JSON.stringify(snapshot) === effectiveJSON` 判定，真实多分组下 `PlatformValues` 是跨组平铺 map、快照是分组粒度，比对恒 false → production 所指版本也露出「回滚」按钮 | 后端 `PlatformVersion` 加 `IsCurrent`（ListVersions LEFT JOIN production label 填充）；前端改用 `v.status === 'published' && v.is_current` |
| Fix B | 安全 | sanitize 指纹前缀 off-by-one：`"sha256:` 前缀 8 字节，`raw[:7]` 恒 false，使「已指纹值透传」成为死代码 → 已指纹值会被重复哈希 | 改为 `len(raw) >= 8 && string(raw[:8]) == "\"sha256:"` + 单测（明文掩码 + 指纹透传） |
| Fix C | 正确性 | trim 计数只数 `status='published'`（不含 archived），否则 over-limit 随 archived 累积膨胀，最终把所有 published 连 production 一起 archive | count 只统计 published |
| Fix D | 正确性 | `parseSamplingRatio` 未拦截 `NaN`，`TraceIDRatioBased(NaN)` 静默变 100% 采样 | `math.IsNaN(f)` 显式拦截回退默认 + 单测 |
| Fix E | 正确性 | P0 agentSampler 只对 `agent.execute` root 生效，HTTP 触发（otelgin `/agents/:id/execute`）的 root span 无 `stratum.agent.execute` 属性，ParentBased 短路使其 90% 被 SDK 丢 | `agent.execute` 用 `WithNewRoot()` 独立根 + `observability.AgentExecuteAttrKey` 属性 + `NewAgentSampler`；`cfg.TraceID` 来自 payload 独立于 OTEL span traceID |
| H4 | 测试 | archived 目标版本可被 Publish/Rollback | SQL UPDATE 置 archived 后 Publish→ErrVersionNotDraft、Rollback→ErrVersionNotPublished（集成测试） |
| H5 | 测试 | trim 后审计行随版本删除 | trim 后 `platform_resource_change_audits` 行仍在（append-only 证据与状态存储解耦） |
| H6 | 测试 | diff 渲染无新增/删除标注断言 | 前端测试断言「新增 / 删除」Tag 渲染 |
| #27 | 产品体验 | 回滚确认文案未说明影响范围；diff 长 JSON 撑破表格 | 回滚文案明确「未显式声明该参数的租户资源回退」；diff 用「新增/删除」Tag；`wordBreak/overflowWrap` 折行 |

## 二、明确不做的取舍（未采纳）

以下项在 review 或设计阶段被提出，经评估后**未采纳**，理由如下（来源：plan + 用户拍板 + review 汇总）：

1. **独立审计日志界面**（评审提出「平台层应有独立审计 UI」）
   - **未采纳理由**：平台配置版本化视图天然含 who/when/message/snapshot diff + 一键回滚，信息量 > 纯审计日志且可操作。用户拍板「由版本配置可视化承载配置变更审计展示」。模型/provider 变更继续走既有 `platform_resource_change_audits` 写入，合规证据照常落库，本期不做展示。
2. **resource-scope 参数纳入平台版本体系**（评审提出「租户资源参数也应版本化」）
   - **未采纳理由**：resource-scope 已是租户自有 `resource_revisions` 的管辖范围（`AgentRevision` / evaluation candidate），两套版本轴正交；把租户参数纳入平台版本体系会破坏单一归属原则（declared 优先）。
3. **平台配置变更强制走评测门禁**（评审提出「配置变更应过 eval」）
   - **未采纳理由**：温度调 0.1 也走评测 = 过度工程。评测侧已有 `TunableRegistry`（`CatModelConfig`/`CatPrompt`）驱动 grid search / LLM rewrite，平台配置变更门禁属于 P2 可选，等真实需求再评估。
4. **按单参数版本化 / Dify 式整应用快照**
   - **未采纳理由**：温度 0.7→0.8 是廉价旋钮不值得一个版本；整应用快照把 agent（有独立 revision）、配置（有独立 registry）捆包破坏单归属。业界（Langfuse `config` 字段 / Dify）共识是「可复现配置集」为版本单元，本实现取**分组发布 + 全量指纹**。
5. **trace 指纹第三套哈希**（评审提出「config 指纹单独算法」）
   - **未采纳理由**：`config` 复用既有 `contentVersion(string(paramsJSON))`（与 `stratum.params.sha256` 同源），不新建哈希算法；`promptVersionMap` 的 `system_prompt` 与 `config` 独立成键，config 恒在（修 nil-map 边界）。

## 三、门禁回归记录（提交前）

| 门禁 | 结果 |
|------|------|
| `go vet`（受影响包） | ✅ |
| `go test -short ./...`（全量） | ✅ 全绿 |
| DB 门控版本化集成测试（`-tags integration`） | ✅ 6/6（含审计轨迹 + 审计失败原子性） |
| `make code-quality` | ✅ EXIT=0（Publish/Rollback 圈复杂度达标；BaseAgent.Execute 抽 helper） |
| `make risk-guardrails` | ✅ |
| `make fe-lint` | ✅（eslint --max-warnings 0） |
| `make fe-build` | ✅（3770 modules / 17.07s） |
| 前端 vitest | ✅ 14 tests |
