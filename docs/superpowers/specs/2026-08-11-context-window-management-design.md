# 上下文窗口管理统一设计（2026-08-11）

## 背景与问题

**症状**：agent 对话响应慢，从第一轮就开始慢。

**根因链**（已通过源码与运行证据确认）：

1. **压缩阈值被工具 token 压垮**：`compactLoopMessagesWithPolicy` 的 lazy 检查
   `EstimateMessages <= compactionThreshold(budget, reservedTokens, ...)`，阈值
   = budget×safety/correction − reservedTokens。工具定义（常达数万 token）先占预算，
   history 可压缩区被挤到极小 → 几乎每步都触发 LLM 摘要。
2. **无冷却**：压缩触发后同一轮循环内后续步骤立刻再次触发 → 每步一次同步 LLM 摘要。
3. **压缩走 gateway fallback 链**：`LLMHistoryCompactor.CompactHistory` → `gw.Route`
   → 主模型重试 1 次 + 候选 3 个逐个尝试，全在 5s budget 内串行。
4. **`context.DeadlineExceeded` 被当瞬态**（`errors.go:52`）：5s budget 耗尽后
   `invokeCandidate` 判定"可重试" → 主模型空转重试 + 降级链逐个空转，每次空转都产生
   日志、指标和候选链 DB List 开销。
5. **maxContextTokens 三个断层**：显式配置不校验（可超模型真实窗口）；
   `DefaultAgentContextTokensCeiling`(32768) 与模型无关；推导在 Create/Update 时
   一次性固化，模型窗口变更不生效。

**目标**：把"单次请求窗口"与"整个执行"的上下文管理统一为一次执行一次记账的预算
账本，压缩成为有界、尽力而为、可观测的机制；消除空转重试与无冷却压缩。

## 决策记录（已获批）

| # | 决策 | 理由 |
|---|---|---|
| D1 | 压缩使用**非流式** | 产物用户不可见；非流式天然"要么完整要么失败"，截断摘要比 breadcrumb 更差；5s budget 已是最紧超时，流式 idle 30s/90s 无额外价值 |
| D2 | 压缩模型 = 主对话模型（不新增独立配置） | wiring 已确认 `HistoryCompactorFactory` 传 `LLMModel`；压缩失败大概率意味着主模型也有问题，快速失败正确 |
| D3 | 执行**不设整体 deadline** | 时间有界 = MaxSteps × 单点超时；成本有界 = 成本预算；整体 deadline 会杀死进行中的执行（快完成时被砍，成本白费）。HTTP/LB 层外部超时是外部契约，不动 |
| D4 | 最终请求 `context_length_exceeded` → **降级最小请求重试一次** | 循环已结束、工具成本已花；降级请求必然更小、成功率高；非退避、非换模型、只一次，不违反"不空转" |
| D5 | `context.DeadlineExceeded` 从瞬态改**永久** | 超时 = 等待无意义，继续试只叠加时延；改后 gateway 链立即 stop，消除空转 |
| D6 | 成本预算用 **token 总量**而非美元 | 与 Ledger 已记账的 `TotalTokens` 对齐，不依赖价格表，无供应商耦合 |
| D7 | 窗口回退链中 **UNKNOWN 不 clamp 显式配置** | 显式配置是最可信信息，未知假设无权压制它；已知窗口（registry/内置表）才是硬上限 |
| D8 | 内置厂商窗口表只覆盖主流模型族 | 少数派模型走保守默认 + correction 自我修正；表维护成本低 |

## 第 1 节：窗口来源与钳制

### 两阶段解析（执行时动态解析，替代"推导一次性固化"）

```
阶段 A：解析模型真实窗口 effectiveModelWindow
  registry 模型记录 context_window > 0
  → vendor 内置表（前缀匹配主流模型族：qwen/zhipu/deepseek/gpt 等）
  → UNKNOWN

阶段 B：解析 agent 执行窗口
  显式配置 max_context_tokens > 0：
    effectiveModelWindow 已知 → clamp(显式值, [min, w×0.85])
    UNKNOWN                    → 显式值直接生效（D7）
  未配置：
    effectiveModelWindow 已知 → w×0.85
    UNKNOWN                    → 保守默认 8000
```

- 每次执行解析，不固化到 agent 记录；管理员后补配置**下次执行立即生效**。
- clamp 结果写 trace attribute `window_source: explicit|registry|vendor_table|fallback`
  和解析出的窗口值；来源为 vendor_table/fallback 时 WARN 日志。
