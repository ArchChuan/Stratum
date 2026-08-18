# 模型管理可编辑参数设计：硬拦截 + 默认注入采纳链

日期：2026-08-18 · 状态：修订稿（4 agent 并行评审后修订，待用户审阅）

关联：`2026-08-11-context-window-management-design.md`（窗口预算链，已落地）、`2026-08-13-model-management-refactor-design.md`（035 全局目录，部分落地）、`2026-08-14-audit-resource-change-design.md`（资源变更审计源）、`035_platform_model_catalog.up.sql`

## 1. Problem

模型目录（`providers`/`models`，public schema，035 迁移）已有 `context_window`/`max_tokens` 等字段，`PUT /admin/models/:id` 与前端 `ModelEditDrawer` 也已支持编辑，但产品化程度低，且模型配置对运行时行为几乎无约束：

| 缺口 | 证据 |
|---|---|
| `model.max_tokens` 未参与运行时采纳 | 请求 max_tokens=0 时兜底是常量 `DefaultOutputReserveTokens`(4096)，模型上限被无视；agent 显式配 131072 而模型上限 8192 时照样透传（clamp 仅对 known 模型生效，来源是静态目录 `LookupModelSpec` 而非 DB 权威值，gateway.go:361-363） |
| 采样参数无默认注入层 | agent 不显式配 temperature 时请求不带该字段（0=unset 跳过），模型/provider 上配的默认温度无人消费 |
| 采样越界无请求时拦截 | temperature >1 等越界只有「配置写入时」registry 校验（agent_service.go 注释：Qwen/Zhipu 拒收 >1 → 网关 500），请求时无防线 |
| 能力标签与运行时弱关联 | capabilities 仅用于分流（chat/embedding）与筛选；`ResolveStructuredOutput`/`ResolveReasoning` 已 fail-closed，tool_use 不约束请求形态 |
| UI 无约束/联动/分层 | InputNumber min=0 无上限提示，maxTokens 可 > contextWindow，无高级折叠 |

模型配置绑定更多参数后，会与现有配置点（agent 采样参数、平台参数 registry、窗口预算账本、memory 管线）交叉，须一次性梳理采纳链。

## 2. Goals / Non-Goals

### Goals

1. 模型管理成为「模型权威数据」的唯一编辑入口：上下文窗口、最大输出、采样默认值、采样上限、能力声明。
2. 采纳原则（用户批准）：**有请求失败风险的参数 → 模型权威数据硬拦截兜底（fail-closed，优先级最高）；其余无失败风险的参数 → 默认值注入层，优先级低于显式配置**。
3. 拦截与注入收敛到网关单一执行点（`Gateway.invoke()` 内，per-link）；上层零改动——**唯一例外**：agent 预算账本 `resolveOutputReserve` 读取 DB 权威 max_tokens（消除欠预留导致的 provider 400，见 §10）。
4. 编辑后立即生效（复用 registry 缓存失效），不固化到业务记录。

### Non-Goals

- 不做 08-13 的 `model_profiles` 行为档案收口（memory 机制收口是独立待办，未落地，不混入本次）。
- 不开放超时/重试/熔断配置——误配即引入失败风险，保持系统权威常量。
- 不引入模型自动 fallback/跨 provider 路由/租户自选模型池（沿用 035 全局目录语义）。
- 不改 `agent.temperature` 等显式配置的既有优先级（显式 > 模型默认 > 平台参数 > 常量）。
- 不做 vision 请求形态拦截：当前请求模型 `Message.Content` 为纯 string，无图像内容载体（domain/llm.go:45-51），vision 门控无请求形态可拦，待未来 content-block 改造后再议。

## 3. 业界参考（调研摘要）

对比 One API / new-api / LiteLLM / Dify / Open WebUI / Cherry Studio / FastGPT：

