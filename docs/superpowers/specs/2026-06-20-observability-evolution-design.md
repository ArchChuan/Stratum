# Stratum 指标监控体系设计

**日期**: 2026-06-20
**状态**: 待实施
**目标**: 构建完善的可观测性体系，为自进化机制（参数自适应 / 模型切换 / Prompt 优化）提供数据燃料

---

## 背景与现状

### 已有基础

- `pkg/observability/PrometheusMetrics`：覆盖 HTTP / Skill / Agent / LLM / Knowledge / Memory / Hermes
- `internal/memory/infrastructure/pipeline/metrics.go`：outbox / embed / enrich / summary / DLQ / entities
- OpenTelemetry 链路追踪已接入，span 传播完整
- `MetricsProvider` interface + `NoopMetrics`，测试友好

### 现有缺口

| 缺口 | 影响 |
|------|------|
| `RecordLLMFirstTokenLatency` 未纳入 interface | 流式指标无法 mock，接口不完整 |
| 无 SLO / Error Budget 定义 | 告警噪音大，自进化触发不可靠 |
| 无用户显式反馈信号 | Prompt 优化路径缺核心训练信号 |
| LLM span 用自定义属性而非 OTel Gen AI 规范 | 接入 Grafana / Datadog 有兼容问题 |
| 高基数标签（tool_name / path / skill_id）无管理策略 | Prometheus 内存风险 |
| Histogram 无 Exemplar | 告警触发后无法一键跳转 trace |
| 无 ReAct 工具调用质量指标 | 无法检测工具调用效率问题 |
| 无 per-tenant LLM cost 追踪 | 无法驱动模型切换决策 |

---

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 存储方式 | Prometheus 实时 + PG 质量样本 | 实时告警与结构化分析分工明确 |
| 样本隔离 | `public` schema + `tenant_id` 列 | 支持跨租户全局基线，管理简单 |
| 执行边界 | 分级：低风险自动执行，高风险待审批 | 安全与效率平衡 |
| 告警策略 | SLO + multi-burn-rate | 替代裸阈值，减少噪音 |

---

## Section 1：MetricsProvider 接口扩充

### 新增方法

```go
// internal interface 扩充（pkg/observability/provider.go）

// LLM — 升入 interface（原有实现，现正式纳入）
RecordLLMFirstTokenLatency(model, provider string, latency float64)

// LLM — 新增
RecordLLMCostUnits(model, provider, tenantID string, units float64)
IncLLMErrorByType(model, provider, errorType string)
// errorType: rate_limit | timeout | content_filter | context_length | unknown

// ReAct 工具调用质量
IncAgentToolCall(agentID, toolName, status string)
// toolName 经 sanitizeToolName 截断，status: success | error
RecordAgentToolCallDuration(agentID, toolName string, durationSeconds float64)
RecordAgentContextUtilization(agentID string, ratio float64)
// ratio = actual_tokens / max_context_tokens
IncAgentIterationsExhausted(agentID string)

// 记忆质量
IncMemoryRecallHit(tenantID, strategy string)
// strategy: vector | text
IncMemoryRecallMiss(tenantID, strategy string)
RecordMemoryRecallResultCount(tenantID string, count float64)
```

### 高基数标签截断策略

```go
// pkg/observability/sanitize.go

var builtinToolRE = regexp.MustCompile(
    `^(stratum_recall_memory|stratum_search_knowledge|stratum_run_skill)$`,
)

// SanitizeToolName 将非内置工具名归为 _other，防止高基数爆炸
func SanitizeToolName(name string) string {
    if builtinToolRE.MatchString(name) {
        return name
    }
    return "_other"
}

var uuidSegRE = regexp.MustCompile(
    `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`,
)

// SanitizePath 将 HTTP path 中的 UUID 替换为 :id，防止路由基数爆炸
func SanitizePath(path string) string {
    return uuidSegRE.ReplaceAllString(path, ":id")
}
```

`skill_id` 标签改用 `skill_type`（`llm` / `retrieval` / `action`）；per-skill 详细指标只写 `evolution_samples`。

### Exemplar 注入

