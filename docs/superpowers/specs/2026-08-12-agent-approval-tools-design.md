# 系统助手工具扩展与平台级审批体系

日期:2026-08-12
状态:已批准(设计经多轮澄清定稿;最终约束:admin/owner 免审批、member 走审批、工具可见性不裁剪)

## Context

系统助手(GovernedAssistant,`stratum.platform_assistant`)当前内置工具硬编码在 `dispatchToolCall`(internal/agent/application/graph/react_tool.go:141),只能诊断 `model_configured` 事实,不能读取可配置模型清单;审批流(ToolApproval)已完整搭建(`agent_tool_approvals` 表 + AES-256 payload + digest 绑定 + 30min TTL + checkpoint 断点恢复),但仅覆盖 MCP 工具 write_reversible 通道。

本次统一补齐:

1. **系统助手模型工具**:只读模型清单 + 写工具更新系统助手模型(diagnose 区同步扩展)。
2. **独立审批工作台**:admin/owner 可见的独立页面(待审批 + 历史 + 参数详情解密)。
3. **审批覆盖扩展**:评测写操作、MCP 工具策略/服务器配置、资源变更——全部纳入统一审批语义。
4. **角色语义**:admin/owner 免审批直接执行 + audit;member 的写路径一律走审批(或 fail closed)。工具可见性不随角色裁剪,权限在执行时校验。
5. **断点恢复 + 层层校验**:审批通过后恢复执行前逐项校验(TTL/策略/目标/会话),失效显式终态,产品语义自洽。

## 设计决策

### D1. 系统助手模型工具

- 新只读工具 `stratum_list_models`(全角色):返回全量模型清单(含 embedding/disabled/providerManaged,标注 enabled + capabilities)。
- 新写工具 `stratum_update_system_model`(全角色**可见**,执行时校验角色):复用 `UpdateSystemAssistantModel`(agent_service.go:547)+ audit。
- **可见性不裁剪**:`SystemAssistantToolDefinitionsForRole` 移除角色裁剪逻辑,全量定义下发;写工具执行时解析发起人角色,member → 明确拒绝(fail closed,错误提示"需要管理员权限"),admin/owner → 执行。与 MCP 工具通道行为一致。
- 角色来源:`resolveSystemAssistantTooling`(agent_service.go:2079)→ `AuthorizeDiagnosticRequest` → `ResolveTenantRole`(DB 查询成员角色)→ `boundedAssistantRoleClass`;guard 触发审批/校验时透传该角色。
- diagnose model 区扩展:清单统计(总数/enabled/disabled/chat/embedding)+ 当前模型有效性(Ready 语义:存在 + enabled + chat capability)。

### D2. 权限矩阵(v5,最终)

| 操作 | 可见性 | member | admin/owner |
|---|---|---|---|
| 全部内置工具定义(含写工具) | 全角色可见 | 只读可执行;写工具拒绝或走审批 | 全部可执行 |
| `stratum_list_models` | ✓ | ✓ | ✓ |
| `stratum_update_system_model` | ✓ | 拒绝(fail closed) | 直接执行 |
| 资源变更 propose_resource_change | ✓ | 提案审批(现有流) | 直接执行(自动确认+apply) |
| MCP 工具 read | ✓ | ✓ | ✓ |
| MCP 工具 write_reversible | ✓ | 发起→审批 | 直接执行 |
| MCP 工具 destructive | ✓ | 拒绝 | 拒绝(L3b 底线,不豁免) |
| MCP 工具策略/服务器配置 | ✓ | 发起→审批 | 直接执行 |
| 评测写操作 | ✓ | 发起→审批 | 直接执行 |
| self-modify(既有通道) | 现状 | 提案制(已有) | 现状 |
| 审批列表 | — | 仅自己的 | 全部 |
| 审批历史/详情 | — | ✗ | ✓ |
| decide/resume | — | ✗ | ✓(可指定审批人,禁自我) |

### D3. 审批流泛化(subject_kind)