- **三层字段模型共识**：provider（URL+key）→ model（窗口/输出上限/能力/价格）→ 请求级（采样默认值）。本次设计沿用此分层。
- **全行业缺口**：max_tokens ≤ context_window 跨字段校验几乎无人实现（LiteLLM 声明不执行，issue #22249；Dify/FastGPT 只靠文档警示）。本次「模型权威硬拦截」正是差异化机会。
- 借鉴：FastGPT `maxTemperature`（0=不支持 temperature）、Cherry v3 强校验、LiteLLM 静态目录+用户配置双层（与现有 modelSpec 兜底同构）、Open WebUI 全局默认 < 模型级覆盖合并策略。

## 4. 采纳链（完整优先级，自高向低）

```
┌─ 硬拦截层（模型权威，fail-closed，永远最高；执行顺序 clamp → 注入 → 预检 → 校验）───┐
│ L1 max_tokens clamp：请求值 > 模型 max_tokens(>0) → clamp 到模型值                      │
│    模型 max_tokens=0（未知）→ 不 clamp（08-11 D7：未知不压制显式）；                    │
│    请求 max_tokens=0 → 注入模型 max_tokens(>0)；仍为 0 → 协议层常量兜底                 │
│    （openai_compat/anthropic 各自 4096 兜底；ollama 0=infinite 原生语义不注入）        │
│ L2 上下文窗口预检：EstimateMessages(msgs) + 有效 max_tokens（clamp+注入后）             │
│    > context_window → 拒绝 4xx（context_length_exceeded 语义）                          │
│    跳过条件 = context_window=0（窗口未知，不做拦截，provider 是最终权威）               │
│    估算为确定性纯函数（bytes/3，英文方向 ~33% 高估 = 保守 fail-closed 侧，可接受）      │
│ L3 采样越界：temperature > min(1, max_temperature) → 拒绝（校验注入后的有效值）        │
│    max_temperature=0 且请求带 temperature → 拒绝（不支持）                              │
│    max_temperature 值域 [0,1]（NULL=全局契约 [0,1]；>1 写时拒收，见 §7）               │
│ L4 能力不匹配（tool_use）：显式标注不支持（known-non）→ 拒绝；                         │
│    unknown → 放行（权威数据不存在时不拦截，provider 兜底；工具即请求目的，             │
│    不能像 reasoning_effort 一样清空）                                                   │
└──────────────────────────────────────────────────────────────────────────────────────┘
┌─ 默认注入层（无失败风险，低于显式配置）──────────────────────────────────────────────┐
│ 请求未设 → 模型 sampling_params → provider default_sampling                           │
│         → 不注入（维持现状；agent 显式 >0 语义完全不变）                               │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

- 执行顺序**必须先注入后校验**：注入的采样值与 clamp 后的 max_tokens 是 L2/L3 的校验对象，注入值必须回灌硬拦截层——「硬拦截层永远最高」才成立。
- 拦截错误 = 永久错误（复用 `markPermanent`，不重试不降级）+ 语义化 4xx；**L2 错误必须实现 `contextLengthMarker`**（复用 08-11 `ErrContextLengthExceeded` 语义），使 agent 侧 D4「最小请求重试」闭环在 L2 命中时仍可触发（L2 命中恰是账本估算失真的信号，正是 D4 设计的恢复场景）。
- 模型记录不存在（无 policy，如 provider.DefaultModel 未入库的 ② 级路径）→ 视为「权威数据不存在」→ L1-L3 跳过 + WARN 日志 + 指标（`llmgateway.policy_missing`），与「UNKNOWN 不压制」同构。

## 5. 数据模型（迁移 `038_model_editable_params`，public schema）

```sql
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    sampling_params  JSONB NOT NULL DEFAULT '{}';  -- temperature/top_p/frequency_penalty/presence_penalty/seed（0=unset）
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_temperature  DOUBLE PRECISION;             -- NULL=全局契约[0,1]；0=不支持 temperature；值域 [0,1]
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    extra_headers    JSONB NOT NULL DEFAULT '{}';  -- 追加请求头（写时黑名单；write-only 不回显）
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    default_sampling JSONB NOT NULL DEFAULT '{}';  -- Provider 级默认采样，模型级未配置时回退
```

- 均为 public schema、`IF NOT EXISTS`、安全默认值（满足 CLAUDE.md 迁移规则）；不改 tenant_schema.sql。
- `context_window` 现有列经 `GetChatModelContextWindow` 读 DB 被窗口链消费（window.go:29-32）；`max_tokens` 在账本侧经 vendor 静态表消费（agent_service.go:3149-3153），本次 L1 与账本一并切换 DB 权威（§10）。两列均为权威上限，不加列。
- 领域实体：`Model.SamplingParams` 用指针字段（`Temperature *float64`），与 agent「0=unset」语义对齐；`Model.MaxTemperature *float64`；`Provider.ExtraHeaders map[string]string`、`Provider.DefaultSampling map[string]any`，**两者 `json:"-"` write-only**（Create/Update 接收，响应永不返回——与 `Provider.APIKey` 同模式）。

## 6. 运行时执行点（评审后修订：Gateway.invoke 层，覆盖全部协议与流式/非流式）

- **执行点挂 `Gateway.invoke()`（gateway.go:293）内 `applyCapabilityGate`/`applyMaxTokensPolicy` 同级**，per-link 应用（fallback 候选模型的 max_tokens/context_window/max_temperature 与主模型不同，L1-L4 必须以每次尝试的 link 为准，在链入口用主模型 policy 预检会错）。天然覆盖全部协议 kind（openai_compat/anthropic/ollama，qwen/zhipu 是 openai_compat 类型别名）与流式（CompleteStream）与非流式（Complete）——协议客户端各自实现，不可作为执行点。
- `resolvedEntry` 缓存扩展 `policy` 字段（maxTokens/contextWindow/temperatureMax/capabilities/samplingDefaults）。`cacheSet` 各解析级（resolveExact/resolveProviderDefault/resolveRecommended/resolveEmbeddingMarked/ResolveFallbackCandidates）按解析出的模型名携带 policy；②③④ 级无模型记录时 policy=nil → 按 §4「权威数据不存在」语义跳过 + WARN + 指标。policy 预计算同时**吸收 `ResolveReasoning`/`ResolveStructuredOutput` 的 N+1 查询**（现每 link 每请求各发一次 `modelRepo.List`，model_registry.go:638-665），迁移而非并存。
- 策略函数 `enforceModelPolicy(req, policy) *CompletionRequest` 纯函数化：clamp → 注入 → 预检 → 校验，返回副本（与 `applyCapabilityGate` 同「绝不修改共享 req」约定）。
- 与现有 `applyMaxTokensPolicy`（gateway.go:357-385）的关系：静态目录 clamp 分支**由 L1 取代**（DB 权威值优先，目录压回会使 DB 权威声明失效）；reasoning floor（抬升到 4096）保留，先后 =「先 floor 后 clamp」（管理员权威上限最后应用，若模型上限低于 floor 抬升值，clamp 到模型上限 + WARN 日志）；`LookupModelSpec` 保留作账本侧 vendor 兜底与 UNKNOWN 兜底。既有 `max_tokens_policy_gateway_test.go`/`max_tokens_policy_internal_test.go` 迁移改造。
- 缓存失效复用现有 `Invalidate()`（模型/Provider 编辑后立即生效）。
- 注入的采样参数与 clamp 结果写日志与指标（model/provider/token 数等必要元数据，不记录敏感信息）。

## 7. API 契约（Gin JSON DTO + contract golden；无 proto 端点）

> 模型/Provider 管理是 Gin `ShouldBindJSON`（model_mgmt_handler.go），`proto/` 下无对应端点，`make proto-gen` 不涉及本次契约——契约经 JSON DTO + `api/http/contract_test.go` golden 守护。

```
UpdateModelInput    + sampling_params（0=unset；写时按字段范围校验）
                    + max_temperature *float64（NULL=全局契约[0,1]；0=不支持；写时校验 [0,1] 与负数拒绝）