`llm_request_duration_seconds` 和 `agent_execution_duration_seconds` 在记录时注入 `trace_id`：

```go
func (m *PrometheusMetrics) RecordLLMRequestDuration(
    model, provider string, dur float64,
) {
    m.llmRequestDuration.With(labels).
        ObserveWithExemplar(dur, prometheus.Labels{
            "trace_id": m.currentTraceID(), // 从 context 取，无则空串
        })
}
```

Grafana 开启 Exemplar 后可从 histogram 高延迟点一键跳转 Tempo trace。

---

## Section 2：OTel Gen AI 语义约定

LLM span 属性迁移至 OTel `gen_ai.*` 命名空间（`pkg/observability/trace.go`）：

| 旧字段 | 新属性 |
|--------|--------|
| `model` | `gen_ai.request.model` |
| `provider` | `gen_ai.system` |
| `prompt_tokens` | `gen_ai.usage.input_tokens` |
| `completion_tokens` | `gen_ai.usage.output_tokens` |
| `latency_ms` | `gen_ai.client.operation.duration` |
| — | `gen_ai.request.temperature`（新增）|
| — | `gen_ai.response.finish_reason`（新增）|

`finish_reason` 取值：`stop`（正常）/ `length`（context 截断）/ `content_filter` / `tool_calls`。
自进化规则 B（模型切换）将 `length` 频率纳入触发条件。

---

## Section 3：SLO + Error Budget + Alerting

### SLO 定义

| SLO 名称 | 目标 | 窗口 |
|----------|------|------|
| agent.success_rate | 95% | 28d rolling |
| agent.latency_p99 | < 10s | 28d rolling |
| llm.error_rate | < 1% | 28d rolling |
| skill.availability | 99% | 28d rolling |
| memory.recall_latency_p95 | < 500ms | 7d rolling |

### Prometheus Recording Rules

```yaml
# deploy/prometheus/rules/slo.yaml
groups:
  - name: slo_recording
    interval: 30s
    rules:
      - record: job:agent_success_rate:ratio_rate5m
        expr: |
          sum(rate(agent_executions_total{status="success"}[5m]))
          / sum(rate(agent_executions_total[5m]))

      - record: job:agent_success_rate:ratio_rate1h
        expr: |
          sum(rate(agent_executions_total{status="success"}[1h]))
          / sum(rate(agent_executions_total[1h]))

      - record: job:agent_success_rate:ratio_rate6h
        expr: |
          sum(rate(agent_executions_total{status="success"}[6h]))
          / sum(rate(agent_executions_total[6h]))

      - record: job:agent_success_rate:error_budget_burn_1h
        expr: (1 - job:agent_success_rate:ratio_rate1h) / (1 - 0.95)

      - record: job:agent_success_rate:error_budget_burn_6h
        expr: (1 - job:agent_success_rate:ratio_rate6h) / (1 - 0.95)

      - record: job:llm_error_rate:ratio_rate5m
        expr: |
          sum(rate(llm_requests_total{status=~"error|timeout"}[5m]))
          / sum(rate(llm_requests_total[5m]))

      - record: job:llm_error_rate:error_budget_burn_1h
        expr: |
          (sum(rate(llm_requests_total{status=~"error|timeout"}[1h]))
          / sum(rate(llm_requests_total[1h]))) / 0.01
```

### Multi-Burn-Rate Alerting

```yaml
  - name: slo_alerts
    rules:
      - alert: AgentSuccessRateSLOBurn
        expr: |
          job:agent_success_rate:error_budget_burn_1h > 14.4
          and job:agent_success_rate:error_budget_burn_6h > 6
        for: 2m
        labels:
          severity: critical
          evolution_trigger: param_adapt
        annotations:
          summary: "Agent success SLO 快速消耗 — error budget 将在 1h 内耗尽"

      - alert: AgentSuccessRateSLOBurnSlow
        expr: |
          job:agent_success_rate:error_budget_burn_1h > 3
          and job:agent_success_rate:error_budget_burn_6h > 1.5
        for: 15m
        labels:
          severity: warning
          evolution_trigger: param_adapt
        annotations:
          summary: "Agent success SLO 缓慢消耗 — 3 天内 error budget 耗尽"

      - alert: LLMErrorRateSLOBurn
        expr: job:llm_error_rate:error_budget_burn_1h > 14.4
        for: 2m
        labels:
          severity: critical
          evolution_trigger: model_switch
```

