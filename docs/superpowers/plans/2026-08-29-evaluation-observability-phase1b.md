# Evaluation Observability Phase 1b Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 P1a 降级项并落地 Phase 1 剩余后端：规则护栏内联（快路径即时拦截）、行为信号两路采集、Stratum tier 解析、判异触发（识别+指标+通知）与 T1–T4 上升分级、飞书告警接入。

**Architecture:** 规则护栏内联到 agent 工具执行链（`ToolExecutionGuard.Execute` 前置），命中即时拦截并计数；Stratum 由 `Tenant.Plan` 经窄端口映射；行为信号两路采集（feedback 推导 + agent 埋点）统一收敛为 `ObservationSignals.Behavior`；判异在 `ObservationService` 识别 → verdict 分级 + 指标 → Prometheus 告警规则 → Alertmanager → 飞书（复用既有通道，不新建）。指标名、告警规则、参数模板严格对齐规格 §11/§4.1/§4.5/§13。

**Tech Stack:** Go 1.25、pgx v5、NATS JetStream、Prometheus + Alertmanager、飞书适配器（已存在）。

## Global Constraints

（每任务隐式包含本节的绑定约束，实施时逐字执行）

1. **评估器不阻断执行（§14）**：观测/信号/判异路径一律 best-effort——失败只 warn + 指标计数，绝不向执行链返回 error 造成阻断。唯一例外是规则护栏本身（§4.1 即时拦截，fail closed）。
2. **规则护栏 fail closed（§4.1/§14）**：denylist 命中即时拦截，禁止默认放行。未启用（默认 false）时静默放行，不参与拦截语义。
3. **tenant 边界**：所有 tenant-scoped 数据库访问显式携带 `tenantID`，统一走 `postgres.WithTenant` + `execTenantTx`（`internal/evaluation/infrastructure/persistence/` 既有模式）。禁止绕过。
4. **pgx JSONB**：向 JSONB 写自定义 struct 时先 `json.Marshal` 再传 `string(b)`（`observation_repository.go:45-52` 先例），禁止直接传 struct。
5. **指标名逐字对齐 §11**：
   - `eval_rule_hit_total{rule, resource, verdict}`（§11.1）
   - `eval_behavior_anomaly_total{resource, signal}`（§11.1）
   - `eval_observation_total{resource, stratum}`（P1a 已有，本计划把 stratum 填上）
   - `eval_sample_coverage{resource}`（§11.1，Gauge）
   - `eval_gate_action_total{layer, action}`（§11.2）
6. **参数只注册不播种**：`evaluation.ruleguard.*` 沿用 `registerObservationParams` 风格（`internal/parameters/domain/registry.go:466`），默认 false/空，PlatformValues 对快照缺失 key 回退 registry default。禁止在 `config/config.go` 改默认值。
7. **行为数字进 `pkg/constants/evaluation.go`**：judge 跌阈 `0.5`、feedback 负反馈阈值 `0.5` 收敛为命名常量，禁止内联。若文件不存在则新建。
8. **跨 context 接口定义在消费方 domain/port**：agent 侧不 import evaluation 类型；参数读取通过 wiring 包装的 `func(ctx) bool` / `func(ctx) []string` 注入。禁止 import 兄弟 context 的 application/infrastructure。
9. **错误逐层 wrap**：`fmt.Errorf("operation: %w", err)`。错误逐层翻译，禁止吞错。best-effort 路径的忽略必须显式注释理由。
10. **code quality 门禁**：圈复杂度 ≤10、认知复杂度 ≤15、函数 ≤120 行、嵌套 ≤4、行宽 ≤120。修改 port 后立即搜索并同步所有 test mock/stub。
11. **监控告警**：新 `monitoring/remote/rules/stratum-evaluation.yaml` 首行必须 `groups:`；每条 alert 带 `runbook_url: /docs/operations/alerts/stratum-evaluation.md#<anchor>` + runbook 内 `<a id="<anchor>"></a>\n\n## <AlertName>` 锚点；promtool test 覆盖每条规则；重跑 `scripts/quality/render-monitoring-rules.sh remote-test` 与 `local` 渲染并 commit generated（`--check` 防漂移，禁止手改 generated）。
12. **Commit 格式**：`[type](scope): description`（type 用 `feat|fix|refactor|test|docs`），结尾 `Co-Authored-By: Claude <noreply@anthropic.com>`。
13. 完整测试门禁：P1b 属功能改动，验收按 `.test/verification.yaml` 走完整测试（`make test-verify-before-pr` + stratum-e2e-tester 系统验收），实施完成后必须执行。

---

### Task 1: 评测指标接口与注册（eval_rule_hit / eval_behavior_anomaly / eval_gate_action / eval_sample_coverage）

**Files:**

- Modify: `pkg/observability/provider.go:123-133`（MetricsProvider 接口 + NoopMetrics）
- Modify: `pkg/observability/prometheus.go:330-354`（字段 + `registerEvalObservationMetrics` + 方法）
- Test: `pkg/observability/prometheus_test.go`（新增）

**Interfaces:**

- Consumes: `MetricsProvider` 现有方法（`IncEvalObservation`、`IncEvalJudgeFailure` 等在 `provider.go:127-133`）。
- Produces: `MetricsProvider` 新增 4 个方法——`IncEvalRuleHit(rule, resource, verdict string)`、`IncEvalBehaviorAnomaly(resource, signal string)`、`IncEvalGateAction(layer, action string)`、`RecordEvalSampleCoverage(resource string, ratio float64)`。后续 Task 3（拦截计数）、Task 5（feedback 合并）、Task 6（判异+coverage）消费。

- [ ] **Step 1: 先读现有实现**

读 `pkg/observability/provider.go` 的 `MetricsProvider` 接口（:123-133）与 `NoopMetrics`（:221-227）、`pkg/observability/prometheus.go` 的 `PrometheusMetrics` 结构体字段区、`registerEvalObservationMetrics`（:332-354）与方法实现（:897-924）。确认结构体字段命名风格（`evalObservationTotal` 等）。

- [ ] **Step 2: 扩展 MetricsProvider 接口与 NoopMetrics**

在 `pkg/observability/provider.go` 接口 `SetEvalQueueBacklog(queue string, count int64)` 之后追加 5 个方法：

```go
 // P1b：规则护栏命中计数（§11.1 eval_rule_hit_total）。
 IncEvalRuleHit(rule, resource, verdict string)
 // P1b：行为异常判异计数（§11.1 eval_behavior_anomaly_total）。
 IncEvalBehaviorAnomaly(resource, signal string)
 // P1b：分层门禁动作计数（§11.2 eval_gate_action_total）。
 IncEvalGateAction(layer, action string)
 // P1b：主动采样覆盖率（§11.1 eval_sample_coverage，Gauge [0,1]）。
 RecordEvalSampleCoverage(resource string, ratio float64)
```

`NoopMetrics` 同步追加 4 个空实现（对齐现有 `NoopMetrics` 单行风格）：

```go
func (NoopMetrics) IncEvalRuleHit(_, _, _ string)                                 {}
func (NoopMetrics) IncEvalBehaviorAnomaly(_, _ string)                            {}
func (NoopMetrics) IncEvalGateAction(_, _ string)                                 {}
func (NoopMetrics) RecordEvalSampleCoverage(_ string, _ float64)                  {}
```

- [ ] **Step 3: 注册 Prometheus 指标**

在 `pkg/observability/prometheus.go` `registerEvalObservationMetrics`（:354 `evalQueueBacklog` 注册之后、函数末尾）追加：

```go
 m.evalRuleHitTotal = factory.NewCounterVec(
  prometheus.CounterOpts{Name: "eval_rule_hit_total", Help: "规则护栏命中计数（§11.1）"},
  []string{"rule", "resource", "verdict"},
 )
 m.evalBehaviorAnomalyTotal = factory.NewCounterVec(
  prometheus.CounterOpts{Name: "eval_behavior_anomaly_total", Help: "行为异常判异计数（§11.1）"},
  []string{"resource", "signal"},
 )
 m.evalGateActionTotal = factory.NewCounterVec(
  prometheus.CounterOpts{Name: "eval_gate_action_total", Help: "分层门禁动作计数（§11.2）"},
  []string{"layer", "action"},
 )
 m.evalSampleCoverage = factory.NewGaugeVec(
  prometheus.GaugeOpts{Name: "eval_sample_coverage", Help: "主动采样覆盖率（§11.1）"},
  []string{"resource"},
 )
```

在 `PrometheusMetrics` 结构体字段区（`evalObservationTotal` 等旁边）追加 4 个字段：

