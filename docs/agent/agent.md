# Agent Development Rules

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

1. 初始上下文由 `BuildContextMessages` 组装，优先级为当前输入 > system prompt 保底 > memory（剩余预算最多 30%）> 会话历史。历史先按窗口截取，再从最老消息开始丢弃以满足预算。
2. ReAct 循环（包括 Planning 子步骤的 ReAct）每次调用 LLM 前对消息副本估算 token；达到 `MaxContextTokens * LoopCompactionSafetyRatio`（当前 80%）后，保留 system/user 锚点和最近 3 个完整消息组，较老中间组整体淘汰并插入省略标记。assistant tool call 与对应 tool result 必须作为原子组保留或删除，禁止产生孤立消息。Reflect、Plan、Synthesize 的结构化单次请求不在本次循环压缩范围内。

`HistoryCompactor` port 和 `LLMHistoryCompactor` 基础实现已经存在，但当前 wiring 未向 `BaseAgent` 或 `ReActState` 注入 compactor，因此生产路径不会调用 LLM 生成摘要。压缩失败或未注入时必须降级为硬截断/计数标记，不能阻断 Agent Loop；trace 与持久化会话历史保持完整，压缩只影响当次 LLM 请求副本。

## MCP Risk And Approval

租户管理员为每个 `(server_id, tool_name)` 设置风险：`read`、`write_reversible`、`destructive`、`unclassified`。未配置或读取失败必须视为 `unclassified`。MCP discovery payload 不能设置受信风险。

- `read` / `write_reversible`：直接执行。
- `destructive` / `unclassified`：Run 进入 `waiting_approval`，工具不得执行。
- 参数、query、固定 Skill revisions 使用 AES-256-GCM 存入 `agent_tool_approvals.encrypted_payload`。
- checkpoint 只保存 approval ID，不保存原始参数。
- 批准后使用同 execution ID 恢复；仅完全匹配 server/tool/arguments 的调用可 bypass 一次。
- 执行前原子抢占 `approved -> executing`；失败回退 `approved`，成功转 `executed`，防止重复副作用。

## Rules

1. 路由、审批、重试、checkpoint 和权限交集必须硬编码，不交给模型决定。
2. MCP 工具默认 `unclassified`，禁止 fail-open。
3. Skill revision 在 Run 内不可漂移；审批恢复使用 payload 固定的 revision。
4. Tool trace 和 Agent trace 是不可变历史；删除旧 Skill 存储时必须保留。
5. `MaxIterations` 和 execution timeout 必须有限。
6. 上下文裁剪必须保持 tool call/tool result 配对，并保留 system/user 锚点；不得直接修改持久化历史或 trace。
7. 不记录 token、API key、password、审批明文 payload 或敏感原始响应。
8. Agent/Skill/MCP/Knowledge/Memory 改动必须完成真实 API、数据库、Agent Loop 和浏览器 E2E。
