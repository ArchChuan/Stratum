# Agent 工具权限管理 Harness 设计

## 目标

为 Stratum 建立一套覆盖工具暴露、运行时授权、人工审批、外部执行、结果治理、审计与恢复的
Agent 工具权限管理 Harness。工作分为两个连续阶段：第一阶段建立可证明的安全不变量、真实链路测试和
CI 门禁，并修复阻断验证的生产缺口；第二阶段在已验证的边界上建设权限管理产品控制面。

本设计优先处理三类威胁：

1. 被直接或间接提示词注入影响的 LLM 试图调用未授权工具或篡改参数；
2. 普通租户用户跨用户、跨 Agent 或跨租户操作工具和审批；
3. 不可信 MCP server 伪造风险、动态增加工具、返回恶意结果，或在超时和重试中产生重复副作用。

## 成功标准

- P0 安全场景成为阻断式 CI，并能定位到稳定的机器可读 reason code；
- 浏览器、HTTP/SSE、Agent Loop、PostgreSQL 与 fake MCP 通过同一 execution/decision ID 串联；
- 未授权工具即使被模型伪造调用，也不能抵达 MCP executor；
- 审批不能扩大基础权限，批准后执行前必须重新授权；
- 并发恢复对单个已批准调用最多发起一次外部执行 attempt；
- destructive 调用超时后进入 `unknown_outcome`，不透明重试；
- 持久化和状态写回失败显式传播，不返回伪成功；
- 日志、trace 和审批摘要不包含凭据或敏感参数明文；
- Harness 退出后不残留服务、测试 schema 或进程。

## 当前仓库事实

当前 Agent 使用精确的 `mcp:<server>:<tool>` allowlist 构造工具目录；active Skill requirements 只能进一步
收窄 MCP 工具。MCP discovery 不能直接决定风险，租户策略将工具分为 `read`、`write_reversible`、
`destructive` 和 `unclassified`，后两类进入审批。审批 payload 使用 AES-GCM 加密，状态通过
`pending -> approved -> executing -> executed` 原子 claim 防止同一审批被并发消费。

已确认的缺口：

- 文档声明的用户级工具权限交集没有独立代码落点；
- 工具目录过滤存在，但缺少 MCP 副作用前统一、不可绕过的 Execution Guard；
- resume 依赖重新运行 Agent 间接重放调用，没有显式重新验证主体、Agent、Skill、策略和参数绑定；
- 审批匹配目前主要比较 server、tool 和 arguments，未形成统一版本化授权决策；
- 策略查询失败降为 `unclassified`，但未保留“未分类”和“存储故障”的不同原因；
- 外部执行超时后会释放为 `approved`，可能在外部已经成功时再次执行；
- checkpoint `MarkCompleted` 错误被忽略。

仓库中的 `internal/platform/harness` 是进程组件生命周期 Harness，不直接承担本设计的授权语义。新的工具权限
Harness 是场景、fixture、fake dependency、跨层断言和 CI 门禁的组合，不应与生命周期 Harness 合并为一个对象。

## 证据与适用边界

### Obsidian 长期知识

- verified 笔记《Agent Harness 是模型之外的任务执行与反馈控制系统》支持把受限工具、执行环境、权限、
  人工控制、真实验证、审计和恢复作为 Harness 的组成，同时强调这不是唯一行业标准分层。
- verified 笔记《Agent 系统应分离执行循环、编排、状态、记忆与治理》支持把权限和恢复从 ReAct loop
  中分离，并明确外部副作用需要 `failed-pending-confirmation` 一类未知结果状态。
- verified Stratum 笔记《来源身份与规范载荷哈希实现重放幂等》支持使用稳定来源身份和版本化规范摘要，
  但明确该模式不自动保证外部不可事务化副作用 exactly-once。
- provisional 的通用分布式系统笔记仅作为检索线索，不作为关键设计证据。

### 官方外部证据

