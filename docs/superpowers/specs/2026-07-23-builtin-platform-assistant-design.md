# 内置平台使用助手设计

**状态：已确认，待实施计划**

## 1. 背景与问题

Stratum 需要为每个租户提供一个平台内置助手，统一承担以下职责：

1. 基于版本化官方资料回答平台使用问题。
2. 在当前租户边界内诊断 Agent、Skill、MCP 和 Knowledge 的应用级状态。
3. 在管理员确认后，帮助创建或更新上述四类资源。

2026-07-15 的初稿因 Skill 自进化和跨资源变更基础尚未稳定而暂缓。当前 Skill 已具备
`draft/candidate/published` 版本语义，Agent 工具权限也已有确定性审批与恢复链路，但 Agent、MCP 和
Knowledge 仍没有统一资源版本模型。因此本设计保留完整产品目标，分两期交付，避免一期同时引入跨资源写入。

## 2. 已确认决策

| 主题 | 决策 |
|---|---|
| 产品范围 | 使用指导、官方知识检索、租户状态诊断、受控资源创建/更新 |
| 交付方式 | 一期只读指导与诊断；二期受控创建/更新 |
| 系统归属 | 平台核心 Profile + 租户托管实例 |
| 角色边界 | 所有成员可使用；普通成员只见本人相关证据，管理员可见租户级脱敏证据 |
| 诊断范围 | 仅当前租户应用层，不含主机、Kubernetes、全局日志或其他租户 |
| 官方回答 | 严格证据模式；无官方依据时明确不知道 |
| 模型 | 使用租户选择的模型；无模型时提供静态引导 |
| 一期租户扩展 | 只允许管理员选择模型 |
| 前端入口 | 现有 Agent 对话页固定置顶，显示“系统内置” |
| 诊断输出 | 简短结论和下一步 + 可展开结构化报告 |
| 二期资源 | Agent、Skill、MCP、Knowledge |
| 二期操作 | 只允许创建/更新普通配置；保守白名单 |
| 确认体验 | 对话预览卡片 -> 专用审阅页 -> 管理员确认应用 |
| 验收重点 | 功能、安全、可靠性和效率基线同时覆盖 |

## 3. 目标与非目标

### 3.1 目标

- 每个新旧租户都拥有且只拥有一个系统助手实例。
- 平台可以统一升级核心提示词、官方知识、诊断工具和安全规则，不产生租户配置漂移。
- 普通成员和管理员获得符合其角色的数据视图；任何权限读取失败都 fail closed。
- 官方回答可引用、可追溯；诊断结果区分事实、推断和缺失证据。
- 二期所有写入都经过类型化提案、管理员确认、冲突检查、确定性应用和审计。
- 会话、执行、Profile 版本、诊断证据和资源变更均可追踪。

### 3.2 非目标

- 不做持续监测、主动巡检或自动优化建议。
- 不读取宿主机、Kubernetes、平台全局基础设施日志或其他租户数据。
- 不使用平台共享模型或共享凭据兜底。
- 一期不开放租户提示词、Skill、MCP 或 Knowledge 扩展。
- 二期不删除资源、不发布 Skill、不部署优化候选、不执行 MCP 工具、不上传 Knowledge 文档。
- 对话、提案、trace 和通用日志不得采集、保存或回显密钥。
- 不让模型决定授权、字段可见性、审批、状态流转、重试、冲突处理或回滚。

## 4. 总体架构

采用“租户托管实例 + 平台运行时 Profile”。

```text
Platform System Assistant Profile
  - core prompt and response rules
  - official documentation provider
  - read-only diagnostic tool catalog
  - role-aware evidence policies
                |
                | runtime deterministic composition
                v
Tenant System Assistant Instance
  - stable agent ID and system key
  - tenant-selected LLM model
  - conversations, executions and traces
```

系统助手仍复用现有 ReAct Agent loop、会话、SSE、执行记录和 trace。Profile 不是新的 Agent 算法，
也不是普通租户可编辑 Agent。运行时根据受管 `system_key` 解析 Profile，并与租户实例允许覆盖的模型配置合成。

模型只负责理解自然语言、选择已授权的只读诊断能力以及组织回答。身份、租户、角色、工具目录、证据裁剪、
引用结构、执行预算和失败语义由 Harness 中的确定性代码控制。