- 未知窗口安全兜底：若估算超过真实窗口 → 400 context_length_exceeded →
  `TokenCorrection` 自动下修压缩阈值 → 自我收敛，不持续失败。
- 移除 `DefaultAgentContextTokensCeiling`(32768) 的模型无关 cap，由 1M 硬 ceiling 替代。

## 第 2 节：执行级预算账本（Budget Ledger）

一次执行一个预算快照，在初始组装与 ReAct 循环内共享：

```
window    = 阶段 B 解析结果（1M 硬 ceiling）
usable    = window − safetyReserve − outputReserve
             safetyReserve    = window × ContextSafetyReserveRatio（固定平台默认 0.2，不暴露用户配置；2026-08-17 产品裁决移除 compaction_safety_ratio 参数）
             outputReserve    = 主模型 MaxTokens（响应预留，入账本）
配额：
  fixedHead  system + memory   ≤ 20% usable（优先，min 200t 起步）
  tools      工具定义          ≤ 20% usable
  history    可压缩区          = usable − fixedHead − tools − task（压缩目标）
  task       当前用户输入      永不压缩
```

- `BuildContextMessagesWithCompaction` 与 `compactLoopMessagesWithPolicy` 统一使用
  同一账本来源，消除两套互不知晓的预算机制。
- 压缩阈值不再被工具 token 挤占：tools 配额与 history 配额分离，阈值回到
  history 可压缩区的真实比例。
- outputReserve 入账本后，主模型响应的输出预算不再占用压缩阈值计算的历史区。

## 第 3 节：执行约束（替代整体 deadline）

```
1. 单点超时矩阵（已有，不动）：
   主对话非流式 60s flat / 流式 dial 10s + TLS 10s + header 30s + idle 30s
   压缩 5s（min(5s, remaining/2)）/ 内存注入 AgentMemoryInjectTimeout / DB AgentDBQueryTimeout
2. 步数上限 MaxSteps（已有，不动）：MaxLLMSteps → s.Steps >= MaxLLMSteps-1 强制回答
3. 成本预算【新增】：agent.max_tokens_per_execution（registry 参数，0 = 不设限）
   图级每次 LLM 调用后累计 Ledger（TotalTokens），超限 → 终止循环 + 返回已产出部分
   + trace terminated_by: cost_budget；属业务终止，非错误路径
```

时间最坏 = MaxSteps × 单点超时（有界）；成本最坏 = 成本预算（有界）。

## 第 4 节：压缩触发、冷却与尽力而为

- **阈值修正**：compactionThreshold 基于第 2 节账本的 history 配额计算，
  工具 token 不再压垮阈值。
- **冷却**：一次执行内压缩触发后进入冷却窗口（常量进 `pkg/constants`，
  建议默认 10s，实现时按压测验证；registry 参数位 `agent.compaction_cooldown_sec`，
  0 = 默认常量），冷却内不重复触发同步 LLM 摘要。
- **尽力而为**：压缩失败 → breadcrumb `[已省略 %d 轮较早对话...]`，循环不被阻断。
- **动态时间片**（保留 fallback 容灾但压缩不放大用户时延）：

```
BudgetPolicy { Total: 5s, NoPrimaryRetry: true, MaxCandidates: 2 }
每次尝试 slice = remaining / remaining_attempts（各自独立 ctx）
主模型瞬态失败：不做 gateway 层立即重试（NoPrimaryRetry），直接降级候选
候选依次尝试：每个 slice 预算内
链耗尽 → markPermanent → 压缩快速失败 → breadcrumb
```

- 压缩协议：非流式（D1），模型 = 主对话模型（D2）。

## 第 5 节：错误处理

### 错误分类统一表

| 错误 | 分类 | 行为 | 落点 |
|---|---|---|---|
| `context.Canceled` | 永久 | 立即停链，不重试不降级 | 现状已符合，不动 |
| `context.DeadlineExceeded` | **永久（改）** | 立即停链，不重试不降级 | errors.go `isTransient` 改一行；gateway 链自动 stop |
| `context_length_exceeded` | **永久 + 语义化（新）** | 显式错误类型，agent 层可感知 | 协议层识别 400 error code → `ErrContextLengthExceeded` |
| 429 / 503 / 5xx / 网络抖动 | 瞬态 | 双层退避重试（图级 RetryFn + 协议层 jitter） | 已有，不动 |