- MCP Tools 规范 2025-06-18：tool annotations 对客户端默认不可信；客户端应为敏感操作提供人工确认、
  展示输入、校验结果、设置超时并记录审计；`tools/list_changed` 说明工具目录可动态变化。
- OWASP LLM01:2025：prompt injection 没有已知的绝对防护，建议最小权限、确定性校验、高风险人工批准、
  外部内容隔离和对抗测试。
- OpenAI Agents SDK：agent input/output guardrail 与逐次 tool guardrail 的执行边界不同；tool input guardrail
  可在批准前检查，并在批准后执行前再次检查；HITL 支持按调用暂停和恢复。

OpenAI SDK 的 run 内 sticky approval 是通用能力，不适合作为 Stratum 第一版语义。Stratum 是多租户、动态
MCP 和外部副作用平台，审批必须绑定本次主体、资源、参数、执行和策略，不能成为持续 bearer capability。

## 核心架构

```text
Tool Catalog
  最小化暴露候选工具
       |
       v
Authorization Decision Point (PDP)
  tenant + user + agent + skill + tool + context
  => deny | allow | require_approval
       |
       v
Execution Guard (最终 PEP)
  重新授权 + schema 校验 + 审批绑定 + 幂等上下文
       |
       v
MCP Executor
       |
       v
Tool Result Guard
  协议/isError/schema/大小/敏感信息/不可信内容
       |
       v
Agent Loop + Audit / Recovery
```

Tool Catalog 负责减少模型看到的攻击面，但不是安全边界。Execution Guard 是所有 MCP 副作用的唯一最终
放行点；即使模型伪造工具名、绕过目录过滤或重放旧调用，执行前仍须通过确定性授权。

Tool Result Guard 在结果进入 LLM 上下文前工作。它验证 MCP 协议错误和 `isError`、可用的 output schema、
载荷大小与截断、敏感信息策略，并把外部内容标记为不可信数据。它不依赖另一个 LLM 判断结果是否安全。

## 授权模型

```text
tenant role / user status
        intersect
Agent exact tool allowlist
        intersect
active pinned Skill requirements
        intersect
tool policy and call context
        => deny | allow | require_approval
```

第一阶段不引入任意表达式 ABAC。第二阶段采用“RBAC 定资格、精确 allowlist 定能力边界、风险与上下文策略
决定是否审批”的分层混合模型。用户级策略只能收窄或 deny，不能例外扩大租户、Agent 或 Skill 边界。

建议标准请求：

```go
type AuthorizationRequest struct {
 TenantID              string
 UserID                string
 UserRole              string
 AgentID               string
 ExecutionID           string
 ToolCallID            string
 ToolID                string
 ArgumentsDigest       string
 ActiveSkillRevision   string
 ObservedPolicyVersion string
}
```

建议标准决策：

```go
type AuthorizationDecision struct {
 ID            string
 Effect        string // deny | allow | require_approval
 ReasonCode    string
 RiskLevel     string
 PolicyVersion string
 Constraints   map[string]any
}
```

`Constraints` 第一阶段只保留明确需要的参数或资源边界，不实现通用策略语言。参数摘要必须使用版本化规范 JSON
计算，不能依赖 map 的非确定顺序或未经定义的字符串格式。

## 授权与审批不变量

1. 基础授权失败一律 `deny`；审批只能解除风险门槛，不能补齐权限。
2. Tool Catalog 和 Execution Guard 必须使用同一决策语义，但只有 Guard 是最终放行点。
3. Skill 激活和用户策略只能保持或收窄权限，不能扩大 Agent allowlist。
4. 缺失 tenant、停用用户、Agent 不可用、Skill revision 解析失败或基础授权依赖失败时 fail closed。
5. 工具未分类可进入审批；策略存储故障也不得自动执行，但必须使用不同 reason code 暴露故障。
6. 审批绑定 tenant、user、agent、execution、tool call、工具、参数摘要、Skill revision 和 policy version。
7. 批准后、执行前重新验证基础权限和当前风险；权限撤销或风险升高必须阻止旧决策直接执行。
8. server 动态新增工具不会自动授权，server 级通配授权不进入第一版。

