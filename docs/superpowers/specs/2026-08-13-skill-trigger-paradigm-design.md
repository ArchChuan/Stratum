# Skill 触发范式重构 — 统一 catalog 工具 + system 注入位置指引

日期:2026-08-13
状态:已批准(5 维度并行评审证实全部关键机制;两处 P0 经用户拍板:memory/knowledge 边界继承 agent 绑定、单工具描述两阶段截断)

## Context

skill 触发范式要对齐 Claude Code / Codex 的外部范式,同时适配云端多租户现实。三家(平台/Claude Code/Codex)的 skill 触发机制各不相同、无行业标准。最终决策:发现触发对齐 → 锚定 agent 级 catalog → 工具面收敛为统一通道 → 统一工具命名 `stratum_skill` → 注入方式 = 维持 system 单通道 + 工具结果返回注入位置指引。

现状关键事实(源码核实 + 并行评审证实):

1. **每 skill 一工具**:`buildSkillToolDefinitions`(`react_helpers.go:75`)为 catalog 每个 skill 生成独立 ToolDefinition。
2. **两拍激活**:`dispatchSkillTool`(`react_tool.go:429`)匹配后写 `s.Actives`,工具结果仅 `"activated skill X revision Y"` 空壳;下一轮 `messagesWithActiveSkills`(`react_helpers.go:208`)把 instructions 作为 system 消息注入,标题格式 `"Active Skill %s (revision %s)"`。
3. **memory/knowledge 边界是执行时 gate,不是工具白名单**:`allowedKnowledgeWorkspaces`(`react_tool.go:892`)在 agent 绑定 `AgentKnowledgeWorkspaceIDs`(=ec.workspaceNames,`agent.go:889`)之上,再按 active skill 声明的 `KnowledgeWorkspaceIDs` 做交集(`react_tool.go:912-916`);`execRecallMemoryTool`(`react_tool.go:527`)在 `len(s.Actives)>0` 时要求任一 active skill 的 `MemoryScopes` 包含 agent 绑定的 scope,否则 error(`react_tool.go:529-530`,`anyActiveAllowsMemoryScope` `react_helpers.go:199`)。这两个 gate 是 memory/knowledge 唯一作用域控制,builtin 工具不走审批链。
4. **工具可见性**:`toolAllowedByActives`(`react_helpers.go:139-157`)在激活后按 skill 白名单过滤工具;审批链另有 `ActiveSkillAllows` 越界 deny(`tool_execution_guard.go:49-52`)。
5. **契约负担**:activation contract 强制 `inputSchema/outputSchema` 为 `{"type":"object"}` 且 `Confirmed`(`version.go:89`);但 `version_service.go:542-547` 已把 nil schema 默认成 `{"type":"object"}`,schema 实为软约束,作者真实负担是 `Confirmed` 开关。
6. **LLM 循环硬约束**:工具结果无论多长,模型只能下一轮看到——"一次到位"不省轮次,真正的价值是认知连贯(模型明确知道激活了什么、指令在哪)。
7. **`fitToolList`**(`react_llm.go:546-557`)按整工具粒度贪心裁剪;单一工具描述超预算会导致整个工具被丢(该步零 skill 可激活的静默失败)。

## 设计决策

### D1. 统一 `stratum_skill` 工具 + 两阶段截断

- 删除"每 skill 一工具"生成,改为单工具定义:
  - `Name = "stratum_skill"`,`Parameters = {"type":"object","properties":{"skill":{"type":"string"}}}`。
  - `Description` = 动态拼接:逐行列出 agent 绑定集合内每个 skill 的 `name + description`。
- **两阶段截断**(用户拍板,评审证实机制缺失):
  - 阶段一:在 `prepareLLMRequest` 内先用当前 messages 估算剩余上下文预算,得到 `stratum_skill` 描述可用的 token allowance。
  - 阶段二:按 allowance 构建描述——先缩短每条 skill 的 description(超长先缩每条、再整条略去,保证列表骨架完整);构造结果恒 fit 门槛,该工具在 `fitToolList` 中永不被整工具丢弃,杜绝"零 skill 可激活"的静默失败。
  - 实现注记:描述构建须移到 allowance 可得之后(`fitToolsToContextBudget` `react_llm.go:506` 之后);描述无法静态预构建,需随预算动态生成。
- **空 catalog 行为**:绑定集为空则省略该工具。
- **保留名冲突**:`buildSkillCatalog`(`agent_service.go:2516`)唯一性校验除重名外,显式拒绝 `stratum_skill` 及内置工具名(`stratum_search_knowledge`/`stratum_recall_memory`/`stratum_create_plan`/`stratum_revise_plan`/`stratum_continue_plan`/`stratum_cancel_plan`)。
- 消费方零影响:workflow(`api/wiring/workflow.go:101` 固定修订激活)、evaluation、system assistant(governed 短路)。