`evolution_trigger` label 供 `EvolutionService` 订阅 Alertmanager webhook 使用。

---

## Section 4：ExecutionSample 事件 + PG Schema

### NATS 事件（subject: `evolution.execution.sampled`）

```go
// internal/evolution/domain/sample.go

type OutcomeSignal string

const (
    OutcomeSuccess              OutcomeSignal = "success"
    OutcomeError                OutcomeSignal = "error"
    OutcomeTimeout              OutcomeSignal = "timeout"
    OutcomeIterationsExhausted  OutcomeSignal = "iterations_exhausted"
)

type ExecutionSample struct {
    SampleID        string        `json:"sample_id"`
    TenantID        string        `json:"tenant_id"`
    AgentID         string        `json:"agent_id"`
    ConversationID  string        `json:"conversation_id"`
    TraceID         string        `json:"trace_id"`
    Model           string        `json:"model"`
    Provider        string        `json:"provider"`
    InputTokens     int           `json:"input_tokens"`
    OutputTokens    int           `json:"output_tokens"`
    // CostUnits = (input_tokens * input_price + output_tokens * output_price) / 1000
    // price 来自 pkg/constants/llm_pricing.go，单位为 USD/1K tokens
    CostUnits       float64       `json:"cost_units"`
    Steps           []StepRecord  `json:"steps"`
    TotalDurationMs int           `json:"total_duration_ms"`
    IterationsUsed  int           `json:"iterations_used"`
    IterationsMax   int           `json:"iterations_max"`
    Outcome         OutcomeSignal `json:"outcome"`
    ErrorType       string        `json:"error_type,omitempty"`
    FinishReason    string        `json:"finish_reason,omitempty"`
    SystemPromptID  string        `json:"system_prompt_id,omitempty"`
    UserFeedback    string        `json:"user_feedback,omitempty"` // 异步回填
    CreatedAt       time.Time     `json:"created_at"`
}

type StepRecord struct {
    StepIndex  int    `json:"step_index"`
    ToolName   string `json:"tool_name"`
    InputHash  string `json:"input_hash"`  // SHA256 前 8 位，不存原文
    Status     string `json:"status"`      // success | error | skip
    DurationMs int    `json:"duration_ms"`
}
```

发送时机：`AgentService.ExecuteStream` 完成回调中 `go publishSample(ctx, sample)`，异步非阻塞，失败静默（不影响主流程）。

### 用户反馈回填

```
POST /agents/:id/executions/:trace_id/feedback
Body: { "signal": "positive" | "negative" }
```

前端：对话结束后展示 👍/👎；会话中途关闭或触发流式中断时自动发送 `abandoned` 信号。
Handler 执行：`UPDATE evolution_samples SET user_feedback = $1 WHERE trace_id = $2 AND tenant_id = $3`。

### PostgreSQL Schema（migration）

