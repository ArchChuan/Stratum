# 平台助手承载架构设计：文档语料 + 行为 Skill + 工具执行

**状态：已确认，待实施**

## 1. 背景与问题

平台助手（`stratum-platform-assistant`、`system_key=stratum.platform_assistant`）当前知识/能力承载有两条
并存路径：

1. **RAG 文档路径**：`docs/assistant/catalog.yaml` → 构建期分块（1200 runes/块）→ `catalog.json` →
   `SeedBuiltinDocs` 幂等灌入 `stratum_docs` workspace → Milvus → `stratum_search_official_docs` 工具。
2. **内置 skill 路径**：`internal/skill/infrastructure/seeds/builtin_skills.go` 中的
   `builtin:platform-guide`、`builtin:tenant-diagnostic`，单 published revision，种子 SQL 绑定系统助手。

产品需求升级：平台助手不只能回答，还要**实际上手做**——创建/更新资源（L2）、执行工具（L3）。因此需要把
"文档承载什么、skill 承载什么、工具执行如何授权"彻底讲清楚：

- 文档（RAG）做**问答**：官方知识检索。
- skill 做**行为引导**：流程执行时模型的 WhenToUse 决策和步骤指引。
- 工具执行走**硬编码控制层**：权限、角色、风险分级、审批状态机、Applier、审计，模型只提动作。

本设计承接两份既有 spec 的演进：

| 上游 spec | 状态 | 本设计的关系 |
|---|---|---|
| 2026-07-23 builtin-platform-assistant-design | 已确认，待实施计划 | 一期只读（L1）与二期受控写（L2）的分层被本设计采纳；其"二期不执行 MCP 工具"边界被本设计 + 07-29 共同 supersede |
| 2026-07-29 platform-mcp-control-plane-design | 已确认，待用户复核文档 | 校准基准。L3a（平台业务工具执行）复用其 ToolContract 风险分级；L3b（租户外部工具执行）为其阶段二/三范围未覆盖的新增能力 |

## 2. 决策记录

| # | 决策 | 理由 | 反例/被否方案 |
|---|---|---|---|
| 1 | 承载三层次：硬编码控制层 + 行为 skill 层 + 文档语料层 | 控制逻辑（授权/审批/审计）必须确定性执行；模型只做语言任务 | 全部交给模型/文档承载 → 授权不可审计、不可 fail closed |
| 2 | 能力分层：L1 问答（RAG）+ 只读诊断；L2 四类资源配置写（管理员确认提案）；L3 工具执行（双路径） | 与 07-23 一/二期、07-29 阶段一/二对齐，能力按风险递增 | 一次全上 → 跨资源写入同时引入，无法分阶段验收 |
| 3 | **L3 双路径**：L3a 平台业务工具（复用 07-29 ToolContract 风险分级：read 自动、write_reversible 一次确认、destructive/credential 二次确认）；L3b 租户外部 MCP 工具（新设计：复用 MCPToolPolicy read 自动 / write_reversible 审批 / destructive+unclassified 拒绝） | 平台工具与租户工具的信任模型不同：平台工具合同硬编码，租户工具凭据与策略归租户 | 只做平台工具 → 用户"实际上手做"的范围被限制在 Stratum 内部；只做外部工具 → 平台自身能力无法执行 |
| 4 | 行为 skill 按能力拆 4 个：platform-guide（问答）、tenant-diagnostic（诊断）、resource-change（新，提案流程）、tool-execution（新，工具执行流程）；WhenToUse 互斥定义 | skill 粒度 = 行为意图粒度，互斥 WhenToUse 避免模型切换歧义 | 单一大 skill → 指令冲突、切换粒度粗；按工具拆 → skill 数量爆炸 |
| 5 | ~~保留 `stratum_activate_skill` 单激活切换~~ → 被 skill 触发范式重构 supersede：统一 `stratum_skill` 工具 + 多 skill 并列生效、冲突由模型自决（见 skill-skill-elegant-glacier 设计） | 激活机制已有实现且测试覆盖；`ComposeSystemAssistantProfile` 保留 AllowedSkills（system_assistant_profile.go:129-140），机制可行 | 加确定性兜底 → 需要路由规则硬编码，与"模型按 WhenToUse 决策"冲突 |
| 6 | 文档语料源从内部开发规范换成面向平台用户的官方使用文档 | 现有 catalog 3 篇（agent.md / mcp-integration.md / knowledge-workspace.md）是内部开发规范，不适合对用户问答 | 继续用开发规范 → 暴露内部实现细节，问答质量差 |
| 7 | 生成方式：半自动 pipeline——从 API 契约/配置项/UI 结构提取事实骨架生成初稿，人工审校补叙述 | 全人工维护跟不上功能演进；全自动生成叙述质量不可控 | 全人工 → 功能稳定后一次性重写成本高；全自动 → 事实与叙述失真 |
| 8 | 生成时机：平台助手一、二期（含 07-29 各阶段）+ 相关功能稳定后全量执行；复用现有 officialdocs 分块 pipeline | 功能未稳定时生成的文档很快过时 | 边开发边生成 → 返工 |
| 9 | 以 2026-07-29 control-plane spec 为校准基准；其"阶段二/三工具范围"未覆盖的外部 MCP 工具执行（L3b）由本设计扩展 | 07-29 已覆盖平台工具合同、风险等级、审批、密钥会话，是最接近的已确认设计 | 以 07-23 为基准 → 其"不执行 MCP 工具"边界已被需求 supersede |