`agent_tool_approvals` 表加列 `subject_kind`(mcp_tool/evaluation_action/mcp_policy/mcp_server)+ 泛化 payload(migrate 后 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`,历史租户安全):

- `ToolApprovalService.Request` 按 subject 构造绑定:digest 集合按 subject 裁剪(工具:arguments/skill/mcp/knowledge revisions + policy_version;配置/评测:参数 payload digest + policy_version)。
- `ExecuteApproved` 按 subject 分发执行器:现有 MCP 工具执行保持;新增 evaluation 执行器(调用评测 service 原操作)、mcp_policy/mcp_server 执行器(调用 MCPService 原方法)。
- 角色分流在 guard/handler 层:admin/owner → 直接执行原操作 + audit;member → 创建审批(pending)。
- 自创建检查:创建审批时校验 assignee 存在性 + 角色(admin/owner),不合法 → fail closed 拒绝创建。
- 审批人校验:`Decide` 增加 actor≠发起人校验(防御 member 被提升后审批自己历史请求)+ assigned_approver 匹配(未指定则任意 admin/owner)。

### D4. 评测审批

评测写操作(router.go:159-185 全量 requireAdmin 路由)改造为角色分流:

- 覆盖操作:create suite / publish suite / generate cases / enqueue run / create experiment / pause / promote / rollback / reject candidate / create baseline / generate optimization。
- member 调用 → 创建审批(subject=evaluation_action,payload=原请求参数解密可审)→ pending;admin/owner 调用 → 直接执行(现状不变)。
- 审批通过后执行 = 直接执行原操作(同步小操作,无 checkpoint 恢复——执行与审批解耦,审批仅是前置门)。
- handler 需获取 tenant 角色(现有 ResolveTenantRole 链路),写操作从 requireAdmin 放宽为 requireActive + 角色分流(保留租户上下文)。

### D5. MCP 权限设置审批

- `SetToolPolicy`(PUT /tool-policies/:serverId/:toolName)、ConnectServer、UpdateServer、SetMCPServerEditors、DeleteServerConfig:member → 审批(subject=mcp_policy/mcp_server);admin/owner → 直接执行。
- 堵漏:member 通过降级风险等级绕过工具审批的路径消失(策略变更本身需要审批)。
- 执行器:审批通过后调用 MCPService 原方法,与直接执行同一代码路径。

### D6. 资源变更整合

- 现状:`resource_change_proposals` 提案流完整(ready_for_review→confirm→apply,24h TTL,baseline fingerprint,审计事件,`FOR UPDATE` 单次 claim);但 propose/apply 工具仅 admin/owner 可见。
- 工具全角色可见后:member 调用 `propose_resource_change` → 现有提案流不变(等待 admin/owner confirm);admin/owner 调用 → **自动确认+apply 一气呵成**(confirmer_id=操作者,完整复用状态机/基线校验/审计,无等待暂停)。
- self-modify 通道(GatedSelfModify,operation proposal)保持现状(已是 member 提案制)。

### D7. 独立审批工作台

- 新页面(admin/owner 可见,路由守卫 + 菜单按角色渲染):`/approvals`。
- Tab1 待审批(全部 pending,含 member 发起 + 指定给我);Tab2 历史(decided/executed/expired/invalidated/voided/cancelled,分页)。
- 详情抽屉:解密 payload 展示参数(服务端解密后下发,遵循不输出密钥/凭据规则——digest 绑定内容经 `buildOperationPayloadSummary` 同类脱敏)。
- 审批人:decide(approve/reject + reason);admin/owner 可指定审批人时展示 assigned_approver;owner 兜底可见全部。
- 聊天页 ApprovalGate 保留,member 只看到自己发起的(后端 ListPending 按 user_id 过滤 member)。

### D8. 指定审批人(软绑定)

- 发起时可选指定(会话 UI 选择,展示可用的 admin/owner 列表);创建时校验存在性 + 角色,fail closed。
- **软绑定**:assignee 仅优先级提示(通知/排序),全部 admin/owner 仍可见可处理——审批人不处理不阻塞(业界 Escalation Policy 语义),避免无人处理死锁。

### D9. 状态机扩展(失效终态)

现有 `ValidateToolApprovalTransition` 扩展终态:

- `pending → cancelled`(会话删除级联 / 发起人撤销)。
- `approved → voided`(执行上下文销毁:会话删除;授权已发但捕获失败——Stripe 授权-捕获语义)。
- `approved/executing → invalidated`(审批语义失效:策略变更、目标消失、绑定 mismatch——执行时重验不通过)。
- 终态不可再 decide/resume(单次消费,幂等)。
- 会话删除(用户显式删除)级联:事务内将关联 pending → cancelled、approved → voided,原因 `conversation_deleted`,审计事件落库;物理删除保留审批历史(可对账)。

### D10. 层层校验(核心原则)

执行路径每层校验,失败显式暴露、语义自洽:

1. **发起层**:角色校验 → 直接执行 / 拒绝(明确错误) / 创建审批;指定审批人存在性校验。
2. **审批层**:CAS 单次决定、禁自我、assignee 匹配、digest 绑定校验(`toolApprovalBindingMismatches`)。
3. **恢复层**(checkpoint 恢复执行前):TTL → 策略重查(policy_version + 当前 risk level)→ 目标存在性(server/tool/agent)→ 会话存在性 → digest 重验;任一失败 → 分类错误(`ErrApprovalExpired`/`ErrApprovalPolicyChanged`/`ErrApprovalTargetGone`/`ErrApprovalConversationGone`)→ 审批终态 invalidated/voided + 原因。
4. **执行层**:CAS 单次消费(Decide/ClaimExecution 已有 `WHERE status=... AND expires_at>NOW()`,保持)、状态机非法转移拒绝(CanTransition/ValidateToolApprovalTransition)。
5. **产品层**:每类失败映射明确用户可见消息(中文)+ 审批工作台历史终态 + 原因,人工可对账。

### D11. 并发与一致性(业界标准映射)

| 竞态场景 | 业界标准 | 落地 |
|---|---|---|
| 双 decide / 双 resume | CAS 条件更新 | 已有(`WHERE status='pending'`),resume 路径同构 |
| 审批单次消费 | single-use claim(OAuth code 语义) | ClaimExecution `approved→executing` 原子,重复返回原结果 |
| 重复发起同操作 | Idempotency Key | 已有(`ON CONFLICT(execution_id,tool_call_id)`、proposal duplicate_pending) |
| 断点恢复双执行 | Lease + Idempotent Executor | 已有 `proposalApplyingRecoveryLease=5min` 先例;恢复执行器幂等 |
| 审批期间策略变更 | 执行时授权重验证(NIST AC/OWASP A01) | 恢复层重查 policy,fail closed |
| 指定审批人失效 | Escalation Policy | 软绑定 + owner 兜底 |
| 同资源并发 propose | 乐观并发控制(fingerprint) | baseline fingerprint 已有 |
| apply 双 claim | SELECT FOR UPDATE + 状态条件 | 已有 |

### D12. API 与前端变更

后端:

- `agent_tool_approvals` 加列 subject_kind/assigned_approver/失效原因字段(ALTER TABLE IF NOT EXISTS)。
- `ListPending`:member 按 user_id 过滤;admin/owner 全量(未指定 + assigned 给自己优先)。
- 新增历史查询 API:`GET /agents/tool-approvals?status=history&page=...`(分页,admin/owner)。
- 新路由 `/agents/tool-approvals/:id` GET 详情(解密 payload,admin/owner)。
- 评测/MCP 写路由角色分流(member → 审批,pending 返回;admin/owner → 现状直接执行)。
- 会话删除 service:级联失效关联审批(事务内)。

前端:

- 新页面 `web/src/modules/approvals/`(待审批 + 历史 + 详情抽屉),路由守卫 admin/owner,菜单按角色渲染。
- 发起审批时指定审批人 UI(会话侧,可选,展示 admin/owner 列表)。
- 聊天页 ApprovalGate 保持,member 仅见自己的。
- 错误消息映射:失效分类 → 中文可解释文案。

## 测试计划

- **模型工具**:list_models 全角色可调(内容正确含 disabled/embedding);update_system_model member 拒绝/admin 成功(含 audit);可见性全角色一致。
- **审批泛化**:subject_kind 各类型 Request/绑定/执行器分发;payload 解密展示脱敏。
- **角色分流**:评测/MCP 配置 member→pending、admin/owner→直接执行;指定审批人创建校验(不存在/角色不符 → 失败)。
- **自我审批**:Decide actor==发起人 → 拒绝;member 提升为 admin 后审批自己历史请求 → 拒绝。
- **可见性**:ListPending member 只见自己的;admin/owner 全量。
- **状态机**:失效终态全转移合法/非法表;终态不可 decide/resume(幂等)。
- **层层校验**:恢复层各失效分类(过期/策略变更/目标消失/会话删除)→ 分类错误 + 终态 + 原因落库。
- **会话删除级联**:pending→cancelled、approved→voided,审计事件,删除事务回滚时级联回滚。
- **并发**:双 decide 第二个 ErrAlreadyDecided;双 resume 幂等;双 claim 单次成功。
- **E2E**(stratum-e2e-development):member 触发 write_reversible → 审批工作台 admin 批准 → 断点恢复执行 → 资源变更生效;member 发起评测 → admin 批准 → run 入队。

## 风险

- 评测写路由放宽为角色分流后,member 路径的 fail closed 必须经测试兜底(角色解析失败 → 拒绝,不默认放行)。
- `agent_tool_approvals` 加列走 tenant DDL 规则(IF NOT EXISTS,历史租户兼容)。
- 审批通过后执行与直接执行等价性:执行器必须复用同一 service 方法,禁止平行实现。
- prompt/profile:写工具对 member 可见后,profile 需说明"无权限调用会得到明确拒绝"。

## 不做

- 不做审批邮件/站外通知(仅站内工作台 + 会话卡片)。
- 不做审批 SLA 强制超时自动动作(仅软绑定 + 过期 TTL)。
- 不改 L3b destructive 底线(任何角色均拒绝)。
- 不重做 self-modify/operation proposal 通道(保持现状)。
- 审批通过后的评测操作不做 checkpoint 断点恢复(同步小操作,执行即完成)。
