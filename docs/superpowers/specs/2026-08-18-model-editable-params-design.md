# 模型管理可编辑参数设计：观测能力、运营策略与运行时默认值分离

日期：2026-08-18
状态：设计已获批，待实现计划
范围：基于 `feat/model-editable-params` 已有提交进行增量修正；本 spec 不授权直接实现或部署。

关联设计：

- `2026-08-11-context-window-management-design.md`
- `2026-08-13-model-management-refactor-design.md`
- `2026-08-14-audit-resource-change-design.md`
- `035_platform_model_catalog.up.sql`

## 1. 背景与问题

现有提交已经建立了 public `providers/models` 目录、`ModelRegistry` 缓存、Gateway per-link 策略和采样字段，但仍有四类语义冲突：

1. `models.context_window` 和 `models.max_tokens` 同时被当作 discovery 结果和管理员可编辑值；再次 discovery 会覆盖人工设置。
2. 最大输出能力与“未设置时的默认输出长度”混为一个字段，可能把原有 4096 默认请求放大到模型最大能力。
3. Gateway 使用 `bytes/3` 估算消息 token，并把输入估算与输出预算相加后直接返回 4xx；当前字段语义和估算精度不足以承担最终拒绝。
4. public 全局目录的写接口仍使用 tenant admin 权限，任一租户管理员都可能影响全平台模型和 provider。

本设计不推翻已有提交，而是明确字段职责、调整运行时采纳链，并拆出独立的 API、权限和安全边界。

## 2. 目标与非目标

### 目标

1. discovery/registry 能力事实、管理员运营策略和请求默认值互不覆盖。
2. Gateway 保持单一运行时采纳点，并对每个 fallback link 使用自己的 policy。
3. `max_tokens` 的最大能力、默认输出长度和 Agent 预算预留保持一致。
4. 模型策略变更立即通过 registry cache invalidation 生效。
5. public 资源由 global admin 写入，平台级变更有可查询、脱敏审计。
6. API 能明确表达“未传、清除、设置”三种更新语义。

### 非目标

- 本次不开放超时、重试、熔断、限流或 fallback 拓扑配置。
- 本次不实现 `model_profiles` 行为档案收口。
- 不把粗略 token 估算升级为 Gateway 最终能力裁决器。
- `extra_headers` 作为独立安全特性评审，不与模型参数功能绑定发布。

## 3. 已有提交的保留范围

以下实现作为基线保留：

- public `providers/models` 目录及 035 迁移；
- `ModelRegistry` 的解析链、缓存和 `Invalidate()`；
- Gateway `invoke()` 内 per-link 策略执行点；
- `sampling_params`、`max_temperature` 的领域校验和迁移基础；
- 资源变更审计模型、脱敏投影和相关测试骨架。

以下行为必须在实现阶段重构：

- 直接编辑并由 discovery 覆盖 `context_window/max_tokens`；
- Gateway 内 `bytes/3` 的上下文硬拒绝；
- 未设置 `max_tokens` 时直接注入模型最大能力；
- tenant admin 修改 public 全局资源；
- 使用普通指针字段表达三态 API 语义。

## 4. 数据模型

### 4.1 观测能力

现有字段保留为 provider/registry 观测事实：

```text
models.context_window
models.max_tokens
```

discovery 可以更新这两个字段。模型管理 UI 将其作为只读事实展示并显示来源。

### 4.2 运营策略

新增 nullable 字段：

```sql
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    operator_context_window INT;

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    operator_max_tokens INT;

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    default_output_tokens INT;
```

语义：

- `NULL`：没有运营覆盖；
- 正整数：设置运营策略；
- `0` 或负数：写入拒绝，不作为“未知”编码；
- discovery 只更新观测字段，不修改以上三个字段。

### 4.3 采样策略

已有字段继续使用：

```text
models.sampling_params
models.max_temperature
providers.default_sampling
```

`sampling_params` 使用强类型结构；禁止以 `map[string]any` 作为对外契约。模型级默认优先于 provider 级默认。

## 5. 有效策略解析

对每个模型计算不可变的 `EffectiveModelPolicy`：

```text
effective_context_window:
  operator_context_window > 0 && observed_context_window > 0
      -> min(operator_context_window, observed_context_window)
  operator_context_window > 0 && observed_context_window == 0
      -> operator_context_window，并标记 source=manual_unknown
  operator_context_window == NULL
      -> observed_context_window

effective_max_output:
  operator_max_tokens > 0 && observed_max_tokens > 0
      -> min(operator_max_tokens, observed_max_tokens)
  operator_max_tokens > 0 && observed_max_tokens == 0
      -> operator_max_tokens，并标记 source=manual_unknown
  operator_max_tokens == NULL
      -> observed_max_tokens
```

运营策略只允许收紧已知 provider 能力。若观测值未知而管理员提供手工上限，必须产生审计和风险指标。

`default_output_tokens` 不等于 `effective_max_output`：

- 前者是未显式设置时的默认请求预算；
- 后者是 provider 能力或运营策略的硬上限。

## 6. Gateway 采纳链

Gateway 在 `invoke()` 内对每个 fallback link 执行：