```go
 evalRuleHitTotal          *prometheus.CounterVec
 evalBehaviorAnomalyTotal  *prometheus.CounterVec
 evalGateActionTotal       *prometheus.CounterVec
 evalSampleCoverage        *prometheus.GaugeVec
```

- [ ] **Step 4: 实现方法**

在 `pkg/observability/prometheus.go` `SetEvalQueueBacklog`（:923）之后追加 5 个方法（字段 nil 防御：断言注册已发生；全 nil 时按 Noop 处理不 panic）：

```go
func (m *PrometheusMetrics) IncEvalRuleHit(rule, resource, verdict string) {
 if m.evalRuleHitTotal != nil {
  m.evalRuleHitTotal.WithLabelValues(rule, resource, verdict).Inc()
 }
}

func (m *PrometheusMetrics) IncEvalBehaviorAnomaly(resource, signal string) {
 if m.evalBehaviorAnomalyTotal != nil {
  m.evalBehaviorAnomalyTotal.WithLabelValues(resource, signal).Inc()
 }
}

func (m *PrometheusMetrics) IncEvalGateAction(layer, action string) {
 if m.evalGateActionTotal != nil {
  m.evalGateActionTotal.WithLabelValues(layer, action).Inc()
 }
}

func (m *PrometheusMetrics) RecordEvalSampleCoverage(resource string, ratio float64) {
 if m.evalSampleCoverage != nil {
  m.evalSampleCoverage.WithLabelValues(resource).Set(ratio)
 }
}
```

> 注：采样到达/采样通过计数由 `ObservationService` 内部 mutex map 维护（Task 6），
> Prometheus 侧只暴露 `eval_sample_coverage` Gauge（ratio），不暴露到达/通过原始计数，
> 避免按 resource 膨胀指标基数且无下游消费。

- [ ] **Step 5: 写冒烟测试**

新建 `pkg/observability/prometheus_test.go`（若已有同名文件则追加），覆盖注册后不 panic + 值递增：

```go
func TestPrometheusMetricsEvalObservationP1b(t *testing.T) {
 m := NewPrometheusMetrics("test", prometheus.NewRegistry())
 m.IncEvalRuleHit("tool_denylist", "agent", "block")
 m.IncEvalBehaviorAnomaly("agent", "judge_below_threshold")
 m.IncEvalGateAction("rule_guard", "block")
 m.RecordEvalSampleCoverage("agent", 0.5)
}
```

若 `NewPrometheusMetrics` 签名与现有测试不同，以现有 `prometheus_test.go` 的构造方式为准（先读该文件确认）。

- [ ] **Step 6: 编译 + 测试 + 提交**

```bash
go build ./pkg/observability/...
go test ./pkg/observability/ -run TestPrometheusMetricsEvalObservationP1b -v
```

预期：PASS。随后提交：

```bash
git add pkg/observability/provider.go pkg/observability/prometheus.go pkg/observability/prometheus_test.go
git commit -m "feat(observability): 注册 P1b 评测指标 eval_rule_hit/behavior_anomaly/gate_action/sample_coverage

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Stratum tier 解析（Tenant.Plan → 观测 stratum）

**Files:**

- Create: `internal/evaluation/domain/port/tier.go`
- Modify: `internal/evaluation/application/observation_service.go:20-28,90-118,45-87`（deps + buildObservation 签名 + 填充）
- Create: `api/wiring/evaluation_tier_adapter.go`
- Modify: `api/wiring/evaluation.go`（装配 `ObservationServiceDeps.TenantTier`，先读现有装配处）
- Test: `internal/evaluation/application/observation_service_test.go`（追加用例）

**Interfaces:**

- Consumes: `iam` 的 `AdminTenantRepo.Get(ctx, tenantID) (*domain.Tenant, error)`，`domain.Tenant.Plan` 是 string（`internal/iam/domain/tenant.go:22-32`）。`ObservationServiceDeps` 现有字段（`Enabled/SampleRate/Evidence/Judge/Repo/Metrics/Logger`，`observation_service.go:20-28`）。
- Produces: `port.TenantTierReader`（新端口）；`ObservationServiceDeps.TenantTier port.TenantTierReader`；`buildObservation` 签名改为 `buildObservation(ctx context.Context, evt domain.ObservationReferenceEvent, trace port.ObservedTrace)`。后续 Task 6 复用该签名。

- [ ] **Step 1: 新建 tier 端口**

创建 `internal/evaluation/domain/port/tier.go`：

```go
package port

import "context"

// TenantTierReader 读取租户 tier（§3.6 stratum）。消费方显式携带 tenantID，
// 与 ctx 解耦（评测消费链路 ctx 无 tenant）。实现由 wiring 适配 iam
// AdminTenantRepo：Tenant.Plan 直接透传为 tier 字符串。
type TenantTierReader interface {
 GetTenantTier(ctx context.Context, tenantID string) (string, error)
}
```

- [ ] **Step 2: 扩展 deps 与 buildObservation**

`internal/evaluation/application/observation_service.go`：

在 `ObservationServiceDeps`（:20-28）追加字段：

```go
 TenantTier port.TenantTierReader // P1b：租户 tier → stratum（nil 时 stratum 留空）
```

`buildObservation`（:90）签名改为 `func (s *ObservationService) buildObservation(ctx context.Context, evt domain.ObservationReferenceEvent, trace port.ObservedTrace) domain.EvalObservation`，并把 `Stratum: ""`（:114）改为：

```go
  Stratum:   s.resolveStratum(ctx, evt.TenantID),
```

新增私有方法（放在 `buildObservation` 之后）：

```go
// resolveStratum 解析租户 tier 为 stratum（§4.3）。tier 解析失败不阻断落库：
// stratum 留空 + warn（§14 参数版本锚点缺失标记 unknown 的等价语义）。
func (s *ObservationService) resolveStratum(ctx context.Context, tenantID string) string {
 if s.deps.TenantTier == nil {
  return ""
 }
 tier, err := s.deps.TenantTier.GetTenantTier(ctx, tenantID)
 if err != nil {
  s.deps.Logger.Warn("observation tier resolve failed", zap.Error(err),
   zap.String("tenant_id", tenantID))
  return ""
 }
 return tier
}
```

同步更新 `Process` 里调用处（:62）：`obs := s.buildObservation(evt, trace)` → `obs := s.buildObservation(ctx, evt, trace)`。

- [ ] **Step 3: wiring 适配器**

创建 `api/wiring/evaluation_tier_adapter.go`：

```go
package wiring