## 5. 系统身份与生命周期

### 5.1 租户实例

租户 `agents` 表增加可空且唯一的 `system_key`。普通 Agent 为 `NULL`；平台助手使用固定系统键。
现有 `llm_model` 保存租户管理员选择的模型。核心提示词和系统工具不复制进租户记录。

对外 DTO 返回 `is_system` 和 `management_mode`，但无需暴露内部系统键。普通 Agent CRUD 必须拒绝：

- 删除系统助手；
- 修改名称、系统身份或核心提示词；
- 绑定租户 Skill、MCP、Knowledge 或追加提示词；
- 将系统助手转换为普通 Agent。

管理员只通过专用设置用例修改模型。

### 5.2 创建、补齐与升级

- 新租户 provision 在激活前幂等创建唯一系统助手实例。实例创建失败时租户不得激活。
- 历史租户通过显式升级/补齐任务处理；任务只补缺失实例，不覆盖已选择模型。
- 普通请求路径不得隐式创建实例或执行数据修复。
- Profile 由平台统一发布。新执行解析当前 Profile；历史执行记录实际使用的 `profile_key/profile_version`。
- Profile 发布必须保留可回滚版本，回滚不修改租户实例或历史执行记录。
- 缺失或损坏实例由显式运维任务幂等修复，并记录审计结果。

## 6. 一期：使用指导与只读诊断

### 6.1 请求链

```text
JWT tenant/user/role
  -> system assistant guard
  -> deterministic capability and evidence policy
  -> official docs provider and/or tenant diagnostic ports
  -> model synthesis with bounded evidence
  -> summary + structured diagnostic report
```

tenant、user 和 role 只能来自已验证请求上下文。用户正文或模型生成的 tenant ID 不参与授权。

### 6.2 官方资料

官方资料使用平台维护的独立、只读、版本化索引，不复用租户 Knowledge workspace。只有平台文档发布流程可以
写入该索引。每条检索证据至少包含：

- 文档 ID 和标题；
- 适用产品版本；
- 章节或锚点；
- 稳定链接；
- 受长度限制的引用片段。

回答必须引用实际命中的证据。没有证据时返回明确的知识缺口，不允许模型用通用知识冒充官方结论。

### 6.3 诊断范围

一期诊断工具全部只读，并按消费方 port 定义最小 DTO。跨 context adapter 在 wiring 中装配，不允许 Agent
application 直接 import 兄弟 context 的 application 或 infrastructure。

| 领域 | 管理员可见证据 | 普通成员可见证据 |
|---|---|---|
| 租户模型 | 配置完整性、可用状态、脱敏错误 | 是否可使用；缺失时联系管理员 |
| Agent | 租户级执行状态、trace 摘要、脱敏错误 | 本人相关会话和执行状态 |
| Skill | 草稿、候选、发布和评测状态 | 本人可使用 Skill 的公开状态 |
| MCP | 连接状态、工具目录、策略状态、脱敏错误 | 本人执行涉及工具的允许状态 |
| Knowledge | workspace 配置、摄取进度和脱敏错误 | 本人有权使用 workspace 的公开状态 |

不得返回原始日志、原始上游响应、密钥、Token、其他用户私有内容或平台全局拓扑。

### 6.4 输出契约

用户首先看到简短结论和下一步。可展开诊断报告至少包含：

- 已确认事实及其来源、对象和时间；
- 有证据支持的推断及置信边界；
- 缺失或失败的证据；
- 建议操作，但一期不提供自动执行；
- 官方引用；
- 诊断工具名称、耗时和脱敏状态摘要。

结构化报告作为执行工件返回和持久化，不只嵌入 Markdown 文本。

### 6.5 无模型与失败降级

- 未配置可用模型：助手仍可见；管理员看到模型配置入口，成员看到联系管理员提示。
- 单个诊断工具失败：标记该证据不可用，不把缺失解释为健康。
- 租户、用户、角色或权限读取失败：不返回对应数据。
- 官方资料未命中：明确说明没有官方依据，并记录知识缺口。
- 模型失败：暴露执行失败；已获取事实不得被包装成伪造结论。

## 7. 二期：受控资源创建与更新