```text
1. 将 request model 替换为当前 link model
2. 读取该 link 的 EffectiveModelPolicy
3. 选择 max_tokens：
   显式请求值
   -> default_output_tokens
   -> provider protocol default
4. 对有效值应用 effective_max_output 上限
5. 注入采样默认值：
   请求值 -> 模型级默认 -> provider 级默认 -> 不注入
6. 校验采样范围和能力约束
7. 调用 provider
```

兼容规则：

- 显式 `max_tokens` 超过硬上限时，暂时沿用已有 clamp 行为，并记录 `policy_adjusted` 指标。
- 未显式设置时不得直接注入模型最大能力。
- reasoning floor 保留，但最终仍受 `effective_max_output` 限制。
- 每个 fallback link 独立计算 policy，不复用主模型 policy。
- policy 缺失时不伪造默认能力；跳过本地增强策略并记录 WARN/指标，由 provider 返回最终错误。

## 7. 上下文窗口与预算

- `effective_context_window` 进入 Agent 预算账本和压缩阈值计算。
- 删除 Gateway 中基于 `bytes/3` 的最终 4xx 预检。
- provider 返回的 `context_length_exceeded` 仍转换为语义化错误。
- 沿用最终请求最小化后仅重试一次的恢复路径。
- `context_window=0` 表示未知，不 clamp 显式 Agent 配置，但记录来源和风险指标。
- 预算账本的 output reserve 必须使用与 Gateway 相同的有效默认/上限解析结果，禁止出现“发送 8192、预算只预留 4096”的分叉。

## 8. API 契约

模型资料和运行策略分离：

```text
PUT   /admin/models/:id                 # display/capabilities/pricing 等资料
PATCH /admin/models/:id/policy          # operator limits/default/sampling
PATCH /admin/providers/:id/defaults     # provider sampling/defaults
```

策略字段使用显式三态 wrapper：

```text
字段缺省    = 保留原值
字段为 null = 清除覆盖，回退默认/观测值
字段有值    = 设置新值
```

map 字段：

```text
缺省 = 保留
null 或 {} = 清空
```

普通 `*map`、`*float64` 不足以表达以上语义；DTO 必须使用 presence/null wrapper 或 `json.RawMessage` 做显式解析。

响应投影：

- 观测能力、运营策略、来源和是否覆盖可返回；
- `extra_headers` 只返回 key 列表与配置状态，不返回值；
- sampling 参数为非凭据，可返回强类型值；
- API key 永不返回。

## 9. 权限与审计

### 权限

- 模型目录读取：租户成员可读；
- public provider/model 写入：仅 `global_admin`；
- tenant admin 不得修改 public 全局模型能力、provider 凭据或全局策略；
- 若未来需要租户自定义，新增 tenant-scoped policy，不复用 public 字段。

### 审计

平台级变更记录：

```text
scope        = platform
resource     = provider | model
actor_id     = 操作者
actor_tenant = 操作者租户，可为空但必须显式表达
before/after = 脱敏投影
```

API key、header 值不进入审计。public 资源写入和审计必须同一事务提交；不得伪造默认 tenant ID。

## 10. Extra Headers 边界

`extra_headers` 不与本功能一起发布。若后续保留：

- 采用 provider-specific allowlist，不能只依赖通用黑名单；
- 限制数量、名称长度和值长度；
- 拒绝控制字符、hop-by-hop、代理路由和鉴权覆盖 headers；
- provider 自身鉴权头最后写入；
- 值不得进入日志、错误、审计或响应；
- 明确 chat、discover、health 等端点的携带范围；
- 使用真实 provider 请求完成脱敏 E2E。

## 11. 迁移与兼容

1. 新增 operator/default 字段，默认 `NULL`，不改变现有运行时行为。
2. 回填前确认历史 `context_window/max_tokens` 是观测值，不把旧人工编辑误判为运营策略。
3. discovery 更新观测字段并保留 operator/default 字段。
4. registry policy cache 扩展为 `EffectiveModelPolicy`，变更后全量失效。
5. 旧 API 在迁移期只允许编辑资料字段；策略改用新 PATCH 接口。
6. 所有迁移使用 `IF NOT EXISTS`，历史租户顺序和回滚测试必须覆盖。

## 12. 测试与验收

### 单元/集成

- 观测能力更新不覆盖 operator/default；
- effective policy 的 known/unknown/min 规则；
- 显式值、默认值、协议默认和硬上限优先级；
- fallback link 使用独立 policy；
- sampling model → provider → unset 回退；
- policy 缺失指标与错误传播；
- API 缺省/null/value 三态；
- max output 与 default output 不混淆；
- cache invalidation 后立即生效；
- global admin/tenant admin 权限矩阵；
- platform audit 脱敏与事务回滚。

### 系统验收

使用 `stratum-e2e-development` 验证：

1. discovery 写入观测能力；
2. 管理员设置 operator policy；
3. 再次 discovery 后 operator policy 仍保留；
4. Agent 执行使用 effective window/output；
5. fallback 候选按自身 policy 执行；
6. tenant admin 写入被拒；
7. global admin 写入、审计和缓存失效闭环。

## 13. 分阶段落地

1. 数据语义与迁移；
2. EffectiveModelPolicy 与 Gateway 采纳链；
3. API 三态契约、权限和审计；
4. 前端观测值/运营策略分层；
5. Extra Headers 独立安全设计；
6. 系统 E2E 与发布验收。

在第 1–2 阶段完成并验证前，不开放新的 UI 编辑入口，也不继续扩大现有分支的实现范围。