UpdateProviderInput + extra_headers（key→value；write-only，响应永不返回）
                    + default_sampling JSONB（回退层）
```

- **写时校验（domain 纯函数定义规则 → application 层调用 → repo 保持纯 IO）**：
  - `max_temperature` ∈ [0,1] 或 NULL；负数拒绝；>1 拒绝（agent 侧 temperature 已被 registry VisualHint 约束 [0,1]，>1 是死配置）。
  - `sampling_params` 各键范围与 L3 同一套边界（temperature/top_p ∈ [0,1] 等），抽公共校验函数。
  - 跨字段：`max_temperature > 0` 时 `sampling_params.temperature ≤ max_temperature`；`max_temperature = 0` 时禁止 `sampling_params` 含 temperature——防「注入值被自己的 L3 拒绝」的运行时失败环。
  - map 字段空值语义：**空 map = 保留既有值，显式 `null` = 清空**（对齐 `apiKey`「空=保留」特例，防 PUT 全量替换误清空）。
- `ProviderManaged` 模型：发现回写（UpsertDiscovered）不触碰 sampling_params/max_temperature；人工编辑允许（与 displayName 同语义）。
- golden 同步断言：**响应不含 extra_headers 值**（write-only 契约）。

## 8. 安全边界（extra_headers，评审后重写）

- **写时校验（唯一防线）**：
  - 头名先 `textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))` 归一化再匹配**固定黑名单集合**（大小写变体/尾空格穿透是真实覆盖风险）：`Authorization`、`Content-Type`、`User-Agent`、`X-API-Key`、`api-key`、`Host`、`Cookie`、`Proxy-Authorization`、`Referer`、`Transfer-Encoding`、`Content-Length`、`Trailer`、`Accept-Encoding`、`X-Forwarded-For`、`Forwarded`（后两者伪造客户端 IP 绕过 provider 侧防护）。
  - 头值拒绝控制字符（CRLF 注入：Go transport 虽 fail-closed 报错，写时应直接拒收）。
  - 大小写变体/尾空格/控制字符写表驱动单测。
- **合并顺序**：请求发出时 extra_headers 先应用，客户端自身 `Authorization: Bearer`/`x-api-key`/`Content-Type` 等硬编码鉴权头**最后设置且永远覆盖**（openai_compat.go:462-464、anthropic.go:598-601 注入点）。不存在「请求显式 header > extra_headers」合并规则——`CompletionRequest` 无 Headers 字段，请求级 header 无载体。
- **不回显**：`extra_headers`/`default_sampling` 值永不出现在任何响应（write-only）；日志与错误正文不打印（前端编辑表单仅回显 key 名，值显示「已配置」掩码）。
- **覆盖范围**：extra_headers 并入 `ProviderConfig`，header 合并收敛为**三客户端共用 helper**（openai_compat/anthropic/ollama 的 chat 请求 + operational 端点 Discover/Health/ListModels 均携带，否则私有网关头 provider 的发现/健康检查与 chat 行为不一致）。

## 9. 前端（product.md：首屏 ≤8 项、高级折叠）

- `ModelEditDrawer`：基础区保持现状；新增**高级设置折叠区**——采样参数（带标签 Slider，temperature 显示范围 [0,1]）、max_temperature Slider [0,1]（tooltip：NULL=全局契约、0=不支持 temperature，编辑后运行时语义说明「请求未显式配置时生效；超上限自动 clamp/拒绝」）。
- `ProviderForm`：扩展高级区——额外 headers 键值编辑（值回显掩码「已配置」/「未配置」，黑名单头在表单层即禁用）、默认采样参数。
- `ModelListPage`：不新增列（≤5 列原则），tooltip 呈现。

## 10. 对现有配置的冲击梳理（评审后修订）

| 现有配置点 | 影响 |
|---|---|
| `agent.temperature`（registry，0.7） | 不变——显式配置优先级最高；模型默认注入仅在 agent=0 时生效 |
| `agent.max_tokens`（registry，≤131072） | 请求超模型上限 → clamp（新拦截）；=0 未设置时注入模型 max_tokens(>0)（**仅 openai_compat/anthropic 协议族**，ollama 0=infinite 原生语义不注入），仍为 0 时维持协议层 4096 常量兜底 |
| 08-11 窗口链（registry→vendor 表→UNKNOWN） | 不动；L2 预检仅新增「已知窗口估算超限 → 拒绝」最终防线，窗口未知（context_window=0）不拦截，provider 是最终权威 |
| 08-11 预算账本 `resolveOutputReserve`（显式 > vendor 表 maxOut > 4096） | **链头插入 DB 权威**：显式 cfg.MaxTokens > **DB 模型 max_tokens** > vendor 表 maxOut > 4096。欠预留现状：agent max_tokens=0 时 L1 注入 DB 值 8192 发送，账本只预留 vendor maxOut 4096 → 输出可溢出窗口 → provider 400 永久中止链。改链后预留与发送一致（Goal 3 的唯一例外，§12 列 agent_service.go + 相关 port） |
| 08-13 `model_profiles`（未落地） | 本次不做——memory 机制收口是独立待办，边界注明 |
| 超时/重试/熔断 | 不开放配置，保持系统常量 |
| memory 管线（enrich/summary 平台参数） | 不变——不涉模型目录字段 |
| **审计（新增）** | 模型/Provider 是平台级权威数据，编辑即改变所有租户运行时行为（08-14 已停写平台级 HTTP 审计，`resource_change_audits` 是唯一审计源）→ 写路径同事务写入 `ResourceChangeAuditEvent` |

## 11. 审计（评审新增）

- `internal/audit/domain/change_audit.go` 的 `ResourceKind` 扩展 `ResourceKindModel`/`ResourceKindProvider`。
- `ModelMgmtService.Update`/`ProviderService.Update` 在业务事务内同事务写审计事件；Before/After 投影**脱敏**：`extra_headers` 值、`api_key` 一律不进投影（对齐 change_audit.go 的 de-sensitized 约定），sampling_params/max_temperature 等数值进投影。
- 归属：事件按操作者租户归属（模型目录在 public schema，无 tenant 语义；审计事件是操作记录，非数据归属）。

## 12. 测试策略（评审扩充）

- 表驱动单测：clamp 边界（=上限/超上限/未知 0）、UNKNOWN 不 clamp、注入链（请求未设 → 模型 → provider → 不注入）、**执行顺序（clamp → 注入 → 预检 → 校验，注入值越界必须被 L3 拒绝）**、max_temperature 越界拒绝、L4 tool_use（known-non 拒 / unknown 放行）、**per-link fallback（候选模型上限不同时按各自 link 策略执行）**、**无 policy 路径 fail-open + `policy_missing` 指标**、header 合并（extra_headers 先应用、鉴权头最后覆盖）、**黑名单大小写变体/尾空格/控制字符拒绝**、日志不打印 extra_headers 值、缓存失效后策略生效。
- **L2 专项**：窗口未知跳过、超限拒绝 + `IsContextLengthExceeded` 语义（`ContextLengthExceeded() bool` 断言）、**L2 命中 → agent D4 最小请求重试 → 通过预检** 全链路。
- **写时校验专项**：max_temperature 负数/>1 拒绝、temperature ≤ max_temperature、max_temperature=0 禁 temperature、map 空值语义（空=保留、null=清空）。
- **既有测试迁移**：`max_tokens_policy_gateway_test.go`/`max_tokens_policy_internal_test.go` 由静态目录改 DB policy 权威；`model_repo_internal_test.go`/`provider_repo_internal_test.go` pgxmock SQL 正则同步新列；`pkg/migration/migration_test.go` 加 038 内容断言（仿 `TestRemoveTenantLLMAPIKeysMigration`）；`test/e2e/llm_admin_test.go` 的 `provisionPublicCatalog` 手工 035 DDL 副本同步 4 列（否则 e2e 运行时崩溃）。
- `api/http/contract_test.go` golden 更新（stub 需能返回带新字段的记录）+ 断言响应不含 extra_headers 值；相关 test mock/stub 同步（port 扩展后立即搜索同步）。
- 服务层字段传播测试（Update 逐列校验、ProviderManaged 保留语义）。
- 系统验收走 `stratum-e2e-development`：编辑采样参数 → agent 执行时采纳；clamp 生效；provider 拦截错误 4xx。

## 13. 改动文件清单（评审后修订）

| 文件 | 改动 |
|---|---|
| `pkg/migration/sql/038_model_editable_params.{up,down}.sql` | 新增迁移 |
| `internal/llmgateway/domain/model.go` | `Model.SamplingParams`（指针字段）、`MaxTemperature`；写时校验规则纯函数 |
| `internal/llmgateway/domain/provider.go` | `Provider.ExtraHeaders`、`DefaultSampling`（`json:"-"`） |
| `internal/llmgateway/domain/port/model_repo.go` / `provider_repo.go` | SELECT/UPDATE 列扩展（逐列核对） |
| `internal/llmgateway/infrastructure/model_repo.go` / `provider_repo.go` | SQL 列同步 |
| `internal/llmgateway/infrastructure/model_registry.go` | `resolvedEntry.policy`、cacheSet 各解析级携带 policy（②③④ 级补查询）、吸收 ResolveReasoning/ResolveStructuredOutput N+1 |
| `internal/llmgateway/infrastructure/gateway.go` | **`enforceModelPolicy` 执行点（invoke 内，per-link）**；`applyMaxTokensPolicy` 静态 clamp 分支由 L1 取代、floor 保留 |
| `internal/llmgateway/infrastructure/provider_runtime.go` | extra_headers 并入 `ProviderConfig`（Discover/Health/ListModels 共用） |
| 三协议客户端（openai_compat/anthropic/ollama） | 共享 header 合并 helper（先 extra_headers 后硬编码鉴权头） |
| `internal/llmgateway/infrastructure/errors.go` | 语义化拦截错误（永久标记）；L2 错误实现 `ContextLengthExceeded() bool` |
| `internal/llmgateway/application/model_mgmt_service.go` / `provider_service.go` | 更新输入扩展 + 写时校验调用 + 审计同事务写入 |
| `internal/agent/application/agent_service.go` + 相关 port | `resolveOutputReserve` 链插入 DB 模型 max_tokens（Goal 3 唯一例外） |
| `internal/audit/domain/change_audit.go` + infra | `ResourceKindModel`/`ResourceKindProvider`、脱敏投影 |
| `api/http/handler/model_mgmt_handler.go` / `provider_handler.go` | DTO 绑定 |
| `web/src/modules/llm/` | Drawer/Form 高级折叠区（max_temperature Slider [0,1]、headers 掩码编辑）、类型扩展 |
| `api/http/contract_test.go` + testdata golden | 契约断言（含 write-only 不回显） |
| `internal/llmgateway/infrastructure/max_tokens_policy_*_test.go` | 静态目录 → DB policy 权威迁移 |
| `internal/llmgateway/infrastructure/model_repo_internal_test.go` / `provider_repo_internal_test.go` | pgxmock SQL 正则同步 |
| `test/e2e/llm_admin_test.go` | `provisionPublicCatalog` 同步 4 列 |
| `pkg/migration/migration_test.go` | 038 迁移内容断言 |
| 各 repository 测试 mock | port 扩展同步 |