### D2. 激活语义:结果返回"注入位置指引"

- `dispatchSkillTool`(`react_tool.go:429`)匹配逻辑改为:读参数 `skill`(catalog 里的 name)→ 解析到 skill id + activation。
- 工具结果从空壳改为位置指引,不重复正文,例如:
  `Skill <name> (revision <id>) 已激活。完整指令已注入 system 消息『Active Skill <name> (revision <id>)』,后续轮次按此执行。`
- 下一轮 `messagesWithActiveSkills` 的 system 注入保持现状(标题格式不变),标题与指引一致 → 认知连贯。

### D3. 多 skill 叠加与上下文压缩

- **叠加语义保留**:`stratum_skill` 可多次调用,`upsertActivation`(`react_helpers.go:188`)同名更新、异名叠加,system 连续拼接 N 条 `Active Skill X (revision Y)`。
- **冲突语义:并列 + 模型自决(显式化)**。注入文本写明"多个 skill 并列生效,指令冲突时由模型自行取舍"。靠声明不靠位置——已核实压缩截断按 size(`truncateProtectedUntilFit`,`compaction.go:333`)不按顺序,位置/顺序语义会被压缩打乱。
- **预算:不设激活数量/token 上限,依赖现有压缩机制**。三重保护:前导 system 全为 anchor 永不整体剔除(`markAnchors`/`evictUntilFit`);压缩只作用请求拷贝 + 每步从 `s.Actives` 重注入完整指令(`react_llm.go:106/133`),单步截断下一步自动恢复;`fitToolsToContextBudget` 走独立工具配额不挤占。接受的代价:skill 指令撑大 anchor head → 对话历史更早被摘要/丢弃。
- **位置指引的截断边界**:`truncateProtectedUntilFit` 只截头部保留,标题在消息头部、指令被截时标题仍存活,位置指引基本可靠;仅当指令截到标题以内才失效,属可接受残余风险。

### D4. 契约负担:inputSchema 可选

- `ActivationContract`(`version.go:34`):`InputSchema`/`OutputSchema` 字段加 `omitempty` 语义可选;`Validate` 只强制 `Name` + `Description`,删除 `isObjectSchema` 校验(96-101)。
- **`Confirmed` 保留为发布门禁**:`version.go:102-104` 的强制是"作者确认契约已就绪",与 inputSchema 是否填写无关;前端 Switch、proto `confirmed` 字段不动。
- **proto 不改**:`inputSchema/outputSchema` 是 `google.protobuf.Struct`(`skill.proto:39-40`),proto3 message 本就有 presence,无需 `make proto-gen`。
- `capability.InputSpec/OutputSpec` 保留不动——它服务 evaluation,与 LLM 工具入参是两回事。
- 解析名唯一性:绑定集合内 skill 的 contract Name 必须唯一(或解析时 ambiguity fail-closed),在 `buildSkillCatalog` 侧校验。

### D5. 权限收窄:工具面=agent 绑定全集,memory/knowledge 边界继承 agent 绑定

- **工具可见性**:去掉 `toolAllowedByActives`(`react_helpers.go:139-157`)过滤——激活 skill 后工具面 = agent 绑定全集(`agent.mcpToolIds`),不再按 skill 白名单隐藏工具。`isReservedPlanTool` 去重(plan 工具由 `effectiveTools` 单独追加、不在 `s.AvailableTools`)作为防御不变量内联保留。
- **审批链**:去掉越界 deny(`tool_execution_guard.go:49-52` 的 `ActiveSkillAllows`),`Actives` 不再传入 guard。安全由双保险兜底:`read/write_reversible` 直接执行、`destructive/unclassified` 人工审批,无 auto-approve、无角色豁免、审批即运行终止+resume。
- **memory/knowledge 执行时 gate:继承 agent 绑定**(用户拍板,区别于"完全去掉"):
  - `execRecallMemoryTool`(`react_tool.go:527`):删除 `len(s.Actives) > 0 && !anyActiveAllowsMemoryScope(...)` 分支(529-530),保留 `RecallMemoryFn` 存在性判断;scope 直接按 agent 绑定的 `s.AgentMemoryScope`。
  - `execSearchKnowledgeTool`(`react_tool.go:456`) + `allowedKnowledgeWorkspaces`(`react_tool.go:892`):去掉 skill 声明交集段(897-902, 912-916),只按 agent 绑定的 `s.AgentKnowledgeWorkspaceIDs` 过滤。
  - `anyActiveAllowsMemoryScope`(`react_helpers.go:199-206`)删除。
  - **安全边界不破**:memory scope 与 knowledge workspace 仍受 agent 绑定约束,只是不再受 skill 声明叠加约束;skill 激活既不扩大也不缩小 agent 的能力边界。