### 7.1 允许操作

| 资源 | 允许 | 禁止 |
|---|---|---|
| Agent | 创建、更新普通配置 | 删除、修改系统助手 |
| Skill | 创建、更新草稿 | 发布、部署候选、归档或删除 |
| MCP | 创建无认证配置；更新已有配置的非敏感字段并保留原凭据 | 创建需认证配置、替换凭据、执行工具、删除 |
| Knowledge | 创建、更新 workspace 配置 | 上传文档、删除 workspace |

当前仓库没有独立 secret reference 模型。二期不顺带建设凭据管理子系统：系统助手只能创建
`AuthTypeNone` 的 MCP 配置；更新已有 MCP 时由现有 `MCPService.UpdateServer` 保留已存凭据，提案不得包含
敏感 `env`、header 或 auth 值。新增或更换凭据继续使用专用 MCP 表单，不进入聊天、提案 payload、执行 trace
或通用日志。

### 7.2 提案状态机

```text
draft -> ready_for_review -> confirmed -> applying -> applied
   |              |              |            |
 invalid        stale          expired       failed / unknown_outcome
```

- `draft`：模型生成类型化 payload，不产生资源副作用。
- `ready_for_review`：Schema、操作白名单和初始权限预检通过。
- `confirmed`：租户管理员通过受鉴权界面动作确认。
- `applying`：执行前原子 claim，并重新授权、校验基线和白名单。
- `applied`：业务服务成功后回读资源，并写入审计结果。
- `invalid`：字段、权限或操作不合法。
- `stale`：资源版本或内容指纹已变化，禁止覆盖；必须重新生成差异。
- `expired`：超过审阅期限，不能继续确认或应用。
- `failed`：能够确定应用失败，并暴露失败与回读状态。
- `unknown_outcome`：请求可能已产生外部影响但无法确认结果；禁止自动重放，转人工对账。

### 7.3 数据模型

租户 schema 增加 `resource_change_proposals`。公共信封至少包含：

- proposal ID、tenant、conversation、proposer、confirmer；
- resource kind、resource ID、operation；
- baseline revision/version 或内容指纹；
- 类型化 payload 和安全摘要；
- 状态、过期时间、创建/确认/应用时间；
- 错误分类、回读结果引用和审计时间线。

payload 必须按资源和操作使用独立 Schema，例如 `AgentCreate`、`SkillDraftUpdate`，禁止使用可表达任意 SQL
或任意后台请求的通用 payload。

### 7.4 应用边界

Proposal Orchestrator 只负责状态、确认、幂等、冲突和审计。Agent、Skill、MCP、Knowledge 各自在消费方
定义 Applier port，并由对应 context adapter 调用本域 application service。wiring 只做薄适配，不包含裸 SQL。

同一提案只能原子 claim 一次。失败后是否允许再次应用由确定性错误分类和回读结果决定，不交给模型。

## 8. API 契约

### 8.1 一期

- Agent 列表响应增加 `is_system`、`management_mode`。
- 新增系统助手设置 API，仅允许租户管理员读取/修改模型。
- 对话和执行复用现有 `/agents/:id/...` 路径与 SSE 行为。
- 执行结果增加结构化诊断报告和官方引用工件。
- 普通 Agent 更新/删除 API 对系统助手返回稳定领域错误。
- 内部诊断能力不开放为通用 HTTP 管理接口，也不暴露给普通 Agent。

### 8.2 二期

最小提案 API 支持：

- 读取提案详情；
- 修改尚未确认的草案；
- 取消待审提案；
- 管理员确认并触发确定性应用；
- 读取状态、错误、审计时间线和回读结果。

不存在供模型直接调用的任意资源写入 API。HTTP 错误继续遵循冻结响应体 `{"error":"..."}`。

## 9. 前端交互

- 系统助手在 Agent 对话列表固定置顶并显示“系统内置”。
- 系统助手不可进入普通编辑/删除流程。
- 无模型时显示静态引导；模型配置入口只对管理员可见。
- 回答先展示结论和下一步，再展示可折叠诊断报告、引用和工具步骤。
- 失败定位到官方检索、具体诊断工具、模型生成或报告持久化阶段。
- 二期消息展示提案摘要卡，点击进入专用审阅页。
- 审阅页展示字段差异、影响范围、权限要求、基线状态和确认按钮。
- `stale`、`invalid`、`failed`、`unknown_outcome` 展示具体原因，不提供绕过式应用。