### 最终请求 context_length_exceeded（D4）

```
循环内（未结束）：400 → correction 下修阈值 → 下一轮提前压缩（已有闭环）
循环结束（最终请求）：第一次 400 context_length_exceeded
  → 降级最小请求重试一次：system + task + 压缩后历史，剔除全部工具结果
  → 成功：返回答案；仍失败：终止
参数校验类 400（schema 等）：直接终止，错误信息明确化（是 bug，重试无意义）
```

### 成本预算超限

业务终止：返回已产出部分结果 + trace `terminated_by: cost_budget`，不进错误路径。

## 第 6 节：测试与成功标准

**成功标准（可验收）**：

1. 症状回归：长对话压缩触发从"每步同步 LLM 摘要"变为"按预算 + 冷却触发"
2. 正确性：压缩后早期关键事实仍在最终回答中（语义断言，非字数断言）
3. fail-fast：`isTransient(context.DeadlineExceeded) == false`（单测锁定）
4. 窗口链：explicit > registry > vendor 表 > 8000；UNKNOWN 不 clamp 显式值
5. 成本预算：累计超限终止 + 部分结果返回 + 非错误路径
6. 时间片：BudgetPolicy slice 计算（含剩余 0 边界）

**测试分层**：

- 单测（表驱动）：
  - `errors_test.go`：三分类判定表（Canceled/DeadlineExceeded/context_length/429/5xx/网络）
  - `window_test.go`：回退链五分支（显式+已知 / 显式+UNKNOWN / registry / vendor 表 / 全空）
  - `compaction_test.go`：阈值（tools 配额分离）+ 冷却窗口内不二次触发
  - `budget_test.go`：成本账本累计 + 超限终止检查点
  - `timeslice_test.go`：slice 计算
- 集成：gateway 层——DeadlineExceeded 不降级；503 降级到候选（扩展现有 fallback 测试）；
  最终请求 400 → 降级最小请求重试 → 成功/仍失败两条路径
- E2E（stratum-e2e-development）：长对话 → 压缩发生 → 早期事实仍在 → 无每步压缩

**回归保护**：DeadlineExceeded 改永久影响所有 LLM 调用路径（主对话、压缩、evaluation），
变更点单测锁定 + 全量 `-race` 回归。

## 影响面

### 改动文件

| 文件 | 改动 |
|---|---|
| `internal/llmgateway/infrastructure/errors.go` | `isTransient`: DeadlineExceeded → 永久；新增 `ErrContextLengthExceeded` 识别 |
| `internal/llmgateway/infrastructure/openai_compat.go` | 400 error code 解析 → 语义化错误 |
| `internal/agent/application/agent_service.go` | 删除 `deriveMaxContextTokens` 固化逻辑；执行时两阶段解析 |
| `internal/agent/application/graph/compaction.go` | 阈值基于账本 history 配额；冷却；降级最小请求重试 |
| `internal/agent/application/graph/retry.go` / `react_llm.go` | 成本预算检查点；错误分类协同 |
| `internal/agent/application/context_budget.go` | 统一账本来源（fixedHead/tools/history/task 配额） |
| `internal/agent/application/agent.go` | 执行时窗口解析接入；成本预算接线 |
| `internal/agent/infrastructure/capability/history_compactor.go` | BudgetPolicy 时间片 |
| `internal/llmgateway/infrastructure/gateway.go` | 压缩路径 NoPrimaryRetry；错误分类协同 |
| `internal/parameters/domain/registry.go` | 新增 `agent.max_tokens_per_execution`；`agent.compaction_cooldown_sec`（0=默认常量） |
| `pkg/constants/` | 冷却常量；移除模型无关 32768 cap |
| `internal/agent/domain/agent.go` | AgentConfig 新增成本预算字段（0=unset） |
| vendor 窗口表（新文件） | 主流模型族 context_window 静态表 |

### 新文件

- `internal/agent/application/graph/window.go`：两阶段窗口解析
- `internal/agent/application/graph/budget.go`：执行级预算账本
- `internal/agent/application/graph/cost_budget.go`：成本预算检查点
- vendor 窗口表（见上）

### 不变

- 压缩非流式、压缩模型 = 主对话模型、单点超时矩阵、步数上限、双层退避、
  前端/HTTP/LB 外部超时、冻结响应体兼容性。