建议 reason code 至少包括：

```text
tool_not_allowlisted
skill_scope_exceeded
tenant_context_missing
user_permission_denied
policy_lookup_failed
tool_unclassified
approval_required
approval_binding_mismatch
approval_expired
approval_already_consumed
execution_claim_conflict
external_outcome_unknown
```

## 外部副作用与恢复

内部 `approved -> executing` 原子转换只保证单个审批在 Stratum 内被一个执行者 claim，不能证明 MCP server
恰好执行一次。MCP Tools 规范没有通用幂等键字段，因此不得擅自向任意工具参数注入内部字段。只有工具 schema
或受信 server 契约显式声明支持时，Execution Guard 才传播由 `decisionID/executionID/toolCallID` 派生的稳定
幂等键；不支持时必须使用下述 `unknown_outcome` 语义。

对不支持幂等的 destructive 工具，目标是 at-most-once attempt：一旦请求已发出但因超时、断线或取消无法
判断结果，状态转为 `unknown_outcome`，禁止自动回到 `approved` 并透明重试。管理员通过查询外部结果、人工
对账、补偿或显式新调用处理。只有明确未发送、明确失败或有服务端幂等保证时，才允许有限重试。

审批、授权决策和必要审计写入失败时不得开始副作用。副作用完成后的状态写入失败必须向上返回错误并进入可
对账状态，不能把响应伪装为成功。

## 第一阶段 Harness

Harness 使用仓库内置的确定性 fake MCP server，并以 `SecurityScenario` 组织测试：

```text
Given: tenant/user/agent/skill/policy/MCP behavior/approval/prompt
When:  browser or API starts and resumes an execution
Then:  visible tools, decisions, SSE, MCP attempts, DB states and audit agree
```

fake MCP 支持：动态工具列表、伪造 annotations、参数记录、调用计数、可逆与不可逆测试状态、延迟、超时、
断线、半成功、首次失败后成功、恶意 prompt injection 结果、超大结果、敏感内容、协议错误、`isError` 和
output schema 不匹配。所有行为由测试 fixture 显式选择，不依赖随机网络故障。

### 测试分层

1. 纯决策：表驱动和性质测试覆盖交集、默认拒绝、reason code、规范摘要和权限收窄单调性。
2. 状态机：覆盖批准、拒绝、过期、claim、并发 resume、失败、unknown outcome 和非法转换。
3. 基础设施：使用真实 PostgreSQL tenant schema 验证隔离、加密、约束、事务和失败传播。
4. 真实链路：启动真实 HTTP server、Agent Loop、确定性 LLM stub 和 fake MCP，验证 SSE、恢复和审计。
5. 浏览器：只覆盖用户可观察的黄金路径，不穷举完整权限组合。

### P0 阻断场景

- 模型伪造未授权工具调用，MCP 调用计数保持零；
- Agent、Skill 或用户权限撤销后，已批准调用不能 resume；
- 跨租户读取、决策和恢复相同 approval ID 全部失败；
- 相同 approval 并发 resume，外部执行 attempt 最多一次；
- tenant、user、agent、execution、tool、arguments、Skill 或 policy 任一变化导致绑定失效；
- destructive 调用超时进入 `unknown_outcome`，不自动重试；
- 授权、审批、checkpoint 或审计写入失败显式向上传播；
- MCP 新增工具或伪造 annotations 不会自动扩权；
- 批准前校验通过但批准后权限变化，执行前复核必须拒绝；
- 恶意、超大、schema 不匹配或含敏感信息的结果不能原样进入 LLM、日志或 trace。

### 浏览器黄金路径

1. read 工具正常完成；
2. destructive 工具暂停并展示实际调用摘要；
3. 管理员批准后恢复，fake MCP 记录一次 attempt；
4. 管理员拒绝后不能恢复；
5. 审批过期、权限撤销或 unknown outcome 显示明确状态；
6. member 无法管理审批，且跨租户记录不可见。

