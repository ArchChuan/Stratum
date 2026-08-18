# 模型管理可编辑参数设计：硬拦截 + 默认注入采纳链

日期：2026-08-18 · 状态：已批准（用户逐节确认）

关联：`2026-08-11-context-window-management-design.md`（窗口预算链，已落地）、`2026-08-13-model-management-refactor-design.md`（035 全局目录，部分落地）、`035_platform_model_catalog.up.sql`

## 1. Problem

模型目录（`providers`/`models`，public schema，035 迁移）已有 `context_window`/`max_tokens` 等字段，`PUT /admin/models/:id` 与前端 `ModelEditDrawer` 也已支持编辑，但产品化程度低，且模型配置对运行时行为几乎无约束：

| 缺口 | 证据 |
|---|---|
| `model.max_tokens` 未参与运行时采纳 | 请求 max_tokens=0 时兜底是常量 `DefaultOutputReserveTokens`(4096)，模型上限被无视；agent 显式配 131072 而模型上限 8192 时照样透传（clamp 仅对 known 模型生效，来源是静态目录而非 DB 权威值） |
| 采样参数无默认注入层 | agent 不显式配 temperature 时请求不带该字段（0=unset 跳过），模型/provider 上配的默认温度无人消费 |
| 采样越界无请求时拦截 | temperature >1 等越界只有「配置写入时」registry 校验（agent_service.go:289 注释：Qwen/Zhipu 拒收 >1 → 网关 500），请求时无防线 |
| 能力标签与运行时弱关联 | capabilities 仅用于分流（chat/embedding）与筛选；`ResolveStructuredOutput`/`ResolveReasoning` 已 fail-closed，tool_use/vision 不约束请求形态 |
| UI 无约束/联动/分层 | InputNumber min=0 无上限提示，maxTokens 可 > contextWindow，无高级折叠 |

模型配置绑定更多参数后，会与现有配置点（agent 采样参数、平台参数 registry、窗口预算账本、memory 管线）交叉，须一次性梳理采纳链。

## 2. Goals / Non-Goals

### Goals

1. 模型管理成为「模型权威数据」的唯一编辑入口：上下文窗口、最大输出、采样默认值、采样上限、能力声明。
2. 采纳原则（用户批准）：**有请求失败风险的参数 → 模型权威数据硬拦截兜底（fail-closed，优先级最高）；其余无失败风险的参数 → 默认值注入层，优先级低于显式配置**。
3. 拦截与注入收敛到网关单一执行点，上层（agent/skill/memory/evaluation）零改动。
4. 编辑后立即生效（复用 registry 缓存失效），不固化到业务记录。

### Non-Goals

- 不做 08-13 的 `model_profiles` 行为档案收口（memory 机制收口是独立待办，未落地，不混入本次）。
- 不开放超时/重试/熔断配置——误配即引入失败风险，保持系统权威常量。
- 不引入模型自动 fallback/跨 provider 路由/租户自选模型池（沿用 035 全局目录语义）。
- 不改 `agent.temperature` 等显式配置的既有优先级（显式 > 模型默认 > 平台参数 > 常量）。

## 3. 业界参考（调研摘要）

对比 One API / new-api / LiteLLM / Dify / Open WebUI / Cherry Studio / FastGPT：

- **三层字段模型共识**：provider（URL+key）→ model（窗口/输出上限/能力/价格）→ 请求级（采样默认值）。本次设计沿用此分层。
- **全行业缺口**：max_tokens ≤ context_window 跨字段校验几乎无人实现（LiteLLM 声明不执行，issue #22249；Dify/FastGPT 只靠文档警示）。本次「模型权威硬拦截」正是差异化机会。
- 借鉴：FastGPT `maxTemperature`（0=不支持 temperature）、Cherry v3 强校验、LiteLLM 静态目录+用户配置双层（与现有 modelSpec 兜底同构）、Open WebUI 全局默认 < 模型级覆盖合并策略。

## 4. 采纳链（完整优先级，自高向低）

```
┌─ 硬拦截层（模型权威，fail-closed，永远最高）───────────────────┐
│ L1 max_tokens clamp：请求值 > 模型 max_tokens(>0) → clamp 到模型值 │
│    模型 max_tokens=0（未知）→ 不 clamp（08-11 D7：未知不压制显式）   │
│ L2 上下文窗口预检：单请求估算 tokens > context_window → 拒绝 4xx    │
│    （agent 预算压缩层 08-11 已落地；网关预检是最终防线，            │
│      估算失败 fail-open 交给 provider，provider 是最终权威）       │
│ L3 采样越界：temperature > min(1, max_temperature) → 拒绝          │
│    max_temperature=0 且请求带 temperature → 拒绝（不支持）         │
│ L4 能力不匹配：请求形态 vs capabilities                           │
│    （扩展 ResolveStructuredOutput/ResolveReasoning fail-closed     │
│      模式到 tool_use/vision）                                      │
└──────────────────────────────────────────────────────────────────┘
┌─ 默认注入层（无失败风险，低于显式配置）────────────────────────┐
│ 请求未设 → 模型 sampling_params → provider default_sampling       │
│         → 不注入（维持现状；agent 显式 >0 语义完全不变）           │
└──────────────────────────────────────────────────────────────────┘
```

拦截错误 = 永久错误（复用 `markPermanent`，不重试不降级）+ 语义化 4xx。

## 5. 数据模型（迁移 `038_model_editable_params`，public schema）

