---
title: Agent 使用指南
---

# Agent Development Rules

## Built-in Platform Assistant

每个 tenant schema 由 `pkg/storage/postgres/tenant_schema.sql` 幂等 provision 恰好一条托管 Agent：
`id=stratum-platform-assistant`、`system_key=stratum.platform_assistant`。识别与展示按 `id`/`system_key`
判断；等同化后与普通 Agent 行为一致（读取/更新走通用 Agent 路由 `GET/PUT /agents/:id`）。

运行时不信任数据库中的托管字段。`SystemAssistantProfile` 按
`CurrentSystemAssistantProfileVersion=2026-08-08.v3` 选择保留在代码中的不可变 Profile，并覆盖名称、描述、
迭代和上下文预算（system prompt 由 `agents.system_prompt` DB 字段承载，不再随 Profile 走代码常量），同时
清空 Skill、MCP、Knowledge、Memory 等租户扩展。执行 trace 和 artifact 记录 `system-assistant-profile`
版本，历史版本继续可解析。

官方知识来自构建期生成并 embed 的只读 catalog：manifest 是 `docs/assistant/catalog.yaml`，生成器位于
`internal/agent/infrastructure/officialdocs/generate`。检索结果必须包含 document ID、标题、产品版本、章节、
官方 URL 和有界 excerpt；无匹配返回 `official evidence not found`，不得回退为模型臆测。

角色证据边界如下：

| tenant role | diagnostic scope | 可见证据 |
|---|---|---|
| member | `self` | 当前用户关联的 Agent/Skill/MCP/Knowledge/Model 脱敏状态 |
| admin/owner | `tenant` | 当前租户上述五个 area 的脱敏汇总 |
| membership 读取失败/未知角色 | 无 | fail closed，不执行 area collector |

系统助手向所有角色暴露 `stratum_search_official_docs` 与 `stratum_diagnose_tenant`；仅 admin/owner 额外看到
`stratum_propose_resource_change`。普通 Agent 看不到这些平台工具。诊断 collector 有界并发、逐 area 独立失败，失败用 `evidence_unavailable`、
`evidence_timeout` 或 `evidence_cancelled` 表达，禁止把缺口写成事实。

诊断 artifact 为 `citations` 和 `diagnostic_report`。报告只保存 typed facts、evidence gaps、建议、工具步骤、
耗时和引用；`inferences` 必须为空。所有字段经凭据脱敏、长度/数量/JSON 大小边界校验后写入
`chat_messages.artifacts_json`。

资源变更使用 `resource_change_proposal` artifact 和独立审阅页。模型只能创建严格类型的 proposal，不能调用资源
写 service。支持 `agent`、`skill_draft`、`mcp_config`、`knowledge_workspace` 的 `create`/`update`；不支持删除、
Skill publish、MCP tool execution 或 Knowledge upload。proposal payload 使用 closed JSON Schema，未知字段和
`token`、`apiKey`、`Authorization`、`password`、`env`、`headers` 等凭据形字段在持久化前拒绝；无效记录只保存
`{}` 与安全错误码。

proposal 状态由 application service 硬编码：`ready_for_review -> confirmed -> applying -> applied`，并可终止为
`invalid`、`stale`、`expired`、`failed`、`unknown_outcome` 或 `cancelled`。创建、编辑、确认、apply 前都重新授权；
update 保存去密 typed baseline projection 供审阅页显示 old/new 差异，并以独立 fingerprint 做冲突判断；确认时重新计算
fingerprint，不同则转 `stale`。确认和 apply
claim 是 tenant PostgreSQL 原子更新，并发请求只允许一个 applier。`unknown_outcome` 无 API/UI 重试入口。

MCP proposal 仅接受 `stdio`、`streamable-http`；HTTP URL 禁止 userinfo 和凭据形 query key。create 强制
`AuthTypeNone`；update 只能修改非敏感配置，保存的 env、headers、bearer/API key/OAuth
凭据保持原值且不进入 baseline、artifact、响应或日志。所有 proposal/event SQL 经 `execTenant`，审计事件追加写入
`resource_change_proposal_events`。管理员路由为 `GET/PATCH /resource-change-proposals/:id`、
`POST /resource-change-proposals/:id/cancel|confirm`。

## Capability Boundaries

Agent Loop 是运行期唯一动态决策者。其他上下文职责固定：

