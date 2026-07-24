# Agent Development Rules

## Built-in Platform Assistant (Phase 1)

每个 tenant schema 由 `pkg/storage/postgres/tenant_schema.sql` 幂等 provision 恰好一条托管 Agent：
`id=stratum-platform-assistant`、`system_key=stratum.platform_assistant`。读取时返回
`isSystem=true`、`managementMode=platform`；通用 Agent update/delete/revision 路径必须返回
`system assistant is platform managed`。租户管理员唯一可改字段是 `llmModel`，普通成员可读取设置但不能写。

运行时不信任数据库中的托管字段。`BuiltinSystemAssistantProfileSource` 按
`CurrentSystemAssistantProfileVersion=2026-07-23.v1` 选择保留在代码中的不可变 Profile，并覆盖名称、描述、
system prompt、迭代和上下文预算，同时清空 Skill、MCP、Knowledge、Memory 等租户扩展。执行 trace 和 artifact
记录 `system-assistant-profile` 版本，历史版本继续可解析。

官方知识来自构建期生成并 embed 的只读 catalog：manifest 是 `docs/assistant/catalog.yaml`，生成器位于
`internal/agent/infrastructure/officialdocs/generate`。检索结果必须包含 document ID、标题、产品版本、章节、
官方 URL 和有界 excerpt；无匹配返回 `official evidence not found`，不得回退为模型臆测。

角色证据边界如下：

| tenant role | diagnostic scope | 可见证据 |
|---|---|---|
| member | `self` | 当前用户关联的 Agent/Skill/MCP/Knowledge/Model 脱敏状态 |
| admin/owner | `tenant` | 当前租户上述五个 area 的脱敏汇总 |
| membership 读取失败/未知角色 | 无 | fail closed，不执行 area collector |

系统助手只暴露 `stratum_search_official_docs` 与 `stratum_diagnose_tenant` 两个硬编码 internal tool；普通
Agent 看不到它们。诊断 collector 有界并发、逐 area 独立失败，失败用 `evidence_unavailable`、
`evidence_timeout` 或 `evidence_cancelled` 表达，禁止把缺口写成事实。

Phase 1 artifact 为 `citations` 和 `diagnostic_report`。报告只保存 typed facts、evidence gaps、建议、工具步骤、
耗时和引用；`inferences` 必须为空。所有字段经凭据脱敏、长度/数量/JSON 大小边界校验后写入
`chat_messages.artifacts_json`。Phase 1 没有资源创建、更新、删除、Skill publish、MCP 执行或 Knowledge 上传入口。

## Capability Boundaries

Agent Loop 是运行期唯一动态决策者。其他上下文职责固定：

| 上下文 | 职责 | 禁止事项 |
|---|---|---|
| Agent | 推理、选择 Skill、调用工具、状态机、checkpoint | 不在 handler 中实现路由或重试 |
| Skill | 版本化 instruction bundle：capability、activation contract、instructions、requirements | 不执行代码、HTTP、LLM 或 MCP |
| MCP | 外部工具发现和副作用执行 | 不自报可信 risk level，不伪装成 Skill |
| Knowledge | `stratum_search_knowledge` 内部检索能力 | 不执行外部副作用 |
| Memory | 自动会话历史、注入、按需 recall | 不作为通用工具网关 |

依赖方向：`Agent -> Skill snapshot / MCP port / Knowledge port / Memory port`。Skill 不依赖或调用 MCP；Skill requirements 只声明运行期所需的稳定 MCP tool IDs。

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

权限取交集：租户权限 ∩ 用户权限 ∩ Agent allowlist ∩ active Skill requirements。Agent 绑定具体 MCP tool，不绑定整个 server，避免 server 新增工具后自动扩权。

## Skill Activation

- Run 启动时解析允许的 published/candidate Skill revision，并固定 revision ID。
- 模型通过内置 `stratum_activate_skill` 激活一个 instruction bundle。
- 同一时刻只允许一个 active Skill；再次激活会替换前一个。
- 激活后 system messages 注入该 revision instructions。
- Skill 不生成可执行 ToolDefinition，也不经过 CapabilityGateway。
- Agent 可以不激活 Skill，直接使用 Agent allowlist 中的 MCP 工具。

## Tool Execution

`CapabilityGateway` 只负责 LLM completion。外部工具调用通过消费侧 `MCPToolExecutor`，最终到 `ClientManager.CallTool(serverID, rawToolName, args)`。

暴露给模型的名称是 `mcp:<server>:<tool>`；发送给 MCP server 的 name 是原始 tool name。不得混用。

内置工具：

| 工具 | 权限 |
|---|---|
| `stratum_activate_skill` | 当前 Run 的 Skill catalog |
| `stratum_search_knowledge` | Agent workspaces ∩ active Skill workspaces |
| `stratum_recall_memory` | Agent memory scope ∩ active Skill memory scopes |
| `stratum_continue_reasoning` | Agent Loop 内部控制 |

## Context Budget And Compaction

`AgentConfig.MaxContextTokens` 控制 Agent 每次 LLM 请求的上下文上限；未配置时使用 `constants.DefaultAgentContextTokens`（8000）。当前运行行为分两层：

1. 初始上下文由 `BuildContextMessagesWithCompaction` 组装，优先级为当前输入 > system prompt 保底 > memory（剩余预算最多 30%）> 会话历史。窗口外和超出预算的最老历史会交给 `HistoryCompactor` 生成摘要，摘要只注入当次请求的 system message。
2. ReAct 循环（包括 Planning 子步骤的 ReAct）每次调用 LLM 前对消息副本估算 token；达到 `MaxContextTokens * LoopCompactionSafetyRatio`（当前 80%）后，保留 system/user 锚点和最近 3 个完整消息组，较老中间组整体压缩。assistant tool call 与对应 tool result 必须作为原子组保留或删除，禁止产生孤立消息。Reflect、Plan、Synthesize 的结构化单次请求不在本次循环压缩范围内。

生产 wiring 在每次执行解析租户 `CapabilityGateway` 后，用 Agent 配置的 LLM model 创建 `LLMHistoryCompactor`，再注入 `BaseAgent` 和 ReAct/Planning 状态。这样摘要调用沿用当前租户的 provider 与凭据，不会跨租户共享网关。压缩失败、工厂未配置或未注入时必须降级为硬截断/计数标记，不能阻断 Agent Loop；trace 与持久化会话历史保持完整，压缩只影响当次 LLM 请求副本。

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