import (
 "context"

 iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
 iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// tenantTierAdapter 把 iam AdminTenantRepo 适配为 evaluation 的 TenantTierReader：
// 租户 plan（free/pro）直接透传为 tier 字符串。
type tenantTierAdapter struct {
 repo iamport.AdminTenantRepo
}

func (a tenantTierAdapter) GetTenantTier(ctx context.Context, tenantID string) (string, error) {
 if a.repo == nil {
  return "", nil
 }
 tenant, err := a.repo.Get(ctx, tenantID)
 if err != nil {
  return "", err
 }
 return string(tenant.Plan), nil
}

// 编译期断言：tenantTierAdapter 满足 evaluation port。
var _ evaluationPort.TenantTierReader = tenantTierAdapter{}
```

> 需要 `evaluationPort` 别名指向 `internal/evaluation/domain/port`——先读 `api/wiring/` 现有 evaluation 装配文件（如 `evaluation.go`）确认已有的 import 别名与 `AdminTenantRepo` 获取方式（容器里 `AdminTenantRepo` 的字段/方法名），按既有风格写。

- [ ] **Step 4: 装配 TenantTier**

在 `api/wiring/` 构造 `ObservationServiceDeps` 处（先 grep `ObservationServiceDeps{` 定位），给 `TenantTier` 赋 `tenantTierAdapter{repo: <容器里的 AdminTenantRepo>}`。先读装配处确认变量名，再赋值；若该处已有 `ObservationService` 构造，直接补字段。

- [ ] **Step 5: 测试 tier 解析**

`internal/evaluation/application/observation_service_test.go` 追加用例（先读该文件，复用现有 deps mock 风格——`TenantTier` 用 stub 函数或最小 struct）：

```go
t.Run("stratum filled from tenant tier", func(t *testing.T) {
 // 构造 deps：Enabled=true、SampleRate=1.0、Judge=启用 stub、Evidence=可解析、
 // TenantTier 返回 "pro"，其余与现有用例一致的 mock。
 // Process(ctx, evt{TenantID:"t1"}) 后断言 Repo.Save 收到的 obs.Stratum == "pro"。
})
t.Run("stratum unknown when tier resolve fails", func(t *testing.T) {
 // TenantTier 返回 error；Process 后 obs.Stratum == ""，且不返回 error（不阻断落库）。
})
```

用例结构参照该文件现有 `TestObservationServiceProcess` 表驱动写法（mock Repo.Save 捕获入参）。

- [ ] **Step 6: 编译 + 全量短测试 + 提交**

```bash
go build ./...
go vet ./internal/evaluation/... ./api/wiring/...
go test -short ./internal/evaluation/... ./api/wiring/...
```

预期：PASS。提交：

```bash
git add internal/evaluation/domain/port/tier.go internal/evaluation/application/observation_service.go api/wiring/evaluation_tier_adapter.go api/wiring/evaluation.go internal/evaluation/application/observation_service_test.go
git commit -m "feat(evaluation): Stratum tier 经 Tenant.Plan 窄端口解析填充观测

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 规则护栏内联（快路径即时拦截 + T1 计数）

**Files:**

- Modify: `internal/agent/domain/agent.go`（追加 `RuleBlock` 结构）
- Create: `internal/agent/application/rule_guard.go`
- Modify: `internal/agent/application/tool_execution_guard.go:24-30,43-56`（deps + Execute 前置）
- Modify: `internal/parameters/domain/registry.go:466`（`registerRuleGuardParams` + 在现有注册入口调用，先读 `registerObservationParams` 被谁调）
- Modify: `internal/agent/application/agent_options.go:685-688`（`buildToolApprovalGuard` 装配 `RuleGuard`）+ `AgentService` struct deps（`internal/agent/application/agent_service.go` 或 `agent_deps.go` 的 `AgentServiceDeps`）
- Test: `internal/agent/application/rule_guard_test.go`（新建）+ `internal/agent/application/tool_execution_guard_test.go`（追加）

**Interfaces:**

- Consumes: Task 1 的 `MetricsProvider.IncEvalRuleHit/IncEvalGateAction`；`port.ToolExecutionRequest`（`tool_execution_guard.go:20`，含 `Tool.Name`）；平台参数读取经 wiring 包装的 `func(ctx) bool` / `func(ctx) []string`。
- Produces: `domain.RuleBlock{Rule, Tool, Message string}`；`RuleBlockedError`（实现 error，`Error()` 含 `rule_blocked:` 前缀）；`RuleGuard` 及 `RuleGuardDeps{Enabled func(ctx) bool, Denylist func(ctx) []string, Metrics observability.MetricsProvider, Logger *zap.Logger}`；`RuleGuard.Check(ctx context.Context, toolID string) (*RuleBlockedError, bool)`；`ToolExecutionGuardDeps.RuleGuard *RuleGuard`；`AgentServiceDeps.RuleGuard *RuleGuard`；context key `ruleBlockCollectorKey{}`（注入 `*[]domain.RuleBlock`）。Task 4 消费 `RuleBlock` 与累积器。

- [ ] **Step 1: 领域类型 RuleBlock**

在 `internal/agent/domain/agent.go`（`AgentResult` 结构前，约 :386 前）追加：

```go
// RuleBlock 记录一次规则护栏即时拦截（§4.1）。仅信号级：进观测事件供评测侧
// 归集，不改变执行错误语义（拦截走 RuleBlockedError 返回）。
type RuleBlock struct {
 Rule    string `json:"rule"`
 Tool    string `json:"tool"`
 Message string `json:"message"`
}
```

- [ ] **Step 2: 新建 RuleGuard**

创建 `internal/agent/application/rule_guard.go`：

```go
package application

import (
 "context"
 "fmt"
 "strings"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "go.uber.org/zap"
)

// RuleBlockedError 是规则护栏即时拦截信号：命中即失败返回，禁止默认放行（§4.1 fail closed）。
type RuleBlockedError struct {
 Rule    string
 Tool    string
 Message string
}

func (e *RuleBlockedError) Error() string {
 return fmt.Sprintf("rule_blocked:%s:tool=%s:%s", e.Rule, e.Tool, e.Message)
}

// ruleBlockCollectorKey 是 context 累积器 key：AgentService 在执行上下文中注入
// *[]domain.RuleBlock，RuleGuard 命中时追加，emitObservation 读取进观测事件。
type ruleBlockCollectorKey struct{}

// RuleGuardDeps 是规则护栏依赖。Enabled/Denylist 由 wiring 包装平台参数读取
// （evaluation.ruleguard.*，仅注册不播种；enabled 默认 false 时静默放行）。
type RuleGuardDeps struct {
 Enabled  func(ctx context.Context) bool
 Denylist func(ctx context.Context) []string
 Metrics  observability.MetricsProvider
 Logger   *zap.Logger
}

// RuleGuard 是内联规则护栏（§4.1 快路径）：denylist 命中即时拦截，零 LLM、零额外延迟。
type RuleGuard struct {
 deps RuleGuardDeps
}

func NewRuleGuard(deps RuleGuardDeps) *RuleGuard {
 if deps.Logger == nil {
  deps.Logger = zap.NewNop()
 }
 if deps.Metrics == nil {
  deps.Metrics = observability.NoopMetrics{}
 }
 return &RuleGuard{deps: deps}
}

// Check 对单个工具名执行规则护栏：denylist 命中返回 RuleBlockedError（fail closed）
// 并计数 T1（eval_rule_hit_total + eval_gate_action_total），同时把拦截记录追加到
// ctx 累积器供观测事件携带；未命中或规则未启用返回 nil（放行）。
func (g *RuleGuard) Check(ctx context.Context, toolID string) (*RuleBlockedError, bool) {
 if g == nil || g.deps.Enabled == nil || !g.deps.Enabled(ctx) {
  return nil, false
 }
 for _, denied := range g.deps.Denylist(ctx) {
  denied = strings.TrimSpace(denied)
  if denied == "" || !strings.EqualFold(denied, toolID) {
   continue
  }
  block := &RuleBlockedError{
   Rule:    "tool_denylist",
   Tool:    toolID,
   Message: fmt.Sprintf("tool %q blocked by platform rule", toolID),
  }
  g.deps.Metrics.IncEvalRuleHit("tool_denylist", "agent", "block")
  g.deps.Metrics.IncEvalGateAction("rule_guard", "block")
  if collector, ok := ctx.Value(ruleBlockCollectorKey{}).(*[]domain.RuleBlock); ok {
   *collector = append(*collector, domain.RuleBlock{Rule: block.Rule, Tool: toolID, Message: block.Message})
  }
  return block, true
 }
 return nil, false
}
```

- [ ] **Step 3: 集成到 ToolExecutionGuard**

`internal/agent/application/tool_execution_guard.go`：

`ToolExecutionGuardDeps`（:24-30）追加字段：

```go
 RuleGuard *RuleGuard // P1b：内联规则护栏，nil 时跳过（默认放行）
```

`Execute`（:43-46）在 `Authorizer == nil` 检查之后、`Authorize` 之前插入：

```go
 if g.deps.RuleGuard != nil {
  if block, blocked := g.deps.RuleGuard.Check(ctx, req.Tool.Name); blocked {
   return nil, block
  }
 }
```

- [ ] **Step 4: 平台参数注册**

`internal/parameters/domain/registry.go`：在 `registerObservationParams`（:466）旁新增 `registerRuleGuardParams`：

```go
// registerRuleGuardParams 是 Phase 1 规则护栏（§4.1）的平台级参数。enabled 默认
// false：平台未显式开启时护栏静默放行（fail open 于规则层，开启后才是 fail closed）。
// denylist 逗号分隔工具名；仅注册不播种。
func (r *ParametersRegistry) registerRuleGuardParams() {
 _ = r.Register(ParameterDefinition{
  Key: "evaluation.ruleguard.enabled", Scope: ScopePlatform, Category: "evaluation",
  DisplayName: "启用规则护栏", Description: "内联工具 denylist 即时拦截(默认关)",
  ValueType: TypeBool, Default: false,
  VisualHint:  VisualHint{Control: ControlToggle},
  Optimizable: true,
 })
 _ = r.Register(ParameterDefinition{
  Key: "evaluation.ruleguard.denylist", Scope: ScopePlatform, Category: "evaluation",
  DisplayName: "规则护栏 Denylist", Description: "逗号分隔的禁止工具名列表(命中即拦截)",
  ValueType: TypeString, Default: "",
  VisualHint:  VisualHint{Control: ControlTextarea},
  Optimizable: true,
 })
}
```

先读 `registry.go` 里 `registerObservationParams` 的调用点（grep `registerObservationParams()`），在相邻位置调用 `r.registerRuleGuardParams()`。

- [ ] **Step 5: 装配 RuleGuard 到 AgentService**

`AgentServiceDeps`（`internal/agent/application/` 内定义处）追加字段：

```go
 RuleGuard *RuleGuard // P1b：内联规则护栏，wiring 注入；nil 时 guard 不装配规则检查
```

`agent_options.go` `buildToolApprovalGuard`（:685-688）的 `ToolExecutionGuardDeps` 补 `RuleGuard: s.deps.RuleGuard`：

```go
 return NewToolExecutionGuard(ToolExecutionGuardDeps{
  Authorizer: s.deps.ToolAuthorizer, Executor: s.deps.MCPToolExecutor, RequestApproval: requestApproval,
  RuleGuard: s.deps.RuleGuard,
 }), approvalService, approvalCtx
```

（`agent_approval.go:777` 的 `NewToolExecutionGuard` 装配处不传 `RuleGuard` 即 nil，合法——审批续跑路径不重复内联规则检查，由主路径已拦。）

- [ ] **Step 6: wiring 装配 RuleGuard**

在 `api/wiring/agent.go` 构造 `AgentService` 处（grep `AgentService{` 或 `NewAgentService(`），给 `RuleGuard` 赋值。参数读取沿用容器内 `parameters` 读取器既有模式（先读 wiring 里 `evaluation.observe.enabled` 怎么包成 `func(ctx) bool`，镜像即可）：

```go
 RuleGuard: agentapplication.NewRuleGuard(agentapplication.RuleGuardDeps{
  Enabled:  func(ctx context.Context) bool { return <parameters读取器>.Bool(ctx, "evaluation.ruleguard.enabled", false) },
  Denylist: func(ctx context.Context) []string {
   raw := <parameters读取器>.String(ctx, "evaluation.ruleguard.denylist", "")
   return strings.Split(raw, ",")
  },
  Metrics: metrics,
  Logger:  logger,
 }),
```

- [ ] **Step 7: 测试**

新建 `internal/agent/application/rule_guard_test.go`：

```go
func TestRuleGuardCheck(t *testing.T) {
 t.Run("blocked when denylist matches", func(t *testing.T) {
  g := NewRuleGuard(RuleGuardDeps{
   Enabled:  func(context.Context) bool { return true },
   Denylist: func(context.Context) []string { return []string{"danger_tool"} },
   Metrics:  observability.NoopMetrics{},
  })
  blocks := &[]domain.RuleBlock{}
  ctx := context.WithValue(context.Background(), ruleBlockCollectorKey{}, blocks)
  block, blocked := g.Check(ctx, "danger_tool")
  if !blocked || block == nil || block.Rule != "tool_denylist" {
   t.Fatalf("expected blocked, got blocked=%v block=%v", blocked, block)
  }
  if len(*blocks) != 1 {
   t.Fatalf("expected 1 accumulated block, got %d", len(*blocks))
  }
 })
 t.Run("allow when not listed", func(t *testing.T) {
  g := NewRuleGuard(RuleGuardDeps{
   Enabled:  func(context.Context) bool { return true },
   Denylist: func(context.Context) []string { return []string{"danger_tool"} },
   Metrics:  observability.NoopMetrics{},
  })
  if block, blocked := g.Check(context.Background(), "safe_tool"); blocked || block != nil {
   t.Fatalf("expected allowed, got blocked=%v block=%v", blocked, block)
  }
 })
 t.Run("disabled guard allows", func(t *testing.T) {
  g := NewRuleGuard(RuleGuardDeps{Enabled: func(context.Context) bool { return false }})
  if block, blocked := g.Check(context.Background(), "danger_tool"); blocked || block != nil {
   t.Fatalf("expected allowed when disabled, got blocked=%v", blocked)
  }
 })
}
```

`internal/agent/application/tool_execution_guard_test.go` 追加用例（先读该文件现有构造方式）：`Execute` 在 `RuleGuard` 命中时返回 `RuleBlockedError`，未命中时照常执行 executor（mock executor 断言未被调用/被调用）。

- [ ] **Step 8: 编译 + 测试 + 提交**

```bash
go build ./...
go vet ./internal/agent/... ./internal/parameters/...
go test -short ./internal/agent/application/ -run 'TestRuleGuardCheck|TestToolExecutionGuard' ./internal/parameters/...
```

预期：PASS。提交：

```bash
git add internal/agent/domain/agent.go internal/agent/application/rule_guard.go internal/agent/application/tool_execution_guard.go internal/agent/application/agent_options.go internal/parameters/domain/registry.go api/wiring/agent.go internal/agent/application/rule_guard_test.go internal/agent/application/tool_execution_guard_test.go
git commit -m "feat(agent): 规则护栏内联快路径即时拦截并计数 T1

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 观测事件契约扩展 + agent 侧信号填充（rule + behavior）

**Files:**

- Modify: `internal/agent/domain/port/observation.go`（`ObservationEvent` 加 `RuleSignals`/`Behavior` 字段）
- Modify: `internal/agent/application/agent_execution.go:273-303,311-361`（execCtx 注入累积器 + emitObservation 调用 ctx→execCtx）
- Modify: `internal/agent/application/agent_service.go:118-138`（emitObservation 填充信号）
- Modify: `internal/evaluation/domain/observation_event.go`（`ObservationReferenceEvent` 同步加字段）
- Modify: `internal/evaluation/domain/observation_event_test.go`（golden 契约更新，先读）
- Test: `internal/evaluation/domain/observation_event_test.go`

**Interfaces:**

- Consumes: Task 3 的 `ruleBlockCollectorKey{}`、`domain.RuleBlock`；`AgentResult` 的 `NoAnswerInfo.Retried` 与 `Degraded`（`internal/agent/domain/agent.go:418,443`）。
- Produces: `port.ObservationEvent` 新增字段 `RuleSignals []RuleSignalPayload`、`Behavior BehaviorSignalPayload`（均 `omitempty`）；`evaluation/domain/ObservationReferenceEvent` 对应字段。Task 5 用 behavior 字段映射的合并，Task 6 用 rule/behavior 信号判异。

- [ ] **Step 1: 读现有事件契约与 golden**

读 `internal/agent/domain/port/observation.go`（`ObservationEvent` 结构）、`internal/evaluation/domain/observation_event.go`（已同步结构）、`internal/evaluation/domain/observation_event_test.go`（golden 断言方式——是固定 JSON 字符串还是 golden 文件）。

- [ ] **Step 2: agent 侧 ObservationEvent 加字段**

`internal/agent/domain/port/observation.go` 的 `ObservationEvent` 追加：

```go
 // P1b：规则护栏命中信号（§4.1）与执行行为信号（§4.2）。best-effort，可为空。
 RuleSignals []RuleSignalPayload  `json:"rule_signals,omitempty"`
 Behavior    BehaviorSignalPayload `json:"behavior,omitempty"`
```

同文件追加类型：

```go
// RuleSignalPayload 单条规则命中信号（进评测观测 signals.rule）。
type RuleSignalPayload struct {
 Rule    string `json:"rule"`
 Message string `json:"message"`
}

// BehaviorSignalPayload 执行行为信号（进评测观测 signals.behavior）。
type BehaviorSignalPayload struct {
 Retry       bool `json:"retry"`
 Escalation  bool `json:"escalation"`
 Abandonment bool `json:"abandonment"`
}
```

- [ ] **Step 3: evaluation 侧 ObservationReferenceEvent 同步**

`internal/evaluation/domain/observation_event.go` 的 `ObservationReferenceEvent` 追加同 JSON 字段：

```go
 RuleSignals []RuleSignalPayload  `json:"rule_signals,omitempty"`
 Behavior    BehaviorSignalPayload `json:"behavior,omitempty"`
```

同文件追加镜像类型（字段与 agent 侧 JSON 一致，golden 契约守护对齐）：

```go
// RuleSignalPayload 与 internal/agent/domain/port 的 RuleSignalPayload JSON 对齐。
type RuleSignalPayload struct {
 Rule    string `json:"rule"`
 Message string `json:"message"`
}

// BehaviorSignalPayload 与 internal/agent/domain/port 的 BehaviorSignalPayload JSON 对齐。
type BehaviorSignalPayload struct {
 Retry       bool `json:"retry"`
 Escalation  bool `json:"escalation"`
 Abandonment bool `json:"abandonment"`
}
```

> 若 golden 契约测试（`observation_event_test.go`）是固定 JSON 断言，同步补上新字段的期望值；若是 marshal/unmarshal 往返断言，追加新字段往返用例。先读再改。

- [ ] **Step 4: emitObservation 填充信号**

`internal/agent/application/agent_service.go` `emitObservation`（:118-138）把事件构造改为：

```go
 evt := port.ObservationEvent{
  TenantID:     meta.TenantID,
  TraceID:      meta.TraceID,
  ExecutionID:  executionID,
  AgentID:      agentID,
  ResourceKind: "agent",
  ResourceID:   agentID,
  CompletedAt:  time.Now().UTC().Format(time.RFC3339Nano),
  RuleSignals:  ruleSignalsFromBlocks(ctx),
  Behavior:     behaviorFromResult(result),
 }
```

同文件追加两个私有 helper：

```go
// ruleSignalsFromBlocks 从 ctx 累积器读取规则护栏拦截记录，转观测事件信号。
// 无累积器或为空时返回 nil（omitempty 不出现）。
func ruleSignalsFromBlocks(ctx context.Context) []port.RuleSignalPayload {
 collector, ok := ctx.Value(ruleBlockCollectorKey{}).(*[]domain.RuleBlock)
 if !ok || len(*collector) == 0 {
  return nil
 }
 out := make([]port.RuleSignalPayload, 0, len(*collector))
 for _, b := range *collector {
  out = append(out, port.RuleSignalPayload{Rule: b.Rule, Message: b.Message})
 }
 return out
}

// behaviorFromResult 从执行结果推导行为信号（§4.2）：检索触底重试→Retry；
// 工具连续校验失败降级→Abandonment。Escalation 由 feedback 侧（安全违规）补。
func behaviorFromResult(result *AgentResult) port.BehaviorSignalPayload {
 b := port.BehaviorSignalPayload{}
 if result.NoAnswerInfo != nil && result.NoAnswerInfo.Retried {
  b.Retry = true
 }
 if result.Degraded {
  b.Abandonment = true
 }
 return b
}
```

- [ ] **Step 5: execCtx 注入累积器 + 改用 execCtx**

`internal/agent/application/agent_execution.go`：

`Execute`（:280-284）在 `execCtx` 创建后注入累积器：

```go
 execCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
 defer cancel()
 blocks := &[]domain.RuleBlock{}
 execCtx = context.WithValue(execCtx, ruleBlockCollectorKey{}, blocks)
```

`Execute` 里 `s.emitObservation(ctx, ...)`（:295）改为 `s.emitObservation(execCtx, ...)`。

`ExecuteStream` 的 `run` 闭包（:339-361）：在 `execCtx, cancel = context.WithCancel(...)` 后同样注入 `blocks`，并把闭包内 `s.emitObservation(execCtx, ...)`（先读 :348 后实际调用处，可能用 `execCtx` 或 `ctx`）统一为 `execCtx`。

- [ ] **Step 6: 测试**

`internal/evaluation/domain/observation_event_test.go`：更新/新增用例断言新字段往返一致（RuleSignals 非空时 marshal 出 `"rule_signals"`，空时 omitempty 不出现；Behavior 同理）。

`internal/agent/application/agent_service_test.go`（若存在）或新建：`emitObservation` 在 `ctx` 带累积器 + result 带 `Degraded`/`NoAnswerInfo.Retried` 时，事件含对应信号；`ObservationEmitter` mock 捕获事件断言字段。

- [ ] **Step 7: 编译 + 测试 + 提交**

```bash
go build ./...
go vet ./internal/agent/... ./internal/evaluation/domain/...
go test -short ./internal/evaluation/domain/... ./internal/agent/application/...
```

预期：PASS（golden 已同步）。提交：

```bash
git add internal/agent/domain/port/observation.go internal/agent/application/agent_execution.go internal/agent/application/agent_service.go internal/evaluation/domain/observation_event.go internal/evaluation/domain/observation_event_test.go
git commit -m "feat(evaluation): 观测事件契约扩展 rule/behavior 信号并填充

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: feedback 行为信号推导 + 合并到观测

**Files:**

- Modify: `internal/evaluation/domain/port/observation_repo.go`（`ObservationRepository` 加 2 方法）
- Modify: `internal/evaluation/infrastructure/persistence/observation_repository.go`（实现 + 复用 `observationColumns`/`unmarshalObservationJSON`）
- Create: `internal/evaluation/domain/port/behavior.go`
- Modify: `internal/evaluation/application/observation_service.go`（`ApplyBehaviorSignals`）
- Modify: `internal/evaluation/application/feedback_service.go:22-32,34-133`（推导 + 调用）
- Modify: `internal/evaluation/application/feedback_service_test.go`、`observation_service_test.go`、`observation_repository_test.go`
- Modify: `api/wiring/evaluation.go`（装配 `FeedbackService` 注入 writer）

**Interfaces:**

- Consumes: `domain.BehaviorSignals`（`internal/evaluation/domain/observation.go:60-65`）；`domain.FeedbackRequest{Score, SecurityViolation}`（`internal/evaluation/domain/evaluation.go:253-265`）；`observation_repository.go` 的 `observationColumns`（:15）与 `unmarshalObservationJSON`（:149）。
- Produces: `ObservationRepository.FindLatestByTrace(ctx, tenantID, traceID) (*domain.EvalObservation, error)`、`UpdateBehaviorSignals(ctx, tenantID, observationID string, signals domain.BehaviorSignals) error`；`port.BehaviorSignalWriter{ ApplyBehaviorSignals(ctx, tenantID, traceID string, signals domain.BehaviorSignals) error }`；`ObservationService.ApplyBehaviorSignals`；`FeedbackService` 新依赖 `writer port.BehaviorSignalWriter`。Task 6 判异消费合并后的 behavior 信号。

- [ ] **Step 1: 常量**

先读 `pkg/constants/evaluation.go`（不存在则新建），追加：

```go
// Evaluation 运行态观测行为阈值（P1b §4.2）。
const (
 // JudgeBelowThreshold 是 judge 单维度跌阈判异的阈值（§4.2 判异触发）。
 JudgeBelowThreshold = 0.5
 // FeedbackNegativeThreshold 是 feedback 负反馈判异阈值：score 低于该值视为放弃倾向。
 FeedbackNegativeThreshold = 0.5
)
```

若 `pkg/constants/evaluation.go` 已有 judge 相关常量命名，按既有命名风格对齐（先 grep `Judge` in `pkg/constants/`）。

- [ ] **Step 2: ObservationRepository 加方法**

`internal/evaluation/domain/port/observation_repo.go` 接口追加：

```go
 // FindLatestByTrace 按 trace_id 取最近一条观测（created_at 倒序）；不存在返回 (nil, nil)。
 FindLatestByTrace(ctx context.Context, tenantID, traceID string) (*domain.EvalObservation, error)
 // UpdateBehaviorSignals 合并更新某条观测的 signals.behavior（幂等，不存在不报错）。
 UpdateBehaviorSignals(ctx context.Context, tenantID, observationID string, signals domain.BehaviorSignals) error
```

`internal/evaluation/infrastructure/persistence/observation_repository.go` 实现（沿用 `postgres.WithTenant` + `execTenantTx` 风格）：

```go
func (r *PgObservationRepository) FindLatestByTrace(ctx context.Context, tenantID, traceID string) (*domain.EvalObservation, error) {
 var (
  obs                              domain.EvalObservation
  kind, verdict                    string
  paramJSON, signalsJSON, costJSON string
 )
 ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
 err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx,
   `SELECT `+observationColumns+` FROM eval_observations WHERE trace_id = $1 ORDER BY created_at DESC LIMIT 1`,
   traceID,
  ).Scan(&obs.ID, &obs.TraceID, &kind, &obs.Resource.ResourceID,
   &paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &verdict, &obs.CreatedAt)
 })
 if err != nil {
  if err == pgx.ErrNoRows {
   return nil, nil
  }
  return nil, fmt.Errorf("find latest observation by trace: %w", err)
 }
 obs.Resource.Kind = domain.ResourceKind(kind)
 obs.Verdict = domain.ObservationVerdict(verdict)
 if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
  return nil, fmt.Errorf("find latest observation by trace: unmarshal: %w", err)
 }
 return &obs, nil
}

func (r *PgObservationRepository) UpdateBehaviorSignals(ctx context.Context, tenantID, observationID string, signals domain.BehaviorSignals) error {
 signalsJSON, err := json.Marshal(signals)
 if err != nil {
  return fmt.Errorf("marshal behavior signals: %w", err)
 }
 ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
 err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  _, execErr := tx.Exec(ctx,
   `UPDATE eval_observations SET signals = jsonb_set(signals, '{behavior}', $2) WHERE id = $1`,
   observationID, string(signalsJSON),
  )
  if execErr != nil {
   return fmt.Errorf("update behavior signals: %w", execErr)
  }
  return nil
 })
 if err != nil {
  return fmt.Errorf("update behavior signals: %w", err)
 }
 return nil
}
```

> 注：`jsonb_set` 全量覆盖 behavior 对象。合并语义在 application 层完成（先读旧 signals → 合并 → 写回），repo 只负责整对象覆盖。`ObservationService.ApplyBehaviorSignals` 里做合并（Step 4）。

- [ ] **Step 3: BehaviorSignalWriter 端口**

创建 `internal/evaluation/domain/port/behavior.go`：

```go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// BehaviorSignalWriter 把用户行为信号（§4.2）合并到对应 trace 的观测。
// best-effort：找不到观测或更新失败返回错误由调用方决定忽略/告警，不阻断反馈链路。
type BehaviorSignalWriter interface {
 ApplyBehaviorSignals(ctx context.Context, tenantID, traceID string, signals domain.BehaviorSignals) error
}
```

- [ ] **Step 4: ObservationService.ApplyBehaviorSignals**

`internal/evaluation/application/observation_service.go` 追加方法（并加 `_ port.BehaviorSignalWriter = (*ObservationService)(nil)` 编译期断言于 `NewObservationService` 后）：

```go
// ApplyBehaviorSignals 把行为信号合并到该 trace 最近一条观测（§4.2）。找不到观测
// 时静默返回 nil（采样未覆盖该 trace，反馈不补造观测）；更新失败返回错误（调用方
// 忽略，best-effort）。
func (s *ObservationService) ApplyBehaviorSignals(ctx context.Context, tenantID, traceID string, signals domain.BehaviorSignals) error {
 if s.deps.Repo == nil {
  return nil
 }
 obs, err := s.deps.Repo.FindLatestByTrace(ctx, tenantID, traceID)
 if err != nil {
  return err
 }
 if obs == nil {
  return nil
 }
 merged := obs.Signals.Behavior
 merged.Retry = merged.Retry || signals.Retry
 merged.Escalation = merged.Escalation || signals.Escalation
 merged.Abandonment = merged.Abandonment || signals.Abandonment
 if merged == obs.Signals.Behavior {
  return nil
 }
 return s.deps.Repo.UpdateBehaviorSignals(ctx, tenantID, obs.ID, merged)
}
```

> 注：`merged == obs.Signals.Behavior` 比较两个 struct（全 bool 可比较）——为幂等短路。若行为信号有变化则合并写回；无变化返回 nil。

- [ ] **Step 5: FeedbackService 推导 + 调用**

`internal/evaluation/application/feedback_service.go`：

`FeedbackService` struct（:22-26）加字段：

```go
 writer port.BehaviorSignalWriter
```

`NewFeedbackService`（:28-32）签名加 `writer port.BehaviorSignalWriter` 参数并赋值。

`Record`（:34）在 `s.repo.Record` 成功之后（:62 后、`result := FeedbackResult{...}` 前）插入：

```go
 s.emitBehaviorSignals(ctx, tenantID, input)
```

新增私有方法（放在 `Record` 之后）：

```go
// emitBehaviorSignals 从 feedback 推导行为信号并合并到观测（§4.2 路 A）：
// score 低于阈值视为放弃倾向，security_violation 视为升级。best-effort：
// writer 为 nil 或合并失败均不阻断反馈链路，失败只记不报。
func (s *FeedbackService) emitBehaviorSignals(ctx context.Context, tenantID string, input RecordFeedbackInput) {
 if s.writer == nil {
  return
 }
 signals := domain.BehaviorSignals{}
 if input.Score < constants.FeedbackNegativeThreshold {
  signals.Abandonment = true
 }
 if input.SecurityViolation {
  signals.Escalation = true
 }
 if !signals.Abandonment && !signals.Escalation {
  return
 }
 _ = s.writer.ApplyBehaviorSignals(ctx, tenantID, input.TraceID, signals)
}
```

- [ ] **Step 6: wiring 装配**

在 `api/wiring/` 构造 `NewFeedbackService` 处，追加 `writer` 参数（传容器的 `ObservationService`——它实现了 `port.BehaviorSignalWriter`，先读装配处确认容器字段名）。同步检查 `NewFeedbackService` 的全部调用点（含测试），改签名。

- [ ] **Step 7: 测试**

`internal/evaluation/application/observation_service_test.go` 追加：`ApplyBehaviorSignals` 找不到观测返回 nil；找到且合并成功（mock `FindLatestByTrace` 返回观测、断言 `UpdateBehaviorSignals` 收到合并值）；无变化短路不写。

`internal/evaluation/application/feedback_service_test.go`：`Record` 在 score<0.5 时 writer 收到 `Abandonment=true`；SecurityViolation 时 `Escalation=true`；writer 为 nil 不 panic。mock `BehaviorSignalWriter` 捕获入参。

`internal/evaluation/infrastructure/persistence/observation_repository_test.go` 追加：`FindLatestByTrace` 返回最近一条；`UpdateBehaviorSignals` 后 `Get` 的 signals.behavior 合并值一致。

- [ ] **Step 8: 编译 + 测试 + 提交**

```bash
go build ./...
go vet ./internal/evaluation/... ./api/wiring/...
go test -short ./internal/evaluation/...
```

预期：PASS。提交：

```bash
git add pkg/constants/evaluation.go internal/evaluation/domain/port/observation_repo.go internal/evaluation/infrastructure/persistence/observation_repository.go internal/evaluation/domain/port/behavior.go internal/evaluation/application/observation_service.go internal/evaluation/application/feedback_service.go api/wiring/evaluation.go internal/evaluation/application/feedback_service_test.go internal/evaluation/application/observation_service_test.go internal/evaluation/infrastructure/persistence/observation_repository_test.go
git commit -m "feat(evaluation): feedback 行为信号推导并合并到观测

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 判异触发 + eval_sample_coverage 恢复

**Files:**

- Modify: `internal/evaluation/application/observation_service.go`（verdict 分级 + 判异指标 + sample 累计）
- Modify: `pkg/constants/evaluation.go`（复用 Task 5 常量，无新常量）
- Test: `internal/evaluation/application/observation_service_test.go`（追加）

**Interfaces:**

- Consumes: Task 4 的事件 `RuleSignals/Behavior`、Task 5 的 `JudgeBelowThreshold`；`MetricsProvider.IncEvalBehaviorAnomaly/IncEvalGateAction/RecordEvalSampleCoverage`（Task 1）。
- Produces: `ObservationService` 判异判定（verdict 分级：rule→block、behavior/judge 异常→flag）；`eval_sample_coverage` 每事件更新。无新导出签名。

- [ ] **Step 1: buildObservation 填充事件信号**

`internal/evaluation/application/observation_service.go` `buildObservation` 在返回 `domain.EvalObservation` 前，把事件携带的信号写入：

```go
 obs.Signals.Rule = ruleSignalsFromEvent(evt)
 obs.Signals.Behavior = behaviorFromEvent(evt)
```

同文件追加两个转换 helper：

```go
// ruleSignalsFromEvent 事件携带的规则命中信号转观测信号。
func ruleSignalsFromEvent(evt domain.ObservationReferenceEvent) []domain.RuleSignal {
 out := make([]domain.RuleSignal, 0, len(evt.RuleSignals))
 for _, r := range evt.RuleSignals {
  out = append(out, domain.RuleSignal{Rule: r.Rule, Message: r.Message})
 }
 return out
}

// behaviorFromEvent 事件携带的行为信号转观测信号。
func behaviorFromEvent(evt domain.ObservationReferenceEvent) domain.BehaviorSignals {
 return domain.BehaviorSignals{
  Retry:       evt.Behavior.Retry,
  Escalation:  evt.Behavior.Escalation,
  Abandonment: evt.Behavior.Abandonment,
 }
}
```

- [ ] **Step 2: 判异识别 + verdict 分级**

`internal/evaluation/application/observation_service.go` 新增方法（`applyJudge` 之后）：

```go
// applyAnomalyVerdict 判异识别（§4.2 判异触发）：规则命中 → block；行为异常或
// judge 跌阈 → flag；否则 pass。仅信号级结论，非权威判定（§4.3）。
func (s *ObservationService) applyAnomalyVerdict(resource string, obs *domain.EvalObservation) {
 if len(obs.Signals.Rule) > 0 {
  obs.Verdict = domain.VerdictBlock
  s.deps.Metrics.IncEvalBehaviorAnomaly(resource, "rule_block")
  s.deps.Metrics.IncEvalGateAction("detect", string(domain.VerdictBlock))
 }
 // block 优先级最高（T4 红线）：规则命中后，行为/judge 判异只独立计数、不降级 verdict。
 // 各 signal 维度独立计数（§11.1 eval_behavior_anomaly_total{resource, signal}）。
 if obs.Verdict != domain.VerdictBlock && obs.Signals.Behavior.Abandonment {
  obs.Verdict = domain.VerdictFlag
  s.deps.Metrics.IncEvalBehaviorAnomaly(resource, "behavior_abandonment")
  s.deps.Metrics.IncEvalGateAction("detect", string(domain.VerdictFlag))
 }
 if obs.Verdict != domain.VerdictBlock && obs.Signals.Behavior.Escalation {
  obs.Verdict = domain.VerdictFlag
  s.deps.Metrics.IncEvalBehaviorAnomaly(resource, "behavior_escalation")
  s.deps.Metrics.IncEvalGateAction("detect", string(domain.VerdictFlag))
 }
 if obs.Verdict != domain.VerdictBlock && anyJudgeBelow(obs.Signals.Judge, constants.JudgeBelowThreshold) {
  obs.Verdict = domain.VerdictFlag
  s.deps.Metrics.IncEvalBehaviorAnomaly(resource, "judge_below_threshold")
  s.deps.Metrics.IncEvalGateAction("detect", string(domain.VerdictFlag))
 }
}
```

> verdict 取最高优先级（block > flag > pass），规则命中后行为/judge 判异仅独立计数不覆盖（见代码守卫）。`anyJudgeBelow` 改用 `constants.JudgeBelowThreshold`，并把 `applyJudge`（:156-158）里的 `0.5` 同步替换为常量。

`Process`（:62-70）在 `buildObservation` 后、`applyJudge` 前调用：

```go
 obs := s.buildObservation(ctx, evt, trace)
 s.applyAnomalyVerdict(string(obs.Resource.Kind), &obs)
```

（verdict 初始为 pass；applyJudge 也会设 flag——两个路径叠加无冲突，rule/behavior 判异在 judge 前先标，judge 跌阈后标 flag 保持 flag 不变。）

- [ ] **Step 3: eval_sample_coverage 累计**

`ObservationService` struct（:30-32）加字段：

```go
type ObservationService struct {
 deps ObservationServiceDeps

 mu      sync.Mutex
 arrival map[string]int64 // resource -> 观测事件到达数
 sampled map[string]int64 // resource -> 采样通过数
}
```

`NewObservationService`（:34-39）初始化：

```go
 return &ObservationService{deps: deps, arrival: make(map[string]int64), sampled: make(map[string]int64)}
```

`Process` 里采样决策处插入计数（`:46-51` 区域，`Enabled` 检查后、`sampleDecision` 前后）：

```go
 if !s.deps.Enabled(ctx) {
  return nil
 }
 s.recordArrival(evt.ResourceKind)
 if !sampleDecision(s.deps.SampleRate(ctx), evt.ResourceKind, evt.TraceID) {
  return nil
 }
```

`Process` 落库成功后（`:85` `IncEvalObservation` 前）插入：

```go
 s.recordSampled(evt.ResourceKind)
 s.deps.Metrics.IncEvalObservation(evt.ResourceKind, string(obs.Verdict))
```

同文件追加私有 helper（`Process` 之后）：

```go
// recordArrival 累计某资源的观测事件到达并刷新采样覆盖率（分母）。
func (s *ObservationService) recordArrival(resource string) {
 s.mu.Lock()
 s.arrival[resource]++
 s.flushCoverageLocked(resource)
 s.mu.Unlock()
}

// recordSampled 累计某资源的采样通过数并刷新覆盖率（分子）。
func (s *ObservationService) recordSampled(resource string) {
 s.mu.Lock()
 s.sampled[resource]++
 s.flushCoverageLocked(resource)
 s.mu.Unlock()
}

// flushCoverageLocked 按当前累计值写 eval_sample_coverage Gauge（ratio = 采样/到达）。
func (s *ObservationService) flushCoverageLocked(resource string) {
 arrival := s.arrival[resource]
 if arrival == 0 {
  return
 }
 ratio := float64(s.sampled[resource]) / float64(arrival)
 s.deps.Metrics.RecordEvalSampleCoverage(resource, ratio)
}
```

> 并发安全：`Process` 由 consumer worker 单 goroutine 调用（`consumer.go` runConsumerLoop 串行），但锁保证后续多消费者/测试并发安全。`map` 在 `NewObservationService` 初始化，避免 nil map 写入。

- [ ] **Step 4: 测试**

`internal/evaluation/application/observation_service_test.go` 追加：

```go
t.Run("rule hit verdict block", func(t *testing.T) {
 // 事件带 RuleSignals=[{Rule:"tool_denylist"}]；Process 后 Save 收到 obs.Verdict==block。
})
t.Run("behavior abandonment verdict flag", func(t *testing.T) {
 // 事件带 Behavior.Abandonment=true；Process 后 obs.Verdict==flag。
})
t.Run("sample coverage recorded", func(t *testing.T) {
 // Enabled=true、SampleRate=1.0；Process 一次后断言 metrics mock 收到
 // RecordEvalSampleCoverage("agent", 1.0)（mock 实现 MetricsProvider 记录调用）。
})
```

用例结构参照现有表驱动测试；metrics mock 需实现 Task 1 新增的 5 个方法（若 `observation_service_test.go` 里已有 metrics stub，补充实现）。

- [ ] **Step 5: 编译 + 测试 + 提交**

```bash
go build ./...
go vet ./internal/evaluation/...
go test -short ./internal/evaluation/...
```

预期：PASS。提交：

```bash
git add internal/evaluation/application/observation_service.go pkg/constants/evaluation.go internal/evaluation/application/observation_service_test.go
git commit -m "feat(evaluation): 判异识别分级与 eval_sample_coverage 采样覆盖恢复

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 监控告警规则（stratum-evaluation + runbook + promtool + 渲染）

**Files:**

- Create: `monitoring/remote/rules/stratum-evaluation.yaml`
- Create: `docs/operations/alerts/stratum-evaluation.md`
- Create: `monitoring/remote/rules/stratum-evaluation_test.yaml`
- Modify: `monitoring/remote/generated/stratum-prometheus-rules.yaml`（由渲染脚本生成，禁止手改）
- Modify: `monitoring/local/rules/*.yml`（由渲染脚本生成）
- Verify: `scripts/quality/monitoring-config-test.sh`、`scripts/quality/render-monitoring-rules.sh`

**Interfaces:**

- Consumes: Task 1/6 落地的指标 `eval_behavior_anomaly_total{resource,signal}`、`eval_sample_coverage{resource}`、`eval_rule_hit_total{rule,resource,verdict}`；`monitoring/remote/rules/stratum-ai.yaml` 的规则格式先例（:4-16）；`monitoring/remote/alertmanager/alertmanager.yaml` 的 route（severity=critical 24/7、warning 仅工作日 9-19）。
- Produces: 3 条 Prometheus 告警规则 + runbook 锚点 + promtool test。T1 计数无告警；T2 行为异常判异 warning；§14 采样覆盖不足 warning；T4 红线规则命中 critical（24/7）。

- [ ] **Step 1: 读先例与渲染契约**

读 `monitoring/remote/rules/stratum-ai.yaml` 前 20 行（格式 + labels 结构）、`scripts/quality/monitoring-config-test.sh`（runbook 契约：锚点格式 + alertmanager 双写 + promtool 命令）、`scripts/quality/render-monitoring-rules.sh`（remote-test/local 渲染命令）。确认 `stratum-evaluation.yaml` 会被自动纳入（`rules/*.yaml` 通配）。

- [ ] **Step 2: 写告警规则**

创建 `monitoring/remote/rules/stratum-evaluation.yaml`（首行必须是 `groups:`）：

```yaml
groups:
  - name: stratum-evaluation
    rules:
      - alert: StratumEvalBehaviorAnomaly
        expr: |
          rate(eval_behavior_anomaly_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
          service: evaluation
          component: runtime-observation
          environment: remote-test
        annotations:
          summary: 评测行为异常判异升高
          description: >-
            运行态观测行为异常判异速率 {{ $value | printf "%.2f" }}/s（signal 维度见
            labels），持续 5 分钟。检查判异来源（judge 跌阈 / 行为放弃 / 规则命中）并
            到评测中心下钻对应 trace。
          dashboard_url: https://stratum.grafana/d/evaluation
          runbook_url: /docs/operations/alerts/stratum-evaluation.md#stratum-eval-behavior-anomaly
      - alert: StratumEvalSampleCoverageLow
        expr: |
          eval_sample_coverage < 0.5
        for: 10m
        labels:
          severity: warning
          service: evaluation
          component: runtime-observation
          environment: remote-test
        annotations:
          summary: 评测主动采样覆盖率不足
          description: >-
            eval_sample_coverage（采样通过/事件到达）低于 0.5 持续 10 分钟，
            可能整层被静默跳过。核对采样率参数与 judge 可用性，禁止静默跳过某层（§14）。
          dashboard_url: https://stratum.grafana/d/evaluation
          runbook_url: /docs/operations/alerts/stratum-evaluation.md#stratum-eval-sample-coverage-low
      - alert: StratumEvalRuleBlocked
        expr: |
          rate(eval_rule_hit_total{verdict="block"}[5m]) > 0
        for: 5m
        labels:
          severity: critical
          service: evaluation
          component: runtime-observation
          environment: remote-test
        annotations:
          summary: 规则护栏命中拦截
          description: >-
            规则护栏即时拦截速率 > 0（rule={{ $labels.rule }}），持续 5 分钟。
            T4 红线级：按 runbook 立即人工确认并处置。
          dashboard_url: https://stratum.grafana/d/evaluation
          runbook_url: /docs/operations/alerts/stratum-evaluation.md#stratum-eval-rule-blocked
```

> `for`/`expr` 阈值仅作起点；若 `monitoring-config-test.sh` 对阈值/expr 有更强契约，以守卫为准。severity 分级：warning → 飞书工作日 9-19；critical → 24/7（`alertmanager.yaml` 既有 route，无需改动）。

- [ ] **Step 3: 写 runbook**

创建 `docs/operations/alerts/stratum-evaluation.md`（锚点契约：`<a id="anchor"></a>` 空行后 `## <AlertName>`）：

```markdown
# Stratum Evaluation 告警 Runbook

评测运行态观测层（Phase 1）告警处置手册。

## 通用处置

- 所有判异类告警先到评测中心（运行态观测视图）按 trace 下钻，确认是否为真实能力退化。
- 告警只做通知与定位，处置动作（回滚/调参/拦截规则调整）走既有操作台与 CD 流程，禁止远端手改。
- 规则护栏（T1/T4）命中即拦截，属 fail closed 预期行为；持续命中说明平台规则需重新评估。

<a id="stratum-eval-behavior-anomaly"></a>

## StratumEvalBehaviorAnomaly

行为异常判异速率升高（judge 跌阈 / 行为放弃 / 行为升级）。

- 定位：查询 `eval_behavior_anomaly_total` 按 signal 分组，定位具体异常维度与 resource。
- 确认：到评测中心下钻对应 trace，核对 judge 分数与行为信号来源（feedback 或 agent 埋点）。
- 处置：真能力退化 → 走参数/版本调整；误报 → 调整判异阈值或信号映射。

<a id="stratum-eval-sample-coverage-low"></a>

## StratumEvalSampleCoverageLow

主动采样覆盖率低于阈值，可能整层静默跳过。

- 定位：核对 `evaluation.observe.sample_rate` 与 `evaluation.observe.enabled`、judge 可用性。
- 处置：调高采样率或恢复 judge；覆盖率长期低说明观测失去代表性（§14 禁止静默跳过某层）。

<a id="stratum-eval-rule-blocked"></a>

## StratumEvalRuleBlocked

规则护栏即时拦截命中（T4 红线级）。

- 定位：按 `rule` label 定位命中规则与工具；查询 `eval_rule_hit_total` 按 tool 分布。
- 处置：T4 强制人工确认——评估该工具是否应继续禁用、denylist 是否需调整，经审批后在平台参数更新
  `evaluation.ruleguard.denylist`，再回归验证。禁止自动放行（fail closed，§14）。
```

- [ ] **Step 4: 写 promtool test**

创建 `monitoring/remote/rules/stratum-evaluation_test.yaml`：

```yaml
rule_files:
  - stratum-evaluation.yaml

evaluation_interval: 1m

tests:
  - interval: 1m
    input_series:
      - series: 'eval_behavior_anomaly_total{resource="agent", signal="judge_below_threshold"}'
        values: '0 0 0 0 0 1 1 1 1 1 1'
      - series: 'eval_sample_coverage{resource="agent"}'
        values: '1 1 0.4 0.4 0.4 0.4 0.4 0.4 0.4 0.4 0.4 0.4'
      - series: 'eval_rule_hit_total{rule="tool_denylist", resource="agent", verdict="block"}'
        values: '0 0 0 0 0 1 1 1 1 1 1'
    alert_rule_test:
      - eval_time: 10m
        alertname: StratumEvalBehaviorAnomaly
        exp_alerts: []
      - eval_time: 11m
        alertname: StratumEvalBehaviorAnomaly
        exp_alerts:
          - exp_labels:
              severity: warning
              service: evaluation
              component: runtime-observation
            exp_annotations:
              summary: 评测行为异常判异升高
      - eval_time: 10m
        alertname: StratumEvalSampleCoverageLow
        exp_alerts:
          - exp_labels:
              severity: warning
      - eval_time: 11m
        alertname: StratumEvalRuleBlocked
        exp_alerts:
          - exp_labels:
              severity: critical
```

> 若现有 `stratum-ai.yaml` 已有 test 文件先例（`stratum-ai_test.yaml`），按其断言风格对齐（`exp_alerts` 是否要求全字段）。`input_series` 时间点与 `eval_time` 需与 `for` 时长匹配（示例仅为骨架，实施时按实际 `for` 校准）。

- [ ] **Step 5: 渲染 generated + 本地**

```bash
bash scripts/quality/render-monitoring-rules.sh remote-test
bash scripts/quality/render-monitoring-rules.sh local
```

预期：`monitoring/remote/generated/stratum-prometheus-rules.yaml` 与 `monitoring/local/rules/*.yml` 更新（含新规则）。

- [ ] **Step 6: 跑守卫**

```bash
bash scripts/quality/monitoring-config-test.sh
```

预期：PASS（含 promtool check rules / test、runbook 锚点契约、alertmanager 双写比对、render --check 无漂移）。

- [ ] **Step 7: 提交**

```bash
git add monitoring/remote/rules/stratum-evaluation.yaml monitoring/remote/rules/stratum-evaluation_test.yaml docs/operations/alerts/stratum-evaluation.md monitoring/remote/generated/stratum-prometheus-rules.yaml monitoring/local/rules/
git commit -m "feat(monitoring): 评测判异告警规则 + runbook + promtool test（T2/T4 飞书）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review 记录

**Spec 覆盖核对（§13 Phase 1 逐项）：**

- 规则护栏内联（快路径）→ Task 3（拦截）+ Task 4（信号传递）+ Task 6（verdict block）。
- 行为信号采集 → Task 4（agent 埋点）+ Task 5（feedback 推导，两者都做）。
- Stratum tier → Task 2。
- 判异触发（judge 跌阈/规则命中/行为异常）→ Task 6（识别+指标+通知；待审核池留 P1c）。
- 上升人类分级 T1–T4 → Task 3（T1 计数）+ Task 7（T2 warning / T4 critical 24/7 分级框架；T3 评审池依赖 P1c 池本体）。
- 飞书报警接入 → Task 7（复用 alertmanager 既有 route，不新建通道）。
- 评测 meta 指标补齐 → Task 1（新增 4 指标）+ Task 6（eval_sample_coverage 恢复）。
- P1a 降级项：`observation_service.go:114` Stratum → Task 2；`signals.rule/behavior` → Task 4+6；`eval_rule_hit_total/eval_behavior_anomaly_total` → Task 1+3+6；`provider.go:129`/`prometheus.go:341` eval_sample_coverage → Task 1+6。

**Placeholder 扫描：** 已消除全部 TBD/「待后续」类表述；每任务含可运行测试与精确命令。wiring 装配处（Task 2/3/5）标注「先读既有装配确认变量名」，属必要上下文读取而非占位。

**类型一致性：** `domain.RuleBlock`（agent）与 `port.RuleSignalPayload`/`BehaviorSignalPayload`（agent+evaluation 镜像）命名一致；`BehaviorSignals`（evaluation domain）贯穿 Task 5/6；`constants.JudgeBelowThreshold` 供 Task 5（feedback 阈值）与 Task 6（judge 跌阈）共用。