| 上下文 | 职责 | 禁止事项 |
|---|---|---|
| Agent | 推理、选择 Skill、调用工具、状态机、checkpoint | 不在 handler 中实现路由或重试 |
| Skill | 版本化 instruction bundle：capability、activation contract、instructions | 不执行代码、HTTP、LLM 或 MCP |
| MCP | 外部工具发现和副作用执行 | 不自报可信 risk level，不伪装成 Skill |
| Knowledge | `stratum_search_knowledge` 内部检索能力 | 不执行外部副作用 |
| Memory | 自动会话历史、注入、按需 recall | 不作为通用工具网关 |

依赖方向：`Agent -> Skill snapshot / MCP port / Knowledge port / Memory port`。Skill 不依赖或调用 MCP；工具、知识与记忆边界只由 Agent 绑定决定（Spec D5）。

## AgentConfig

关键权限字段：

```go
type AgentConfig struct {
    AllowedSkills         []string // 可激活 Skill ID
    MCPToolIDs            []string // mcp:<server>:<tool> 精确 allowlist
    KnowledgeWorkspaceIDs []string
    MemoryScope           string
}
```

权限取交集：租户权限 ∩ 用户权限 ∩ Agent allowlist。Agent 绑定具体 MCP tool，不绑定整个 server，避免 server 新增工具后自动扩权；激活 Skill 只注入指令，不再参与权限收窄（Spec D5）。

## Skill Activation

- Run 启动时解析允许的 published/candidate Skill revision，并固定 revision ID；绑定集合内的 contract Name 必须唯一且不得与平台内置工具名冲突（fail-closed）。
- 工具面收敛为**单一内置工具** `stratum_skill`（参数 `skill`，值为绑定集合内 skill 的 Name，回退 SkillID）。该工具描述动态列出绑定集合内每个 skill 的 name+description，已激活 skill 标注 `(已激活)`；描述按两阶段截断压缩进上下文预算（先算 allowance 再按 allowance 截断），保证工具在 `fitToolList` 贪心打包中恒被保留。绑定集为空时省略该工具。
- 模型调用 `stratum_skill` 激活一个或多个 instruction bundle；同一时刻允许多个 active Skill 叠加生效，**冲突语义 = 并列 + 模型自决**（注入文本显式声明）。
- 已激活 Skill 再次调用 `stratum_skill` 被拦截（幂等提示，不重复激活、不消耗轮次）。
- 激活结果返回**注入位置指引**（不重复正文）：`Skill <name> (revision <id>) 已激活。完整指令已注入 system 消息『Active Skill <name> (revision <id>)』`。
- 激活后 system messages 按激活顺序注入全部已激活 revision 的 instructions（作为连续 system 消息块插在首条 system 消息之后），标题格式 `Active Skill <name> (revision <id>)` 与指引一致。
- 权限边界**继承 Agent 绑定**：激活 Skill 既不扩大也不缩小 Agent 的能力边界。MCP 工具面 = Agent allowlist 全集（不再按 Skill 声明收窄）；knowledge 按 `Agent workspaces` 过滤；memory 按 Agent 绑定 scope 执行。Skill 声明的 `MCPToolIDs/MemoryScopes/KnowledgeWorkspaceIDs` 降级为声明性元数据，不做运行时过滤。
- Skill 不生成可执行 ToolDefinition，也不经过 CapabilityGateway。
- Agent 可以不激活 Skill，直接使用 Agent allowlist 中的 MCP 工具。

## Tool Execution

`CapabilityGateway` 只负责 LLM completion。外部工具调用通过消费侧 `MCPToolExecutor`，最终到 `ClientManager.CallTool(serverID, rawToolName, args)`。

暴露给模型的名称是 `mcp:<server>:<tool>`；发送给 MCP server 的 name 是原始 tool name。不得混用。

内置工具：

| 工具 | 权限 |
|---|---|
| `stratum_skill` | 当前 Run 的 Skill catalog（统一触发，参数 `skill`） |
| `stratum_search_knowledge` | Agent workspaces |
| `stratum_recall_memory` | Agent 绑定 memory scope |
| `stratum_continue_reasoning` | Agent Loop 内部控制 |

## Context Budget And Compaction

`AgentConfig.MaxContextTokens` 控制 Agent 每次 LLM 请求的上下文上限；0 = 自动按模型窗口解析（窗口 known → `0.85×window`，未知 → `constants.DefaultAgentContextTokens`=32768）。当前运行行为分两层：