所有页面遵循现有 loading、成功短提示、失败常驻、Empty 和移动端响应式规范。

## 10. 错误语义与可观测性

一期稳定错误至少包括：

- `assistant_model_unavailable`
- `official_evidence_not_found`
- `diagnostic_evidence_unavailable`
- `diagnostic_forbidden`
- `system_assistant_managed`

二期增加：

- `proposal_invalid`
- `proposal_stale`
- `proposal_expired`
- `proposal_forbidden`
- `proposal_already_claimed`
- `proposal_apply_failed`
- `proposal_unknown_outcome`

日志和 trace 只记录安全元数据：租户、用户、Profile 版本、诊断能力、资源类型、proposal ID、状态、耗时和
脱敏错误。不得记录提示词全文、密钥、Token、审批明文 payload 或原始上游响应。

## 11. 测试与验收

### 11.1 自动测试

1. 领域/应用：Profile 合成、受管字段保护、角色裁剪、证据分类、状态机、冲突和错误传播。
2. 数据库：新租户唯一实例、历史租户幂等补齐、失败不激活、两个租户完全隔离。
3. API/契约：列表标识、模型设置 RBAC、删除保护、报告结构、提案审阅与确认。
4. 真实链路：PostgreSQL、API、Agent Loop、官方检索、浏览器和测试模型/MCP。
5. 安全回归：伪造 tenant ID、成员请求管理员证据、提示注入诱导写入、密钥泄露、跨租户 ID 猜测、重复确认。

Agent、Skill、MCP、Knowledge、IAM 和数据库链路改动必须使用 `stratum-e2e-development` 完成真实 E2E。
一期和二期应分别形成可独立验收的测试场景，不能等二期完成后才验证一期。

### 11.2 产品验收

- 官方问题能基于实际证据回答并提供版本化引用。
- 典型 Agent、Skill、MCP、Knowledge 故障能给出事实、推断、缺失证据和下一步。
- 二期管理员能完成四类资源的受控创建/更新。
- 跨租户访问、越权诊断、无依据官方回答、未经确认写入和密钥泄露为零容忍。
- 工具失败不伪装成功；stale 提案不覆盖新配置；重复确认不重复写入。
- 记录首次有效指引时间、诊断耗时、配置步骤数和人工返工率；首版建立基线，不虚设提升百分比。

## 12. 证据与现状边界

| 设计主张 | 当前项目证据 | 结论 |
|---|---|---|
| 系统助手应复用现有 Agent loop | `internal/agent/application/agent_service.go`、`docs/agent/agent.md` | 不新增执行算法 |
| 租户实例需要稳定 ID | 会话、执行、trace 均关联 Agent ID | 不采用纯虚拟 Agent |
| 权限和审批必须确定性执行 | `docs/agent/agent.md`、现有 Tool Guard/Approval 状态机 | 模型只提出动作 |
| 租户数据必须留在 tenant schema | `pkg/storage/postgres/tenant_schema.sql`、`pkg/tenantdb` 规则 | 实例和提案均 tenant-scoped |
| Skill 已有候选和版本基础 | `internal/skill/application/version_service.go` | 二期复用本域服务，不重造 Skill 版本 |
| 跨资源统一版本模型尚不存在 | Agent、MCP、Knowledge 当前数据模型 | 提案统一状态，不强行统一资源内部版本 |

Obsidian 中已验证的 Agent/Harness 原则支持把模型执行循环与权限、状态、证据、恢复和人工控制分离；本设计只把
该原则用于解释边界，当前实现事实仍以仓库代码、测试和运行证据为准。

## 13. 实施拆分

本设计必须拆成两个独立实施计划：

1. **一期计划**：系统身份、租户生命周期、Profile、官方资料、只读诊断、角色裁剪、结构化报告和前端入口。
2. **二期计划**：提案模型、状态机、四类 Applier、审阅页、确认应用、冲突/未知结果和安全回归。

一期上线后先收集任务完成、安全门禁和效率基线，再进入二期。二期不得反向放宽一期的只读诊断边界。