裁决过程记录：决策 3 曾只覆盖平台业务工具（07-29 范围），五轴审核发现"工具执行"对象歧义（平台 vs 外部），
用户裁决**双路径**——平台工具与租户外部工具都执行，但授权模型不同。

## 3. 承载架构：三层次职责边界

```text
┌─────────────────────────────────────────────────────────────┐
│ 硬编码控制层（确定性，fail closed）                          │
│   权限/角色裁剪、MCPToolPolicy 与 ToolContract 风险分级、      │
│   审批状态机、提案 Applier、审计、密钥注入与脱敏、租户隔离      │
├─────────────────────────────────────────────────────────────┤
│ 行为 skill 层（模型侧行为引导）                               │
│   WhenToUse 决策、步骤指引、工具选择偏好、输出结构约束          │
│   4 个 skill 单激活切换，Instructions 短、只引工具名           │
├─────────────────────────────────────────────────────────────┤
│ 文档语料层（问答知识）                                       │
│   面向用户的官方文档 → 分块 → RAG 检索 → 证据引用             │
└─────────────────────────────────────────────────────────────┘
```

**原则**：路由、重试、授权、审批、状态机、冲突处理和回滚全部硬编码；skill 只做模型侧行为引导（WhenToUse、
步骤、输出格式），不承载控制逻辑；文档只承载问答知识，不承载行为指令。

## 4. 能力分层与授权

### 4.1 L1：问答与只读诊断

- 问答：`stratum_search_official_docs`（官方文档 RAG），严格证据模式。
- 诊断：`stratum_diagnose_tenant`（只读，角色裁剪证据，普通成员只见本人相关，管理员见租户级脱敏证据）。
- 授权：无需审批；任何权限读取失败 fail closed。

### 4.2 L2：四类资源配置写（管理员确认提案）

- 工具：`stratum_propose_resource_change`（Agent / Skill / MCP / Knowledge 的创建与普通配置更新）。
- 流程：模型生成类型化提案 → 管理员确认 → 确定性 Applier 应用 → 回读 + 审计。
- 授权：管理员角色 + 提案状态机（`resource_change_proposals`），禁止删除、凭据替换、发布等破坏性操作。

### 4.3 L3a：Platform MCP 工具执行（平台业务能力）

复用 2026-07-29 ToolContract 风险分级（read / write_reversible / destructive / credential / forbidden）：

| 风险级 | 授权 |
|---|---|
| read | 自动放行 |
| write_reversible | 一次确认 |
| destructive / credential | 二次确认 |

平台工具合同硬编码（`pkg/platformmcp/contract.go`），绑定约束遵循 07-29 §6.3：只有
`system_key=stratum-platform-mcp` 且 platform_managed 才加载平台身份；密钥会话原则（密钥不进入对话/SSE/
trace/提案/日志）沿用。

### 4.4 L3b：租户外部 MCP 工具执行（新设计）

平台助手执行**租户自己挂载的外部 MCP 工具**（如 GitHub、内部系统）。信任模型与 L3a 不同：合同不是平台
硬编码，凭据与策略归租户。

**绑定约束**

- 平台助手可绑定租户 `tenant_managed` MCP server 的工具。
- 系统助手身份与租户身份分离：平台身份（platform_managed）不参与租户工具执行；凭据是租户自身配置的。
- 普通 Agent 不得解析平台 MCP 绑定（`TestOrdinaryAgentCannotResolveCopiedPlatformMCPBindings` 已守护）。

**授权（复用现有 MCPToolPolicy 风险分级）**

| MCPToolPolicy 风险级 | 平台助手行为 |
|---|---|
| read | 自动放行 |
| write_reversible | 管理员审批（复用现有审批链路） |
| destructive | 拒绝 |
| unclassified | 一律拒绝（fail closed，即使管理员未标注也不放行） |

**凭据处理**

- 凭据在 Harness 层注入请求，不透出给模型（模型只见工具名与脱敏参数）。
- 密钥不进入对话、SSE、trace、提案 payload 或通用日志。

**结果脱敏**

- 外部工具返回值可能含敏感数据（代码、个人数据、密钥），进入对话前经过脱敏（复用
  `tool_result_guard`/secret redaction 现有实现）。

**租户隔离与 prompt injection 防护**

- 只能执行当前租户配置且被批准的 server 工具；跨租户工具不可见。
- 外部数据视为不可信输入：工具返回值不得作为授权依据，注入指令不改变 Harness 行为。

**审计**

- 每次外部工具执行记录：工具名、脱敏参数、耗时、结果摘要、用户、租户。