```sql
-- internal/migration/sql/NNN_evolution.sql

CREATE TABLE IF NOT EXISTS evolution_samples (
    id               UUID PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    agent_id         TEXT NOT NULL,
    trace_id         TEXT,
    model            TEXT NOT NULL,
    provider         TEXT NOT NULL,
    input_tokens     INT,
    output_tokens    INT,
    cost_units       NUMERIC(12,6),
    iterations_used  INT,
    iterations_max   INT,
    outcome          TEXT NOT NULL,
    error_type       TEXT,
    finish_reason    TEXT,
    system_prompt_id TEXT,
    user_feedback    TEXT,         -- positive | negative | abandoned | NULL
    steps            JSONB,
    created_at       TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_evo_samples_agent
    ON evolution_samples (tenant_id, agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_evo_samples_outcome
    ON evolution_samples (tenant_id, outcome, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_evo_samples_feedback
    ON evolution_samples (tenant_id, user_feedback, created_at DESC)
    WHERE user_feedback IS NOT NULL;

CREATE TABLE IF NOT EXISTS evolution_recommendations (
    id          UUID PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    agent_id    TEXT,
    rec_type    TEXT NOT NULL,              -- param_adapt | model_switch | prompt_upgrade
    risk_level  TEXT NOT NULL,              -- low | high
    status      TEXT NOT NULL DEFAULT 'pending',
    -- status: pending | auto_applied | approved | rejected
    payload     JSONB NOT NULL,             -- 具体变更内容
    evidence    JSONB NOT NULL,             -- 支撑数据（PromQL 结果 + 样本统计）
    confidence  NUMERIC(4,3),              -- 0.000~1.000
    applied_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_evo_recs_tenant
    ON evolution_recommendations (tenant_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS metric_snapshots (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    agent_id      TEXT,
    snapshot_date DATE NOT NULL,
    metric_name   TEXT NOT NULL,
    value         NUMERIC(20,6) NOT NULL,
    labels        JSONB,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (tenant_id, agent_id, snapshot_date, metric_name)
);
CREATE INDEX IF NOT EXISTS idx_metric_snapshots
    ON metric_snapshots (tenant_id, snapshot_date DESC);
```

---

## Section 5：EvolutionService 规则引擎

### Bounded Context 目录结构

```
internal/evolution/
├── domain/
│   ├── sample.go               # ExecutionSample, StepRecord, OutcomeSignal
│   ├── recommendation.go       # Recommendation, RecType, RiskLevel, Status
│   └── port/
│       ├── sample_repo.go      # SampleRepo（消费者侧 port）
│       ├── recommendation_repo.go
│       └── metrics_querier.go  # PromQL 查询抽象
├── application/
│   └── evolution_service.go    # 规则引擎主循环，实现 harness.Component
└── infrastructure/
    ├── persistence/
    │   ├── sample_repo.go
    │   └── recommendation_repo.go
    └── promql/
        └── querier.go          # Prometheus HTTP API 客户端
```

依赖方向：`evolution/application` 只 import `evolution/domain/port`，不 import `agent/application` 或 `memory/application`。`api/wiring/evolution.go` 负责装配并注入 `AgentConfigWriter`（消费者侧 port，由 `agent/infrastructure` 实现）。

### 规则引擎主循环

```go
// application/evolution_service.go

func (s *EvolutionService) runCycle(ctx context.Context) {
    for _, agentID := range s.listActiveAgents(ctx) {
        s.evalParamAdaptation(ctx, agentID)
        s.evalModelSwitch(ctx, agentID)
        s.evalPromptUpgrade(ctx, agentID)
    }
    s.snapshotMetrics(ctx) // 天级快照写 metric_snapshots
}
```

**规则 A — 参数自适应（低风险，自动执行）**

触发：最近 1000 条样本中 `iterations_exhausted` 比例 > 20%
动作：`max_iterations += 2`（上限 20），直接写 agent 配置，记录 `status=auto_applied`

```go
func (s *EvolutionService) evalParamAdaptation(ctx context.Context, agentID string) {
    rate := s.sampleRepo.ExhaustionRate(ctx, agentID, 1000)
    if rate < 0.20 {
        return
    }
    currentMax := s.agentConfigReader.GetMaxIterations(ctx, agentID)
    newMax := min(currentMax+2, 20)
    s.agentConfigWriter.SetMaxIterations(ctx, agentID, newMax)
    s.recRepo.Save(ctx, &domain.Recommendation{
        AgentID:    agentID,
        RecType:    domain.RecParamAdapt,
        RiskLevel:  domain.RiskLow,
        Status:     domain.StatusAutoApplied,
        Payload:    map[string]any{"max_iterations": newMax, "previous": currentMax},
        Evidence:   map[string]any{"exhaustion_rate": rate, "sample_count": 1000},
        Confidence: 0.85,
    })
}
```

**规则 B — 模型切换（高风险，待审批）**

触发（任意一条）：

- `cost_units_per_success`（7 天均值）> 租户预算阈值 × 1.5
- Alertmanager 发送 `evolution_trigger=model_switch` webhook