1. 初始上下文由 `BuildContextMessagesWithCompaction` 组装，优先级为当前输入 > system prompt 保底 > memory（剩余预算最多 30%）> 会话历史。窗口外和超出预算的最老历史会交给 `HistoryCompactor` 生成摘要，摘要只注入当次请求的 system message。
2. ReAct 循环（包括 Planning 子步骤的 ReAct）每次调用 LLM 前对消息副本估算 token；达到 `MaxContextTokens * LoopCompactionSafetyRatio`（当前 80%，固定平台阈值，不暴露用户配置）后，保留 system/user 锚点和动态保留组数的最近完整消息组（compaction_recent_groups，0 = 自动推导 2/3/5 组），较老中间组整体压缩。assistant tool call 与对应 tool result 必须作为原子组保留或删除，禁止产生孤立消息。Reflect、Plan、Synthesize 的结构化单次请求不在本次循环压缩范围内。

生产 wiring 在每次执行解析租户 `CapabilityGateway` 后，用 Agent 配置的 LLM model 创建 `LLMHistoryCompactor`，再注入 `BaseAgent` 和 ReAct/Planning 状态。这样摘要调用沿用当前租户的 provider 与凭据，不会跨租户共享网关。压缩失败、工厂未配置或未注入时必须降级为硬截断/计数标记，不能阻断 Agent Loop；trace 与持久化会话历史保持完整，压缩只影响当次 LLM 请求副本。

### Reasoning Effort 成本语义

`AgentConfig.ReasoningEffort`（`low`/`medium`/`high`，空串=unset）控制思考强度，网关按模型能力门控映射：OpenAI 兼容直传 `reasoning_effort`，Anthropic 映射 `extended_thinking`（budget low=2000/medium=8000/high=20000，max_tokens 自动抬升保留输出空间）。**成本警告**：high 档位 token 消耗显著放大（Anthropic budget 20000；OpenAI 高阶推理模型 token 成本陡增），且当前无 `max_tokens_per_execution` 联动——多轮 ReAct 执行下单 Agent 可成倍烧 token，属成本 DoS 风险。本期仅文档化，不联动限流；上限控制依赖租户级 `max_tokens_per_execution` 配置与人工预算治理。能力门控为 fail-closed：未知模型或非推理模型清空档位，不默认放行。

## MCP Risk And Approval

租户管理员为每个 `(server_id, tool_name)` 设置风险：`read`、`write_reversible`、`destructive`、`unclassified`。未配置或读取失败必须视为 `unclassified`。MCP discovery payload 不能设置受信风险。

- `read` / `write_reversible`：通过 Execution Guard 复核后直接执行。
- `destructive` / `unclassified`：Run 进入 `waiting_approval`，工具不得执行。
- 参数、query、固定 Skill revisions 使用 AES-256-GCM 存入 `agent_tool_approvals.encrypted_payload`。
- checkpoint 只保存 approval ID，不保存原始参数。
- 批准后使用同 execution ID 恢复；审批只解除风险门槛，不能补授 tenant、user、Agent 或 Skill 缺失的权限。
- resume 在执行前重新解析用户状态、Agent allowlist、固定 Skill revisions 和 policy version；仅完整绑定匹配时可执行一次。
- 执行前原子抢占 `approved -> executing`；失败回退 `approved`，成功转 `executed`，防止重复副作用。
- 请求发送后发生 timeout、cancel 或连接中断时进入 `unknown_outcome`，禁止自动重放，必须人工对账或补偿。

所有 MCP 调用都必须经过 `ToolExecutionGuard`；Tool Catalog 过滤只是最小暴露，不是授权执行点。MCP 返回值在进入
下一轮 LLM 上下文前必须经过 `ToolResultGuard`：拒绝协议/Schema 错误，脱敏、限长并标记为
`<untrusted_tool_result>`。MCP annotations 和外部文本均不可信。

阻断式回归入口是 `make tool-permission-test`。它覆盖纯授权性质、审批状态机、Execution/Result Guard、fake MCP、
Agent Loop/SSE、PostgreSQL tenant 隔离和审批 UI；CI 必须设置 `STRATUM_TEST_POSTGRES_URL`，缺失时直接失败。

## Rules

1. 路由、审批、重试、checkpoint 和权限交集必须硬编码，不交给模型决定。
2. MCP 工具默认 `unclassified`，禁止 fail-open。
3. Skill revision 在 Run 内不可漂移；审批恢复使用 payload 固定的 revision。
4. Tool trace 和 Agent trace 是不可变历史；删除旧 Skill 存储时必须保留。
5. `MaxIterations` 和 execution timeout 必须有限。
6. 上下文裁剪必须保持 tool call/tool result 配对，并保留 system/user 锚点；不得直接修改持久化历史或 trace。
7. 不记录 token、API key、password、审批明文 payload 或敏感原始响应。
8. Agent/Skill/MCP/Knowledge/Memory 改动必须完成真实 API、数据库、Agent Loop 和浏览器 E2E。