## 5. Skill 架构

### 5.1 四个内置 skill

| skill | Capability Goal | WhenToUse（互斥） | 关键工具 |
|---|---|---|---|
| builtin:platform-guide | 基于官方资料回答平台使用问题 | 用户询问功能、概念、配置步骤（"如何创建 Agent"） | stratum_search_official_docs |
| builtin:tenant-diagnostic | 诊断当前租户各模块状态 | 用户询问状态、排查问题、检查配置（"为什么 Agent 执行失败"） | stratum_diagnose_tenant |
| builtin:resource-change | 受控创建/更新四类资源（提案流程） | 管理员要求创建或修改资源（"帮我创建一个新 Agent"） | stratum_propose_resource_change |
| builtin:tool-execution | 执行已授权工具（平台 + 租户外部） | 需要实际操作外部系统或平台工具（"查一下 GitHub issue #42"） | 平台/租户 MCP 工具目录 |

WhenToUse 边界样例（互斥裁决）：

- "查 GitHub issue" → tool-execution（外部操作）
- "如何创建 Agent" → platform-guide（知识问答）
- "为什么 Agent 执行失败" → tenant-diagnostic（状态诊断）
- "帮我创建一个新 Agent" → resource-change（配置写，需管理员确认）

### 5.2 激活机制

- ~~保留 `stratum_activate_skill` 单激活切换：同一时刻一个 active skill。~~ → 被 skill 触发范式重构 supersede：统一 `stratum_skill` 工具（参数 `skill`）激活一个或多个 instruction bundle，多 skill 并列生效、冲突由模型自决；重复激活被入口层拦截。
- 模型按 WhenToUse 语义切换，不加确定性兜底（决策 5）。
- 已知风险：切换错误时行为引导不匹配；接受该风险，由工具层硬编码授权兜底（工具不可用则无法执行）。

### 5.3 systemAssistantPrompt 同步

当前 `system_assistant_profile.go:19-33` 的 prompt 含 "direct write tools remain forbidden"，与 L3 冲突。
必须同步更新为：

- L2 提案可创建（保留现有描述）；
- L3 工具执行按授权边界描述（平台工具风险分级、租户工具只执行被批准的）；
- 保留密钥禁令与证据模式。

Profile 版本 bump 为 `2026-08-04.v2`（保留历史版本，`BuiltinSystemAssistantProfiles` 追加）。

## 6. 文档语料

### 6.1 语料源更换

catalog 从内部开发规范换成面向平台用户的官方使用文档（概念 / 功能 / 配置参考），建议 3-4 篇：

| 文档 | 内容 |
|---|---|
| 平台概览与概念 | 多租户、Agent、Skill、MCP、Knowledge、工作流 |
| Agent 用户指南 | 创建/配置/对话/执行/权限 |
| Skill 用户指南 | 查看、激活、评测 |
| MCP 用户指南 | 挂载 server、工具策略、风险分级 |

### 6.2 拆分原则

- 开发规范文档（docs/agent/*.md）不进用户语料。
- 内部规则（安全红线、fail closed 语义、实现细节）不得暴露给用户问答。
- 新语料只描述面向用户的稳定行为。

## 7. 半自动生成 pipeline

**目标**：功能稳定后全量重生成官方文档，避免人工全文重写。

**事实骨架提取源**：

- API 路由表与 handler 契约（`api/http/router.go` + `contract_test.go` golden 文件）；
- 配置项 schema（`pkg/constants/`、config 结构）；
- 前端页面结构（`web/src/modules/`）。

**流程**：提取事实骨架 → 初稿模板生成（概念/功能/配置参考三类模板）→ 人工审校补叙述。

**人工审校门禁**：

- 事实准确率（与代码/契约对账）；
- 引用完整性（catalog 源文件与 URL 有效）；
- 无内部实现细节泄漏。

**触发时机判定标准**：07-23 一、二期验收完成 + 07-29 各阶段验收完成 + 相关功能冻结（无未决功能 PR）。

## 8. 与现有设计的关系

- **2026-07-29 control-plane spec 为校准基准**（其"已确认，待用户复核文档"状态）：L3a 完全复用其
  ToolContract/审批/密钥会话设计；本设计扩展其阶段二/三工具范围未覆盖的 L3b。
- **2026-07-23 spec 的"二期不执行 MCP 工具"边界**被本设计 + 07-29 共同 supersede：平台业务工具执行见
  07-29，租户外部工具执行见本设计 §4.4。
- 本设计与 07-29 是"确认 + 扩展"关系，非替代。

## 9. 验证

- spec 自审：无占位符、决策表与正文一致、9 项决策全部显式记录。
- 与 07-23 / 07-29 交叉核对：决策表逐项对照，矛盾处显式标注覆盖关系。
- 实施验证（后续计划）：skill 拆分测试（WhenToUse 互斥）、prompt bump 后 Profile 合成测试、
  L3b 授权路径测试（read 放行 / write_reversible 审批 / destructive+unclassified 拒绝、跨租户不可见）。