动作：查 `evolution_samples` 找同类任务中成功率更高、成本更低的备选模型，写 `status=pending` Recommendation

**规则 C — Prompt 优化（高风险，待审批）**

触发：`outcome=success AND user_feedback='negative'` 样本在最近 7 天 > 50 条
动作：从这批样本的 `steps` JSONB 提取工具调用序列，构造优化语料摘要，写 `status=pending` Recommendation（管理员触发 LLM 生成 candidate prompt 后 A/B 评估）

### 分级执行边界

| 低风险（自动执行）| 高风险（pending → 管理员审批）|
|---|---|
| max_iterations ±2 | 模型切换 |
| temperature ±0.05 | system prompt 版本替换 |
| context_window 截断比例 ±10% | max_iterations 跳变（> ±5）|
| memory recall limit ±2 | skill circuit breaker 策略调整 |

### 管理员 API

```
GET  /admin/evolution/recommendations?status=pending&agent_id=:id
POST /admin/evolution/recommendations/:id/apply
POST /admin/evolution/recommendations/:id/reject
```

### Harness 注册

```go
// api/wiring/evolution.go
func buildEvolutionService(cfg *Config, pool *pgxpool.Pool, nc *nats.Conn, ...) *evolution.EvolutionService {
    svc := evolution.NewEvolutionService(
        persistence.NewSampleRepo(pool),
        persistence.NewRecommendationRepo(pool),
        promql.NewQuerier(cfg.PrometheusURL),
        agentConfigAdapter,  // wiring 层 thin adapter
        logger,
    )
    harness.Register(svc)   // 启动顺序：Prometheus + NATS consumer 之后
    return svc
}
```

---

## 数据流总览

```
用户对话
    │
    ▼
AgentService.ExecuteStream
    │── Prometheus 实时指标（各 IncXxx / RecordXxx）
    │── NATS: evolution.execution.sampled → EvolutionSampleConsumer → evolution_samples
    │
    ▼
前端 👍/👎 / abandoned
    │
    ▼
POST /executions/:trace_id/feedback
    │── UPDATE evolution_samples.user_feedback
    │
    ▼
EvolutionService（每 5 分钟）
    │── PromQL 查询 Recording Rules（SLO error budget burn rate）
    │── SQL 查询 evolution_samples（exhaustion_rate / cost / feedback）
    │── 规则 A → 低风险变更 → 直写 agent 配置 + auto_applied 记录
    │── 规则 B/C → 高风险 → pending Recommendation
    │
    ▼
管理员 Dashboard
    │── GET /admin/evolution/recommendations
    │── Apply → agent 配置变更生效 → 下一轮 ExecutionSample 采集验证效果
```

---

## 变更范围

| 文件 / 目录 | 变更类型 |
|---|---|
| `pkg/observability/provider.go` | 扩充 interface（+9 方法）|
| `pkg/observability/prometheus.go` | 实现新方法 + Exemplar 注入 |
| `pkg/observability/sanitize.go` | 新增（高基数截断工具）|
| `pkg/observability/noop.go` | 新增空实现 |
| `pkg/observability/trace.go` | LLM span 属性迁移至 gen_ai.* |
| `internal/evolution/` | 新增 bounded context（4 文件）|
| `internal/agent/application/agent_service.go` | 执行完成后异步发 ExecutionSample |
| `api/http/handler/agent_exec_handler.go` | 新增 feedback endpoint |
| `api/http/router.go` | 注册 feedback + evolution admin 路由 |
| `api/wiring/evolution.go` | 新增装配文件 |
| `internal/migration/sql/NNN_evolution.sql` | 三张表 DDL |
| `deploy/prometheus/rules/slo.yaml` | Recording rules + multi-burn-rate alerts |
| `web/src/modules/agent/` | 👍/👎 反馈组件 + abandoned 信号 |

---

## 未纳入本次设计（后续迭代）

- A/B 测试框架（candidate prompt 评估基础设施）
- 跨租户全局基线聚合表
- eBPF 基础设施层监控
- 离线批量语料训练 pipeline
