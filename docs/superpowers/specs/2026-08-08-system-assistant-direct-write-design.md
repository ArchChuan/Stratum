# C: 系统助手对话内直写资源修改(update + create)

日期:2026-08-08
状态:已批准(方案 1:复用 apply 编排;归属语义:继承发起者权限)

## Context

A+B 已完成并合并(#282):四类资源(Agent / Skill / MCP / Knowledge)ownership 隔离(admin 限己、owner 全权)+ 统一语义审计(`resource_change_audits`,与业务同事务原子)。

当前系统助手对租户资源的**唯一**修改通道是 proposal 流程(用户审批后 apply,审计 source=proposal_apply)。C 阶段新增第二通道:**系统助手在平台助手对话中直接修改资源**(绕过人工审批),边界为 update + create(用户已确认),同事务落审计(source=system_assistant)。

## 设计决策

### C1. 新工具 `stratum_apply_resource_change`

- 定义位置:`SystemAssistantToolDefinitionsForRole`(internal/agent/application/system_assistant_tools.go:49)——admin/owner 追加,member 不可见(与 `stratum_propose_resource_change` 并列)。
- **InputSchema 复用 `proposalToolSchema()`**(oneOf 分支:4 kinds × create/update)。其 oneOf 生成循环只遍历 `OperationCreate`/`OperationUpdate`,**delete 在 schema 层不存在**——直写天然无删除能力。
- 参数解析复用 `parseProposalArguments`(白名单字段 + 类型断言 + `Valid()` 校验,内部 agent/application/system_assistant_tools.go:164)。
- 与 propose 工具的差异仅语义:propose = 创建待审提案;apply = 立即落地。LLM 学习成本为零。
- 工具描述明确:直接落地、无需审批、需先确认用户意图;仍禁止 delete/凭据变更/IAM/发布。

### C2. 执行编排复用(核心)

`ResourceChangeProposalAdapters.applyAgent/applySkill/applyMCP/applyKnowledge`(api/wiring/resource_change_proposal.go:184-330)已封装全部写逻辑。重构为**不依赖 proposal 对象**的公共编排:

- 四个方法签名重构为 `apply*Change(ctx, tenantID, resourceID, operation, change, actorID)`(去掉 proposal 参数)。
- proposal 路径:包装调用,行为不变(`proposalApplyContext` 打 `WithTenantID` + `WithChangeSource(proposal_apply, proposal.ID)`,actorID=ConfirmerID fallback ProposerID)。
- 直写路径:新方法 `ApplyDirect(ctx, tenantID, actorID, kind, operation, resourceID, payload) (ApplyResult, error)`:
  1. `DecodeProposalPayload` 严格解码(DisallowUnknownFields);
  2. `ctx = WithTenantID(tenantID) + WithChangeSource(system_assistant, "")`;
  3. 按 kind 分发到 `apply*Change`,`actorID = 对话发起者`(req.UserID)。
- **update 的 `BaselineFingerprint`**:直写无 baseline,`UpdateDraftBundle` 的 fingerprint 传空串(直写不做并发基线控制,与 API 直连 Update 语义一致;proposal 路径仍传 proposal.BaselineFingerprint)。

### C3. 工具调用执行(ReAct loop)

- `ExecutionConfig` 新字段 `ResourceChangeApplyFn func(context.Context, map[string]any) (domain.SystemAssistantDirectApplyArtifact, error)`;新 option `withResourceChangeApplyFn`(internal/agent/application/agent.go,仿 `withProposalCreateFn`)。
- `systemAssistantExecutionOptions`(agent_service.go:1640):`roleClass admin/owner && deps.DirectApplyFn != nil` 时注入——fn 内部解析参数 → `ApplyDirect` → 组装 artifact。AgentServiceDeps 新字段 + `SetResourceChangeApplier` Set 方法,wiring 注入。
- `dispatchToolCall`(graph/react.go:591-613)新 case → `execApplyResourceChangeTool`(仿 `execProposeResourceChangeTool` :673-699):`!s.GovernedAssistant || s.ResourceChangeApplyFn == nil` → fail closed 工具错误;guard `withInternalToolResultGuard` 校验结果;artifact 记录 Tool/Outcome/ErrorCode。
- 新 domain 类型 `SystemAssistantDirectApplyArtifact{ResourceKind, Operation, ResourceID, Outcome, ErrorCode}`(直写无 proposal,不复用 proposal artifact)。

### C4. 归属与审计

- **不使用 `WithSystemActor`**:直写 `input.ActorID = 对话发起者`,service 层正常走 B 的归属矩阵(admin 限己、owner 全权)。系统助手是 admin/owner 的执行工具,不是隔离豁免者——否则 admin 可借系统助手之手修改他人资源,成为 A 隔离的后门。
- 审计:`actor_id=发起者`、`actor_type=user`(校验主体=审计主体一致)、`source=system_assistant`。B 的 `newChangeAudit` 从 ctx 读 `ChangeSourceFromContext`,同事务原子自动生效,各 repo 零改动。
- audit domain 新增常量 `ChangeSourceSystemAssistant = "system_assistant"`。

### C5. prompt / profile 更新

- 新 profile 版本 `2026-08-08.v3`(现行为 `2026-08-04.v2`),`BuiltinSystemAssistantProfiles` 保留历史版本。
- 修改现状禁令 "never modify tenant resources outside the proposal workflow":允许通过 `stratum_apply_resource_change` 直写(update/create);delete、凭据变更、IAM 操作、publishing 仍禁止。
- 新增工具使用指引:直写前必须从对话确认用户意图;能复用的配置字段(如 MCP 存储凭据)不得重写。

### C6. 边界(继承 proposal 语义,零新逻辑)

| 边界 | 机制 |
|---|---|
| 无 delete | ProposalOperation 仅 create/update,schema 无 delete 分支 |
| MCP 凭据 | create 强制 Auth=None/Env={}/Headers={};update 保留存储凭据(applyMCP 既有逻辑) |
| Agent 平台保护 | update 命中 SystemKey → `ErrSystemAssistantManaged`(applyAgent 既有逻辑) |
| Skill draft-only | update 走 `UpdateDraftBundle`、create 走 `CreateSkillDraft` |
| IAM/发布 | 工具 schema 不含此类资源 |

## 测试计划

- **工具定义**:admin/owner 暴露 `stratum_apply_resource_change` 且 schema=proposalToolSchema;member 不暴露。
- **解析**:`parseProposalArguments` 复用(白名单字段、delete 拒绝)。
- **编排**:`ApplyDirect` 4 kinds × create/update 全格;DecodeProposalPayload 严格性;MCP 凭据保护;agent SystemKey 保护。
- **归属**:admin 直写他人资源 → 工具错误(403 语义);owner 直写任意 → 成功;member 无工具。
- **审计**:直写后 `resource_change_audits` 行 source=system_assistant、actor_id=发起者、actor_type=user,与业务同事务(写失败回滚)。
- **react**:dispatch 路由、fn nil fail closed、artifact 字段。
- **profile**:新版本 prompt 含直写指引、禁令收敛。
- **E2E**(stratum-e2e-development):平台助手对话"帮我修改 X"→ tool call 出现 → 读回修改生效 → DB `resource_change_audits` 出现 source=system_assistant 行。

## 风险

- `UpdateDraftBundle` 空 fingerprint 行为需验证(直写传空串,proposal 路径不变)。
- prompt 语义遵守(直写 vs 提议):工具描述 + profile 指引收敛,行为由测试兜底。
- 编排重构(apply* 签名变化)影响 proposal 路径:行为不变,proposal 相关测试全量回归。

## 不做

- 系统助手 delete/凭据/IAM/发布(用户确认边界之外)。
- 直写并发基线控制(无 proposal 的 baseline 概念)。
- 前端改动(平台助手对话 UI 无需变化)。