```sql
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    sampling_params  JSONB NOT NULL DEFAULT '{}';  -- temperature/top_p/frequency_penalty/presence_penalty/seed（0=unset）
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_temperature  DOUBLE PRECISION;             -- NULL=全局契约[0,1]；0=不支持 temperature
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    extra_headers    JSONB NOT NULL DEFAULT '{}';  -- 追加请求头（安全头黑名单）
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    default_sampling JSONB NOT NULL DEFAULT '{}';  -- Provider 级默认采样，模型级未配置时回退
```

- 均为 public schema、`IF NOT EXISTS`、安全默认值（满足 CLAUDE.md 迁移规则）；不改 tenant_schema.sql。
- `max_tokens`/`context_window` 现有列即为权威上限（08-11 已消费），不加列。
- 领域实体：`Model.SamplingParams` 用指针字段（`Temperature *float64`），与 agent「0=unset」语义对齐；`Provider.ExtraHeaders map[string]string`、`Provider.DefaultSampling`。

## 6. 运行时执行点

- `resolvedEntry` 缓存扩展 `policy` 字段（warm 时从 DB 模型记录预计算：maxTokens/contextWindow/temperatureMax/capabilities/samplingDefaults）。
- `openai_compat.go` `Complete` 入口前统一 `enforceModelPolicy(req)`：clamp/预检/校验/默认注入。
- 缓存失效复用现有 `Invalidate()`（模型/Provider 编辑后立即生效）。
- 注入的采样参数与 clamp 结果写日志与指标（model/provider/token 数等必要元数据，不记录敏感信息）。

## 7. API 契约（proto → `make proto-gen`）

```
UpdateModelInput    + sampling_params JSONB（0=unset）
                    + max_temperature *float64（NULL=全局契约[0,1]；0=不支持）
UpdateProviderInput + extra_headers（key→value）
                    + default_sampling JSONB（回退层）
```

- `ProviderManaged` 模型：发现回写（UpsertDiscovered）不触碰 sampling_params/max_temperature；人工编辑允许（与 displayName 同语义）。
- `api/http/contract_test.go` golden 同步更新。

## 8. 安全边界（extra_headers）

- 写时校验：拒绝 `Authorization`/`Content-Type`/`User-Agent`/`X-API-Key` 等鉴权与内容头（黑名单），防管理员误配覆盖 API key 注入。
- 请求时合并：请求显式 header > provider extra_headers（同名以请求为先）。

## 9. 前端（product.md：首屏 ≤8 项、高级折叠）

- `ModelEditDrawer`：基础区保持现状；新增**高级设置折叠区**——采样参数（带标签 Slider，temperature 显示范围）、max_temperature 说明位（tooltip：0=不支持）、编辑后运行时语义说明（「请求未显式配置时生效；超上限自动 clamp/拒绝」）。
- `ProviderForm`：扩展高级区——额外 headers 键值编辑、默认采样参数。
- `ModelListPage`：不新增列（≤5 列原则），tooltip 呈现。

## 10. 对现有配置的冲击梳理

| 现有配置点 | 影响 |
|---|---|
| `agent.temperature`（registry，0.7） | 不变——显式配置优先级最高；模型默认注入仅在 agent=0 时生效 |
| `agent.max_tokens`（registry，≤131072） | 请求超模型上限 → clamp（新拦截）；=0 未设置时模型 max_tokens 作兜底（替代 4096 常量；08-11 账本 outputReserve 同步生效） |
| 08-11 窗口链（registry→vendor 表→UNKNOWN） | 不动；L2 预检仅新增「已知窗口估算超限 → 拒绝」最终防线，估算失败 fail-open 交给 provider |
| 08-13 `model_profiles`（未落地） | 本次不做——memory 机制收口是独立待办，边界注明 |
| 超时/重试/熔断 | 不开放配置，保持系统常量 |
| memory 管线（enrich/summary 平台参数） | 不变——不涉模型目录字段 |

## 11. 测试策略

- 表驱动单测：clamp 边界（=上限/超上限/未知 0）、UNKNOWN 不 clamp、max_temperature 越界拒绝、能力不匹配拒绝、header 合并与安全头拒绝、缓存失效后策略生效。
- `api/http/contract_test.go` golden 更新；相关 test mock/stub 同步（port 扩展后立即搜索同步）。
- 系统验收走 `stratum-e2e-development`：编辑采样参数 → agent 执行时采纳；clamp 生效；provider 拦截错误 4xx。

## 12. 改动文件清单

| 文件 | 改动 |
|---|---|
| `pkg/migration/sql/038_model_editable_params.{up,down}.sql` | 新增迁移 |
| `internal/llmgateway/domain/model.go` | `Model.SamplingParams`（指针字段）、`MaxTemperature` |
| `internal/llmgateway/domain/provider.go` | `Provider.ExtraHeaders`、`DefaultSampling` |
| `internal/llmgateway/domain/port/model_repo.go` / `provider_repo.go` | SELECT/UPDATE 列扩展（逐列核对） |
| `internal/llmgateway/infrastructure/model_repo.go` / `provider_repo.go` | SQL 列同步 |
| `internal/llmgateway/infrastructure/model_registry.go` | `resolvedEntry.policy`、warm 预计算 |
| `internal/llmgateway/infrastructure/openai_compat.go` | `enforceModelPolicy` 执行点（Complete 前） |
| `internal/llmgateway/infrastructure/errors.go` | 语义化拦截错误（永久标记） |
| `internal/llmgateway/application/model_mgmt_service.go` / `provider_service.go` | 更新输入扩展 |
| `api/http/handler/model_mgmt_handler.go` / `provider_handler.go` | DTO 绑定 |
| `proto/` → `make proto-gen` | 契约扩展 |
| `web/src/modules/llm/` | Drawer/Form 高级折叠区、类型扩展 |
| `api/http/contract_test.go` + testdata golden | 契约断言 |
| 各 repository 测试 mock | port 扩展同步 |