- `requirements.MCPToolIDs/MemoryScopes/KnowledgeWorkspaceIDs` 降级为声明性元数据(文档/校验/作者意图),不做运行时过滤;skill 编辑页不再强制填写资源 ID。**【后续全链路移除**:三字段已从 proto/DTO/表单/写入路径整体删除(指令即自然语言描述资源),DB 列 `skill_revisions.requirements` 保留空列 `'{}'` 不做破坏性迁移;Agent 的工具/知识/记忆边界完全由 agent 绑定决定。**
- 收益:消除"激活 skill 即隐藏 agent 其他工具/改变 memory-knowledge 边界"的持续副作用,工具面行为可预期。

### D6. 重复命中拦截

- `dispatchSkillTool` 匹配到已在 `s.Actives` 的 skill 时拦截,不再走激活流程,返回:`"Skill X (revision Y) 已激活,完整指令已在 system 消息『Active Skill X (revision Y)』,直接按指令执行,无需重复激活"`——幂等 + 复用位置指引引导,避免重复消耗轮次。
- `upsertActivation` 的存储层去重保留,拦截分支是入口层的显式去重。
- 可选增强:`stratum_skill` 的 description 对已激活 skill 动态标注(如 `(已激活)`),让模型看列表即可知哪些已生效。

## 落地范围

- `internal/agent/application/graph/react_helpers.go`:删 `buildSkillToolDefinitions` N 工具生成 → 单工具构建 + 两阶段预算截断;删 `toolAllowedByActives` + `anyActiveAllowsMemoryScope`;`messagesWithActiveSkills` 注入文本加"并列生效、冲突由模型自决"声明;内联保留 `isReservedPlanTool` 去重。
- `internal/agent/application/graph/react_tool.go`:`dispatchSkillTool`(429)参数匹配 + 位置指引 + 已激活拦截;`execRecallMemoryTool`(527)删 scope gate;`execSearchKnowledgeTool`(456)/`allowedKnowledgeWorkspaces`(892)去 skill 声明交集;`classifyToolProvider`(828)对 `stratum_skill` 记录 `Arguments["skill"]` 恢复逐 skill 观测归因。
- `internal/agent/application/tool_execution_guard.go`:删 `ActiveSkillAllows` 越界 deny(49-52),`Actives` 不再传入 guard。
- `internal/agent/application/agent_service.go`:`buildSkillCatalog`(2516)name 唯一性 + 保留名拒绝。
- `internal/agent/application/graph/react_llm.go`:`prepareLLMRequest`/`fitToolsToContextBudget`(506)适配两阶段截断。
- `internal/skill/domain/version.go`:ActivationContract `Validate` 放宽;字段加 `omitempty`。
- `internal/skill/application/version_service.go`:确认 nil schema 默认填充语义(声明性,不动或注释说明)。
- 前端:`web/src/modules/skill/pages/SkillWorkspacePage.tsx` 契约 tab 去 inputSchema/outputSchema 强制项;requirements 资源 ID 改可选/说明降级。**【全链路移除**:requirements 表单项已从创建页/工作台删除。**
- 测试:`tool_authorization_test.go`、`tool_authorizer_test.go`、`react_test.go`、`tool_permission_e2e_test.go` 全量改造(均在验证被删行为);新增 stratum_skill 分发/截断/重复命中/空 catalog/保留名/memory-knowledge 继承测试。
- 契约测试:`api/http/contract_test.go` + `testdata/contracts/*skill*.golden.json`。
- 文档:`docs/agent/agent.md` 删除 `stratum_activate_skill` 旧设计;交叉引用同步 `docs/superpowers/specs/2026-08-04-platform-assistant-carrier-architecture-design.md:37`、`docs/agent/agent-chat-flow.md:85`。
- proto:不改(Struct 已有 presence)。

## 验证

- 后端:`bash scripts/quality/risk-regression-guard.sh --explain` 先跑;`go vet && go test -short ./...`;PR 前 `go test -v -race -timeout 30s ./...`。
- 前端:`make fe-lint && make fe-build`。
- E2E:单 skill 激活按指令执行、多 skill 叠加、重复激活拦截、激活不隐藏 agent 其他工具、destructive/unclassified 仍人工审批、memory/knowledge 仍按 agent 绑定执行、预算紧张时 stratum_skill 仍可达。
- 契约:`make check`(含 dto-residue-guard)确认生成物无残留。