### CI 分级

- PR 必跑：纯决策、状态机、PostgreSQL/API 集成和风险 guardrails；
- Agent、MCP、Skill、IAM、审批或执行改动：增加真实 Agent Loop + fake MCP；
- 发布前：浏览器 E2E、`go test -race`、失败注入和 secret scan。

## 第二阶段产品控制面

### Agent 工具授权

管理员精确选择 Tool ID，并看到 server、描述、当前风险和数据影响。server 新增工具默认未选中。禁止
server 级通配授权。

### 工具策略

支持风险、是否审批、有限参数约束和资源范围。每次变更生成不可变 policy version。第一版不提供任意表达式
语言，不让 LLM 判定风险，也不支持用户例外扩大权限。

### 审批与审计

审批界面展示工具、调用目的摘要、结构化参数 diff、风险、影响范围和策略版本；敏感值脱敏。审计能回答谁、
以哪个 Agent、在哪个租户、依据哪版策略、对哪个资源执行了什么，以及结果是否确定。

第一版不提供“本会话后续全部允许”、长期审批 bearer capability、Skill 扩权或对所有工具透明重试。

## 面试讨论框架

### 基础权限

- Authentication、authorization 和 approval 的责任分别是什么？
- RBAC 为什么不足以表达工具参数、资源范围和运行上下文？
- PDP 与 PEP 如何分工，为什么 Catalog 过滤不能替代 Execution Guard？

### Agent 安全

- 为什么 LLM 不是授权主体，模型产生的 tool call 只能视为候选动作？
- 如何应对 direct/indirect prompt injection、tool poisoning 和 confused deputy？
- Skill activation 如何实现 capability attenuation，并用单调性测试证明不会扩权？

### 分布式可靠性

- 审批等待期间策略变化如何避免 TOCTOU？
- 数据库事务为什么无法保证外部工具 exactly-once？
- 超时后为什么需要 `unknown_outcome`，何时才允许重试？
- 幂等键、at-most-once attempt、补偿和人工对账如何组合？

### 测试与工程

- 为什么要同时使用性质测试、模型化状态机测试、真实数据库和浏览器 E2E？
- fake MCP 应如何构造可重复的恶意和部分失败场景？
- 如何让安全门禁阻断真实回归，同时控制 E2E 的速度和脆弱性？

推荐的核心回答是：LLM 只产生动作建议；Catalog 减少暴露，最终 PEP 位于副作用执行前；PDP 根据租户、
用户、Agent、Skill、工具和上下文生成版本化决策；审批不能扩大基础权限；恢复时重新授权并校验参数摘要；
外部超时不能假定失败，因此 destructive 调用进入 unknown outcome，依赖幂等、对账或补偿而非透明重试。

## 实施边界

第一阶段允许为通过 P0 不变量而最小修改生产代码，包括显式重新授权、统一 Execution Guard、Tool Result
Guard、`unknown_outcome` 和持久化失败传播。不在第一阶段建设通用 ABAC、完整管理 UI 或任意策略语言。

实现前需使用 writing-plans 进一步确定包边界、port 变更、tenant DDL、API 契约、fake MCP 进程模型和每个
测试的具体文件位置；任何数据库变更必须覆盖历史 tenant schema、事务失败和跨租户验证。

## 官方来源

- Model Context Protocol, Tools specification 2025-06-18:
  <https://modelcontextprotocol.io/specification/2025-06-18/server/tools>
- OWASP, LLM01:2025 Prompt Injection:
  <https://genai.owasp.org/llmrisk/llm01-prompt-injection/>
- OpenAI Agents SDK, Guardrails:
  <https://openai.github.io/openai-agents-python/guardrails/>
- OpenAI Agents SDK, Human-in-the-loop:
  <https://openai.github.io/openai-agents-python/human_in_the_loop/>
