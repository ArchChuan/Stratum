# P1a：运行态观测基础设施 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Stratum 落地运行态观测垂直切片：生产 agent 执行完成 → 发布 NATS 轻量引用事件 → 评测服务采样 → 从 Opik 拉证据 → LLM judge 异步打分 → 落 `EvalObservation` 明细 → 暴露查询 API 与 meta 指标。

**Architecture:** 采用规格 §2.2「独立评测管线（旁路评分器）」：规则护栏之外的一切 judge/观测异步离线。agent 执行成功路径经注入的 `ObservationEmitter`（nil-safe、best-effort）发布 `evaluation.observe.{tenant}` 引用事件（只带 trace_id 与资源锚点）；评测侧 NATS 消费 worker 采样后从 Opik 拉取证据、LLM judge 多维打分，组装 `EvalObservation` 落库并更新 meta 指标。评估器不阻断执行：发布失败仅记日志，judge 不可用采样降级跳过，证据查询失败走 NATS 重投 → DLQ。本计划是 Phase 1 的第一个垂直切片，judge 维度按规格 §3.1（faithfulness/relevance/completeness），`stratum` 与平台参数版本锚点按规格 §14 允许以空/unknown 标记（P1b/P2 补齐），全程 fail closed。

**Tech Stack:** Go 1.25.12、Gin v1.9.1、pgx v5.9.2（tenant-schema 隔离 + `execTenantTx`）、NATS JetStream v1.51.0（引用事件传输 + DLQ）、Zap v1.27.1、Opik trace 证据（`internal/agent/infrastructure/opik`）、Prometheus（`pkg/observability`）。

## Global Constraints

- **评估器不阻断执行铁律**（规格 §14 / 会话红线）：judge/证据/观测任何故障都不得阻断 agent 执行。agent 侧发布观测事件必须 nil-safe + best-effort（失败仅 warn 日志）。
- **fail closed**：`EvalObservation` 落库前必须 `Validate()`；非法数据拒绝落库并上报指标。观测开关默认 `false`，未显式开启不得采样。
- **平台参数永不自动回滚**；本计划不涉及任何回滚逻辑。
- **tenant-scoped 数据**：`eval_observations` 表 DDL 只放 `pkg/storage/postgres/tenant_schema.sql`（禁止放 `pkg/migration/sql/`）；所有 repository 方法经 `execTenantTx(ctx, pool, tenantID, fn)`（`pool_iface.go` 无 `execTenant`/`execTenantQuery`，读写在 tx 内 `tx.QueryRow`/`tx.Query`/`tx.Exec`），port 方法签名必须显式含 `tenantID string`。
- **幂等 DDL**：新表 `CREATE TABLE IF NOT EXISTS` + 索引 `CREATE INDEX IF NOT EXISTS`；JSONB 列先建表后无需 backfill（全新增列无存量租户缺列问题）。
- **pgx v5 JSONB**：向 JSONB 写自定义 Go struct 必须 `json.Marshal` 后传 `string(b)`，禁止直接传 struct 或 `pgtype.JSONB{}`。
- **指标名对齐规格 §11**：`eval_observation_total{resource,stratum}`、`eval_judge_score{resource,dimension}`、`eval_sample_coverage{resource}`、`eval_judge_latency_seconds`、`eval_judge_cost_total`、`eval_judge_failure_total{reason}`、`eval_queue_backlog{queue}`。
- **观测参数只注册、不播种**：`PlatformValues` 对快照缺失 key 回退 registry default，新参数（`evaluation.observe.enabled`/`sample_rate`）在 registry 注册即生效，禁止新增 `pkg/migration/sql/` 迁移。
- **错误逐层 wrap**：`fmt.Errorf("operation: %w", err)`；日志只用 Zap，禁止 `fmt.Print`。
- **依赖方向**：`handler → application → domain/port`；infrastructure 实现 port。`application` 不 import NATS/pgx；`infrastructure` 不 import 兄弟 context 的 application。
- **行为数字不内联**：NATS 超时/投递数/保留期、采样默认值全部进 `pkg/constants/evaluation.go`。
- **commit 格式**：`[type](scope): description`（type: `feat|fix|refactor|test|docs`），消息末尾 `Co-Authored-By: Claude <noreply@anthropic.com>`。禁止在 `main` 分支直接提交。

## 范围说明（本计划边界）

本计划实现规格 §13 Phase 1 中的**运行态观测基础设施**垂直切片（Phase 1 拆为四个独立计划，本计划为 P1a）：

| 计划 | 内容 | 状态 |
|---|---|---|
| **P1a（本计划）** | `EvalObservation` 领域模型 + tenant DDL + repository + NATS 引用事件发布/消费 + 采样策略 + judge 异步打分 + 证据链 input/output 扩展 + meta 指标 + 查询 API + wiring 装配 | 本计划 |
| P1b | 规则护栏内联（快路径）+ 行为信号采集 + T1–T4 上升分级 + 飞书告警 + 判异触发 | 后续计划 |
| P1c | 评测集两条新产线：KB 派生、运行态沉淀（判异 → 待审核池 → 人工确认） | 后续计划 |
| P1d | 前端两视图：运行态健康分趋势 + 门禁状态（规格 §10.1/10.2） | 后续计划 |

**P1a 内的明确降级**（规格 §14 允许，后续期补齐，落地时以 `// TODO(P1b)`/`// TODO(P2)` 标注）：

- `stratum`（tenant_tier）先落空字符串：租户 tier 解析留 P1b。
- `param_version.platform`（平台层版本锚点）先落 `unknown`：配置版本机制绑定留 Phase 2。
- `signals.rule` / `signals.behavior` 先为空：规则护栏（P1b）与行为信号（P1b）填充。
- `eval_rule_hit_total` / `eval_behavior_anomaly_total` 指标留 P1b。

## 文件结构

```
internal/evaluation/domain/observation.go            # Task 1  EvalObservation 模型 + Validate
internal/evaluation/domain/observation_test.go       # Task 1
internal/evaluation/domain/observation_event.go      # Task 2  评测侧引用事件解析结构
internal/evaluation/domain/observation_event_test.go # Task 2  JSON 契约对齐测试
internal/agent/domain/port/observation.go            # Task 2  ObservationEmitter 接口 + ObservationEvent 结构
internal/agent/domain/port/observation_test.go       # Task 2  JSON golden 测试
pkg/constants/evaluation.go                          # Task 2  观测常量（Modify）
internal/parameters/domain/registry.go               # Task 2  registerObservationParams（Modify）
internal/agent/domain/evidence.go                    # Task 3  TraceEvidence 加 Input/Output（Modify）
internal/agent/infrastructure/opik/mapper.go         # Task 3  mapEvidence 填充 Input/Output（Modify）
internal/evaluation/domain/port/evaluation.go        # Task 3  ObservedTrace 加 Input/Output（Modify）
api/wiring/evaluation.go                             # Task 3  mapEvaluationEvidence 映射（Modify）
pkg/observability/provider.go                        # Task 4  MetricsProvider 接口 + NoopMetrics（Modify）
pkg/observability/prometheus.go                      # Task 4  PrometheusMetrics 实现（Modify）
pkg/observability/prometheus_metrics_test.go         # Task 4  指标单测
internal/agent/application/agent_service.go          # Task 5  AgentServiceDeps + Setter（Modify）
internal/agent/application/agent_execution.go        # Task 5  Execute/ExecuteStream 挂载 + emitObservation（Modify）
internal/agent/application/observation_emit_test.go  # Task 5  挂载单测
api/wiring/observation_emitter.go                    # Task 6  NATS 观测发布 adapter（Create）
api/wiring/observation_emitter_test.go               # Task 6  单测（mock JetStream）
internal/evaluation/application/observation_sampling.go      # Task 7  采样策略（Create）
internal/evaluation/application/observation_sampling_test.go # Task 7
internal/evaluation/domain/port/observation_repo.go  # Task 8  ObservationRepository port（Create）
pkg/storage/postgres/tenant_schema.sql               # Task 8  eval_observations 表 DDL（Modify）
internal/evaluation/infrastructure/persistence/observation_repository.go      # Task 8  pgx 实现
internal/evaluation/infrastructure/persistence/observation_repository_test.go # Task 8  pgxmock
internal/evaluation/application/observation_service.go      # Task 9  落地服务（Create）
internal/evaluation/application/observation_service_test.go # Task 9
internal/evaluation/infrastructure/observation/dead_letter.go  # Task 10 DLQ（Create）
internal/evaluation/infrastructure/observation/consumer.go    # Task 10 NATS 消费 worker（Create）
internal/evaluation/infrastructure/observation/consumer_test.go # Task 10
api/http/handler/evaluation_handler.go                # Task 11 查询端点 + WithObservationService（Modify）
api/http/router.go                                    # Task 11 路由（Modify）
api/http/contract_test.go + testdata/contracts/*.golden.json # Task 11 契约（Modify）
api/wiring/evaluation.go                              # Task 12 wiring 装配（Modify）
api/wiring/agent.go                                   # Task 12 agent emitter 注入（Modify）
```

---

### Task 1: EvalObservation 领域模型

**Files:**

- Create: `internal/evaluation/domain/observation.go`
- Test: `internal/evaluation/domain/observation_test.go`

**Interfaces:**

- Consumes: 无（纯领域类型，仅依赖 stdlib）。
- Produces: `domain.EvalObservation`、`domain.ObservationSignals`、`domain.JudgeSignal`、`domain.ParamVersion`、`domain.ObservationResourceRef`、`domain.CostPerf`、`domain.ObservationVerdict`（`VerdictPass|VerdictFlag|VerdictBlock`）、`domain.ParamSource`（`ParamSourceUnknown` 等）、方法 `(*EvalObservation).Validate() error`。后续 Task 8/9 依赖本任务的类型与校验语义。**注意：观测资源锚点用 `ObservationResourceRef`（kind + resource_id 两字段），不是现有 `domain.ResourceRef`（含 revision）**——见实现注释。

- [ ] **Step 1: 写失败测试**

```go
// internal/evaluation/domain/observation_test.go
package domain

import (
 "strings"
 "testing"
 "time"
)

func TestEvalObservationValidate(t *testing.T) {
 base := EvalObservation{
  ID: "obs-1", TraceID: "trace-1",
  Resource: ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"},
  Param: ParamVersion{
   Resource: ResourceParamVersion{Ref: "r1", Version: "v3"},
   Source:   ParamSourceResource,
  },
  Signals: ObservationSignals{Judge: []JudgeSignal{
   {Dimension: "faithfulness", Score: 0.9, Confidence: 0.85},
  }},
  CostPerf:  CostPerf{LatencyMS: 1200, Tokens: 3200, CostUSD: 0.012},
  Verdict:   VerdictPass,
  CreatedAt: time.Now().UTC(),
 }
 if err := base.Validate(); err != nil {
  t.Fatalf("valid observation rejected: %v", err)
 }

 cases := []struct {
  name    string
  mutate  func(*EvalObservation)
  wantSub string
 }{
  {name: "missing trace_id", mutate: func(o *EvalObservation) { o.TraceID = "" }, wantSub: "trace_id"},
  {name: "missing resource kind", mutate: func(o *EvalObservation) { o.Resource.Kind = "" }, wantSub: "resource kind"},
  {name: "missing resource id", mutate: func(o *EvalObservation) { o.Resource.ResourceID = "" }, wantSub: "resource id"},
  {name: "invalid verdict", mutate: func(o *EvalObservation) { o.Verdict = ObservationVerdict("bogus") }, wantSub: "verdict"},
  {name: "judge dimension empty", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Dimension = "" }, wantSub: "dimension"},
  {name: "judge score out of range", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Score = 1.5 }, wantSub: "score"},
  {name: "judge confidence out of range", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Confidence = -0.1 }, wantSub: "confidence"},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   obs := base
   tc.mutate(&obs)
   err := obs.Validate()
   if err == nil {
    t.Fatal("expected validation error, got nil")
   }
   if !strings.Contains(err.Error(), tc.wantSub) {
    t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
   }
  })
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/domain/ -run TestEvalObservationValidate -v`
Expected: FAIL（`observation.go` 不存在，编译错误）。

- [ ] **Step 3: 最小实现**

```go
// internal/evaluation/domain/observation.go
package domain

import (
 "fmt"
 "time"
)

// ObservationVerdict 是 EvalObservation 的仅信号级结论（§4.3），非权威判定。
type ObservationVerdict string

const (
 VerdictPass  ObservationVerdict = "pass"
 VerdictFlag  ObservationVerdict = "flag"
 VerdictBlock ObservationVerdict = "block"
)

// ParamSource 标记关键参数实际生效的来源层级（§4.3）。
type ParamSource string

const (
 ParamSourcePlatform  ParamSource = "platform"
 ParamSourceResource  ParamSource = "resource"
 ParamSourceBoth      ParamSource = "both"
 ParamSourceUnknown   ParamSource = "unknown"
)

// PlatformParamVersion 平台层生效版本锚点。P1a 阶段配置版本机制未绑定，
// 统一标记 unknown；Phase 2 绑定后回填。
type PlatformParamVersion struct {
 GroupKey   string `json:"group_key"`
 VersionSeq int64  `json:"version_seq"`
}

// ResourceParamVersion 租户资源配置版本锚点（来自执行证据的 Assignments）。
type ResourceParamVersion struct {
 Ref     string `json:"ref"`
 Version string `json:"version"`
}

// ParamVersion 双版本锚点（§4.3/§3.4）。
type ParamVersion struct {
 Platform PlatformParamVersion `json:"platform"`
 Resource ResourceParamVersion `json:"resource"`
 Source   ParamSource          `json:"source"`
}

// RuleSignal 规则护栏命中信号（P1b 填充）。
type RuleSignal struct {
 Rule    string `json:"rule"`
 Message string `json:"message"`
}

// JudgeSignal LLM judge 单维度打分（score/confidence 归一化到 [0,1]）。
type JudgeSignal struct {
 Dimension  string  `json:"dimension"`
 Score      float64 `json:"score"`
 Confidence float64 `json:"confidence"`
}

// BehaviorSignals 用户行为信号（P1b 填充）。
type BehaviorSignals struct {
 Retry       bool `json:"retry"`
 Escalation  bool `json:"escalation"`
 Abandonment bool `json:"abandonment"`
}

// ObservationSignals 观测信号集合。
type ObservationSignals struct {
 Rule     []RuleSignal    `json:"rule"`
 Judge    []JudgeSignal   `json:"judge"`
 Behavior BehaviorSignals `json:"behavior"`
}

// CostPerf 成本性能。
type CostPerf struct {
 LatencyMS int64   `json:"latency_ms"`
 Tokens    int64   `json:"tokens"`
 CostUSD   float64 `json:"cost_usd"`
}

// ObservationResourceRef 观测对象资源锚点（kind=agent 时 ResourceID 为 agent_id）。
// 与领域 ResourceRef（含 revision_id）不同：观测锚点只含 kind + resource_id，
// revision 语义由 param_version.resource 承载（spec §4.3 resource(kind,id)）。
type ObservationResourceRef struct {
 Kind       ResourceKind `json:"kind"`
 ResourceID string       `json:"resource_id"`
}

// EvalObservation 一次运行态观测（规格 §4.3）。
type EvalObservation struct {
 ID        string             `json:"id"`
 TraceID   string             `json:"trace_id"`
 Resource  ObservationResourceRef `json:"resource"`
 Param     ParamVersion       `json:"param_version"`
 Signals   ObservationSignals `json:"signals"`
 CostPerf  CostPerf           `json:"cost_perf"`
 Stratum   string             `json:"stratum"`
 Verdict   ObservationVerdict `json:"verdict"`
 CreatedAt time.Time          `json:"created_at"`
}

// Validate 校验观测字段，非法返回错误。fail closed：不允许坏数据落库。
func (o *EvalObservation) Validate() error {
 if o.TraceID == "" {
  return fmt.Errorf("evaluation observation: trace_id required")
 }
 if o.Resource.Kind == "" {
  return fmt.Errorf("evaluation observation: resource kind required")
 }
 if o.Resource.ResourceID == "" {
  return fmt.Errorf("evaluation observation: resource id required")
 }
 switch o.Verdict {
 case VerdictPass, VerdictFlag, VerdictBlock:
 default:
  return fmt.Errorf("evaluation observation: invalid verdict %q", o.Verdict)
 }
 for _, j := range o.Signals.Judge {
  if j.Dimension == "" {
   return fmt.Errorf("evaluation observation: judge dimension required")
  }
  if j.Score < 0 || j.Score > 1 {
   return fmt.Errorf("evaluation observation: judge score %v out of [0,1]", j.Score)
  }
  if j.Confidence < 0 || j.Confidence > 1 {
   return fmt.Errorf("evaluation observation: judge confidence %v out of [0,1]", j.Confidence)
  }
 }
 return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/evaluation/domain/ -run TestEvalObservationValidate -v`
Expected: PASS（7 个子用例全部通过）。

- [ ] **Step 5: 提交**

```bash
git add internal/evaluation/domain/observation.go internal/evaluation/domain/observation_test.go
git commit -m "feat(evaluation): EvalObservation 领域模型与校验

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 观测事件结构与观测常量（+ 参数注册）

**Files:**

- Create: `internal/agent/domain/port/observation.go`
- Test: `internal/agent/domain/port/observation_test.go`
- Create: `internal/evaluation/domain/observation_event.go`
- Test: `internal/evaluation/domain/observation_event_test.go`
- Modify: `pkg/constants/evaluation.go`
- Modify: `internal/parameters/domain/registry.go`

**Interfaces:**

- Consumes: 无。
- Produces:
  - `agentport.ObservationEvent`（JSON 字段契约：`tenant_id/trace_id/execution_id/agent_id/resource_kind/resource_id/completed_at`）与 `agentport.ObservationEmitter interface { Emit(ctx context.Context, evt ObservationEvent) error }` —— Task 5/6 消费。
  - `domain.ObservationReferenceEvent`（评测侧解析结构，与 agent 事件字段逐一对应）—— Task 9/10 消费。
  - `constants.ObservationStream` / `ObservationSubjectPrefix` / `ObservationConsumerName` / `ObservationAckWait` / `ObservationMaxDeliver` / `ObservationStreamMaxAge` / `ObservationDLQMaxAge` / `ObservationSampleRateDefault` —— Task 6/10 消费。
  - 平台参数 `evaluation.observe.enabled`（TypeBool, default false）、`evaluation.observe.sample_rate`（TypeFloat, default 0.1）—— Task 7/9 读取。

- [ ] **Step 1: 写失败测试（agent 侧事件 golden）**

```go
// internal/agent/domain/port/observation_test.go
package port

import (
 "encoding/json"
 "testing"
)

func TestObservationEventJSON(t *testing.T) {
 evt := ObservationEvent{
  TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
  AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
  CompletedAt: "2026-08-28T12:00:00.123Z",
 }
 raw, err := json.Marshal(evt)
 if err != nil {
  t.Fatalf("marshal: %v", err)
 }
 want := `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z"}`
 if string(raw) != want {
  t.Fatalf("JSON mismatch\n got %s\nwant %s", raw, want)
 }
}
```

- [ ] **Step 2: 写失败测试（评测侧解析结构 + 契约对齐）**

```go
// internal/evaluation/domain/observation_event_test.go
package domain

import (
 "encoding/json"
 "testing"
)

// 契约样例：与 internal/agent/domain/port 的 ObservationEvent JSON 逐字段对齐。
// 若 agent 侧改动事件字段，此 golden 会失败，倒逼同步两侧定义。
const observationEventGolden = `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z"}`

func TestObservationReferenceEventUnmarshal(t *testing.T) {
 var evt ObservationReferenceEvent
 if err := json.Unmarshal([]byte(observationEventGolden), &evt); err != nil {
  t.Fatalf("unmarshal: %v", err)
 }
 if evt.TenantID != "t1" || evt.TraceID != "trace-1" || evt.ExecutionID != "exec-1" {
  t.Fatalf("identity fields mismatch: %+v", evt)
 }
 if evt.AgentID != "agent-1" || evt.ResourceKind != "agent" || evt.ResourceID != "agent-1" {
  t.Fatalf("resource fields mismatch: %+v", evt)
 }
 if evt.CompletedAt != "2026-08-28T12:00:00.123Z" {
  t.Fatalf("completed_at mismatch: %q", evt.CompletedAt)
 }
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/agent/domain/port/ ./internal/evaluation/domain/ -run "TestObservationEventJSON|TestObservationReferenceEventUnmarshal" -v`
Expected: FAIL（`port/observation.go`、`domain/observation_event.go` 不存在，编译错误）。

- [ ] **Step 4: 实现 agent 侧事件 + 评测侧解析结构**

```go
// internal/agent/domain/port/observation.go
package port

import "context"

// ObservationEvent 是 agent 执行成功后发布的轻量引用事件（规格 §2.2）。
// 只携带 trace 标识与资源锚点，证据本体（Opik）由评测服务拉取，禁止在
// 事件里携带 prompt/输出等 payload（观测埋点只做引用、不做 payload 双写）。
type ObservationEvent struct {
 TenantID    string `json:"tenant_id"`
 TraceID     string `json:"trace_id"`
 ExecutionID string `json:"execution_id"`
 AgentID     string `json:"agent_id"`
 ResourceKind string `json:"resource_kind"`
 ResourceID  string `json:"resource_id"`
 // CompletedAt RFC3339Nano 时间戳，评测侧解析为创建时间锚点。
 CompletedAt string `json:"completed_at"`
}

// ObservationEmitter 发布观测引用事件。实现必须 best-effort：失败只应记录
// 日志，绝不阻断 agent 执行（评估器不阻断执行铁律）。
type ObservationEmitter interface {
 Emit(ctx context.Context, evt ObservationEvent) error
}
```

```go
// internal/evaluation/domain/observation_event.go
package domain

// ObservationReferenceEvent 是评测侧消费的观测引用事件解析结构，字段与
// internal/agent/domain/port 的 ObservationEvent 逐一对应（由
// observation_event_test.go 的 golden 契约守护对齐）。
type ObservationReferenceEvent struct {
 TenantID    string `json:"tenant_id"`
 TraceID     string `json:"trace_id"`
 ExecutionID string `json:"execution_id"`
 AgentID     string `json:"agent_id"`
 ResourceKind string `json:"resource_kind"`
 ResourceID  string `json:"resource_id"`
 CompletedAt string `json:"completed_at"`
}

// ResourceRef 返回该事件的资源锚点（agent 执行观测对象，ObservationResourceRef）。
func (e ObservationReferenceEvent) ResourceRef() ObservationResourceRef {
 return ObservationResourceRef{Kind: ResourceKind(e.ResourceKind), ResourceID: e.ResourceID}
}
```

- [ ] **Step 5: 实现观测常量（`pkg/constants/evaluation.go` 追加）**

在 `pkg/constants/evaluation.go` 顶部 const 块追加（文件已 import `time`）：

```go
 // ObservationStream 承载运行态观测引用事件（WorkQueue 单消费语义）。
 ObservationStream = "evaluation-observe"
 // ObservationDLQStream 观测消费重投耗尽后的死信流。
 ObservationDLQStream = "evaluation-observe-dlq"
 // ObservationSubjectPrefix 引用事件 subject 前缀；完整 subject 为
 // "evaluation.observe.{tenant}"，与 memory 的 domain.action 命名同族。
 ObservationSubjectPrefix = "evaluation.observe"
 // ObservationConsumerName 观测 judge 消费组名。
 ObservationConsumerName = "observation-judge"
 // ObservationAckWait 消费确认窗口；ObservationMaxDeliver 重投上限，超限进 DLQ。
 ObservationAckWait     = 30 * time.Second
 ObservationMaxDeliver  = 3
 // ObservationFetchMaxWait 单次 Fetch 等待窗口；ObservationFetchBackoffBase /
 // ObservationFetchBackoffMax 消费退避与重投延迟的指数区间（重投 NakWithDelay 用 Base）。
 ObservationFetchMaxWait     = 5 * time.Second
 ObservationFetchBackoffBase = 1 * time.Second
 ObservationFetchBackoffMax  = 30 * time.Second
 // ObservationStreamMaxAge / ObservationDLQMaxAge 消息保留期。
 ObservationStreamMaxAge = 7 * 24 * time.Hour
 ObservationDLQMaxAge    = 30 * 24 * time.Hour
 // ObservationSampleRateDefault 采样率默认值（registry 兜底，运行时经平台参数覆盖）。
 ObservationSampleRateDefault = 0.1
 // ObservationPublishTimeout 引用事件发布超时预算（agent 侧 best-effort，超时不阻断）。
 ObservationPublishTimeout = 3 * time.Second
 // ObservationBacklogInterval 消费积压指标采集周期。
 ObservationBacklogInterval = 30 * time.Second
)
```

- [ ] **Step 6: 实现观测参数注册（`internal/parameters/domain/registry.go`）**

在 `registerJudgeParams` 函数后新增（并确认 `NewParametersRegistry` 的函数体内按字母序调用了 `r.registerObservationParams()`，与 `registerOptimizerParams` 等并列；找不到现成调用处则在该函数体末尾追加一行调用）：

```go
// registerObservationParams 是 Phase 1 运行态观测（§4.2）的平台级参数。
// enabled 默认 false：平台未显式开启时禁止采样（fail closed）。
// 仅注册不播种：PlatformValues 对快照缺失 key 回退 registry default。
func (r *ParametersRegistry) registerObservationParams() {
 f := func(v float64) *float64 { return &v }
 _ = r.Register(ParameterDefinition{
  Key: "evaluation.observe.enabled", Scope: ScopePlatform, Category: "evaluation",
  DisplayName: "启用运行态观测", Description: "是否对生产执行采样并异步 judge 打分(默认关)",
  ValueType: TypeBool, Default: false,
  VisualHint:  VisualHint{Control: ControlToggle},
  Optimizable: true,
 })
 _ = r.Register(ParameterDefinition{
  Key: "evaluation.observe.sample_rate", Scope: ScopePlatform, Category: "evaluation",
  DisplayName: "运行态观测采样率", Description: "生产 trace 采样比例 0-1(按资源分层确定性采样)",
  ValueType: TypeFloat, Default: 0.1,
  VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.05)},
  Optimizable: true,
 })
}
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/agent/domain/port/ ./internal/evaluation/domain/ ./internal/parameters/... -short -v`
Expected: PASS（两个新 golden 用例通过；参数 registry 存量测试不回归，新增 key 不破坏现有 Schema 断言——若现有 registry 测试断言 key 总数，需按实际断点同步更新，禁止删断言）。

- [ ] **Step 8: 提交**

```bash
git add internal/agent/domain/port/observation.go internal/agent/domain/port/observation_test.go \
        internal/evaluation/domain/observation_event.go internal/evaluation/domain/observation_event_test.go \
        pkg/constants/evaluation.go internal/parameters/domain/registry.go
git commit -m "feat(evaluation): 观测引用事件契约与观测平台参数

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 证据链扩展（Input/Output）

**Files:**

- Modify: `internal/agent/domain/evidence.go`（`TraceEvidence` 加 `Input`/`Output` 字段）
- Modify: `internal/agent/infrastructure/opik/mapper.go`（`mapEvidence` 填充）
- Modify: `internal/evaluation/domain/port/evaluation.go`（`ObservedTrace` 加 `Input`/`Output` 字段）
- Modify: `api/wiring/evaluation.go`（`mapEvaluationEvidence` 映射）
- Test: `internal/agent/infrastructure/opik/mapper_test.go`（追加用例）

**Interfaces:**

- Consumes: `agentdomain.TraceEvidence`（现状无 Input/Output）、`evalport.ObservedTrace`（现状无 Input/Output）。
- Produces: `TraceEvidence.Input/Output string`、`ObservedTrace.Input/Output string`。Task 9 落地服务用 `ObservedTrace.Input/Output` 组 judge 请求。

背景：规格 §3.1 的 judge 维度（faithfulness/relevance/completeness）需要实际输入/输出文本。Opik span DTO 已带 `Input/Output any`（`internal/agent/infrastructure/opik/dto.go`），但顶层 `TraceEvidence` 未暴露，导致评测侧拿不到可打分的文本。本任务打通该链。

- [ ] **Step 1: 写失败测试（mapper 填充 Input/Output）**

在 `internal/agent/infrastructure/opik/mapper_test.go` 追加（先读该文件了解现有 `opikTrace` 构造方式，沿用其构造）：

```go
func TestMapEvidenceCarriesInputOutput(t *testing.T) {
 trace := opikTrace{
  ID:    "opik-trace-1",
  Input: map[string]any{"query": "帮我总结上周会议"},
  Output: map[string]any{"text": "上周会议主要结论是……"},
 }
 evidence, err := mapEvidence(trace, nil)
 if err != nil {
  t.Fatalf("mapEvidence: %v", err)
 }
 if !strings.Contains(evidence.Input, "帮我总结上周会议") {
  t.Fatalf("Input missing user query: %q", evidence.Input)
 }
 if !strings.Contains(evidence.Output, "上周会议主要结论") {
  t.Fatalf("Output missing assistant text: %q", evidence.Output)
 }
}
```

> 注：`opikTrace` 结构是否已有 `Input/Output any` 字段以该文件为准；若尚无，先在 `dto.go` 的 `opikTrace` 追加与 span 一致的 `Input any`/`Output any` 字段，再写本测试。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/infrastructure/opik/ -run TestMapEvidenceCarriesInputOutput -v`
Expected: FAIL（`mapEvidence` 未填充 Input/Output，断言失败或编译失败）。

- [ ] **Step 3: 实现证据链扩展**

`internal/agent/domain/evidence.go` 的 `TraceEvidence` 追加两个字段（紧接 `LatencyMs` 后）：

```go
 Input              string
 Output             string
```

`internal/agent/infrastructure/opik/mapper.go` 的 `mapEvidence` 在 `StartedAt` 行后追加：

```go
  StartedAt: trace.StartTime, Input: textOf(trace.Input), Output: textOf(trace.Output),
```

文件内新增 `textOf` helper（放 `errorMessage` 附近）：

```go
// textOf 把 Opik span/trace 的 input/output（any：string、map、slice）转成
// judge 可读的纯文本。string 原样返回；复合类型 JSON 序列化（保持结构信息）；
// 其他类型 fmt.Sprintf。nil 返回空串。
func textOf(v any) string {
 if v == nil {
  return ""
 }
 if s, ok := v.(string); ok {
  return s
 }
 switch v.(type) {
 case map[string]any, []any:
  if raw, err := json.Marshal(v); err == nil {
   return string(raw)
  }
 }
 return fmt.Sprintf("%v", v)
}
```

`internal/evaluation/domain/port/evaluation.go` 的 `ObservedTrace` 追加（紧接 `LatencyMs` 后；`TotalTokens` 供 CostPerf.Tokens，`TraceEvidence` 已带该字段，由 mapEvidence 填充）：

```go
 Input              string
 Output             string
 TotalTokens        int64
```

`api/wiring/evaluation.go` 的 `mapEvaluationEvidence` 的返回结构追加：

```go
 return evalport.ObservedTrace{
  TraceID: evidence.TraceID, UserID: evidence.UserID, CostUSD: evidence.CostUSD, LatencyMs: evidence.LatencyMs,
  Input: evidence.Input, Output: evidence.Output, TotalTokens: evidence.TotalTokens,
  Success: evidence.Status == agentdomain.ExecStatusSuccess, SecurityViolation: evidence.SecurityViolation,
  Assignments: assignments,
 }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/infrastructure/opik/ -run TestMapEvidenceCarriesInputOutput -v`
Expected: PASS。
再跑编译检查（生成物未变的普通 Go 包直接编译验证）：

Run: `go build ./internal/agent/... ./internal/evaluation/... ./api/wiring/`
Expected: 编译成功。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/domain/evidence.go internal/agent/infrastructure/opik/mapper.go \
        internal/agent/infrastructure/opik/mapper_test.go \
        internal/evaluation/domain/port/evaluation.go api/wiring/evaluation.go
git commit -m "feat(evaluation): 证据链携带 input/output 供 judge 打分

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: MetricsProvider 评测观测指标扩展

**Files:**

- Modify: `pkg/observability/provider.go`（接口 + NoopMetrics）
- Modify: `pkg/observability/prometheus.go`（PrometheusMetrics 实现）
- Test: `pkg/observability/prometheus_metrics_test.go`（追加用例）

**Interfaces:**

- Consumes: `observability.MetricsProvider`（现状仅 `IncEvaluationJob`）。
- Produces: 7 个新方法（Task 9/10 消费，指标名对齐规格 §11）：
  - `IncEvalObservation(resource, verdict string)` → `eval_observation_total{resource,stratum}`（stratum 标签 P1b 补，当前空串）
  - `RecordEvalJudgeScore(resource, dimension string, score float64)` → `eval_judge_score{resource,dimension}` Histogram
  - `SetEvalSampleCoverage(resource string, pct float64)` → `eval_sample_coverage{resource}` Gauge
  - `RecordEvalJudgeLatency(seconds float64)` → `eval_judge_latency_seconds` Histogram
  - `RecordEvalJudgeCost(costUSD float64)` → `eval_judge_cost_total` Counter
  - `IncEvalJudgeFailure(reason string)` → `eval_judge_failure_total{reason}` Counter
  - `SetEvalQueueBacklog(queue string, count int64)` → `eval_queue_backlog{queue}` Gauge

- [ ] **Step 1: 写失败测试（Prometheus 指标）**

先读 `pkg/observability/prometheus_metrics_test.go` 与 `prometheus_test.go` 了解现有断言工具（gather + family 查找），追加：

```go
func TestEvalObservationMetrics(t *testing.T) {
 m := NewPrometheusMetrics()
 m.IncEvalObservation("agent", "pass")
 m.IncEvalObservation("agent", "pass")
 m.RecordEvalJudgeScore("agent", "faithfulness", 0.9)
 m.SetEvalSampleCoverage("agent", 0.5)
 m.RecordEvalJudgeLatency(1.5)
 m.RecordEvalJudgeCost(0.012)
 m.IncEvalJudgeFailure("evidence_missing")
 m.SetEvalQueueBacklog("observation", 7)

 families := gather(t, m)
 // 断言方法与指标名、label 维度对齐规格 §11。
 assertCounterVec(t, families, "eval_observation_total", []string{"resource"}, 2)
 assertCounterVec(t, families, "eval_judge_failure_total", []string{"reason"}, 1)
 assertHistogramVecSum(t, families, "eval_judge_score", 0.9)
 assertHistogramSum(t, families, "eval_judge_latency_seconds", 1.5)
 assertCounterSum(t, families, "eval_judge_cost_total", 0.012)
 assertGaugeVec(t, families, "eval_sample_coverage", "agent", 0.5)
 assertGaugeVec(t, families, "eval_queue_backlog", "observation", 7)
}
```

> 注：`NewPrometheusMetrics` 构造名与现有 gather/assert helper 以 `prometheus_metrics_test.go` 实际为准；断言 helper 若不存在则仿照 `prometheus_test.go` 现有 family 遍历写最小版（见 Step 3 末尾提示）。若现有测试文件已提供等价 helper，直接复用，禁止重复定义。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/observability/ -run TestEvalObservationMetrics -v`
Expected: FAIL（接口方法不存在，编译错误）。

- [ ] **Step 3: 实现接口 + NoopMetrics + PrometheusMetrics**

`pkg/observability/provider.go` 的 `MetricsProvider` 接口追加（`IncEvaluationJob` 行后）：

```go
 IncEvalObservation(resource, verdict string)
 RecordEvalJudgeScore(resource, dimension string, score float64)
 SetEvalSampleCoverage(resource string, pct float64)
 RecordEvalJudgeLatency(seconds float64)
 RecordEvalJudgeCost(costUSD float64)
 IncEvalJudgeFailure(reason string)
 SetEvalQueueBacklog(queue string, count int64)
```

`provider.go` 的 `NoopMetrics` 追加同名空实现（与其他方法对齐格式）：

```go
func (NoopMetrics) IncEvalObservation(_, _ string)                            {}
func (NoopMetrics) RecordEvalJudgeScore(_, _ string, _ float64)               {}
func (NoopMetrics) SetEvalSampleCoverage(_ string, _ float64)                 {}
func (NoopMetrics) RecordEvalJudgeLatency(_ float64)                          {}
func (NoopMetrics) RecordEvalJudgeCost(_ float64)                             {}
func (NoopMetrics) IncEvalJudgeFailure(_ string)                              {}
func (NoopMetrics) SetEvalQueueBacklog(_ string, _ int64)                     {}
```

`pkg/observability/prometheus.go` 在 `IncEvaluationJob` 实现附近追加（读该文件确认字段命名风格，`PrometheusMetrics` 已有各 metrics 字段；新字段名如 `evalObservationTotal` 等按文件惯例）：

```go
func (m *PrometheusMetrics) IncEvalObservation(resource, verdict string) {
 m.evalObservationTotal.WithLabelValues(resource, "").Inc() // stratum 标签 P1b 接入 tenant-tier
}

func (m *PrometheusMetrics) RecordEvalJudgeScore(resource, dimension string, score float64) {
 m.evalJudgeScore.WithLabelValues(resource, dimension).Observe(score)
}

func (m *PrometheusMetrics) SetEvalSampleCoverage(resource string, pct float64) {
 m.evalSampleCoverage.WithLabelValues(resource).Set(pct)
}

func (m *PrometheusMetrics) RecordEvalJudgeLatency(seconds float64) {
 m.evalJudgeLatency.Observe(seconds)
}

func (m *PrometheusMetrics) RecordEvalJudgeCost(costUSD float64) {
 m.evalJudgeCostTotal.Add(costUSD)
}

func (m *PrometheusMetrics) IncEvalJudgeFailure(reason string) {
 m.evalJudgeFailureTotal.WithLabelValues(reason).Inc()
}

func (m *PrometheusMetrics) SetEvalQueueBacklog(queue string, count int64) {
 m.evalQueueBacklog.WithLabelValues(queue).Set(float64(count))
}
```

并在 `PrometheusMetrics` struct 内追加字段、`NewPrometheusMetrics` 构造里注册这些 vec/histogram/counter。字段注册块（按 prometheus 库惯例，放 struct 字段区）：

```go
 evalObservationTotal  *prometheus.CounterVec
 evalJudgeScore        *prometheus.HistogramVec
 evalSampleCoverage    *prometheus.GaugeVec
 evalJudgeLatency      prometheus.Histogram
 evalJudgeCostTotal    prometheus.Counter
 evalJudgeFailureTotal *prometheus.CounterVec
 evalQueueBacklog      *prometheus.GaugeVec
```

构造注册（`NewPrometheusMetrics` 内的 `CounterOpts`/`HistogramOpts`，命名与 §11 对齐，`Namespace`/`Subsystem` 沿用文件现有前缀约定）：

```go
  m.evalObservationTotal = promauto.With(m.reg).NewCounterVec(prometheus.CounterOpts{
   Name: "eval_observation_total", Help: "运行态观测落库计数（§11.1）",
  }, []string{"resource", "stratum"})
  m.evalJudgeScore = promauto.With(m.reg).NewHistogramVec(prometheus.HistogramOpts{
   Name: "eval_judge_score", Help: "judge 单维度得分（§11.1）",
   Buckets: prometheus.LinearBuckets(0, 0.1, 11),
  }, []string{"resource", "dimension"})
  m.evalSampleCoverage = promauto.With(m.reg).NewGaugeVec(prometheus.GaugeOpts{
   Name: "eval_sample_coverage", Help: "主动采样覆盖率（§11.1）",
  }, []string{"resource"})
  m.evalJudgeLatency = promauto.With(m.reg).NewHistogram(prometheus.HistogramOpts{
   Name: "eval_judge_latency_seconds", Help: "judge 调用耗时（§11.2）",
   Buckets: prometheus.ExponentialBuckets(0.1, 2, 8),
  })
  m.evalJudgeCostTotal = promauto.With(m.reg).NewCounter(prometheus.CounterOpts{
   Name: "eval_judge_cost_total", Help: "judge 累计成本美元（§11.2）",
  })
  m.evalJudgeFailureTotal = promauto.With(m.reg).NewCounterVec(prometheus.CounterOpts{
   Name: "eval_judge_failure_total", Help: "judge 调用失败计数（§11.2）",
  }, []string{"reason"})
  m.evalQueueBacklog = promauto.With(m.reg).NewGaugeVec(prometheus.GaugeOpts{
   Name: "eval_queue_backlog", Help: "消息队列积压（§11.2）",
  }, []string{"queue"})
```

> 若 `prometheus_metrics_test.go` 没有现成的 `gather`/`assertCounterVec` 等 helper，最小实现版（追加在测试文件内）：

```go
func gather(t *testing.T, m *PrometheusMetrics) []*dto.MetricFamily {
 t.Helper()
 reg := m.reg
 // 若 reg 为 nil 测试无法 gather，改用注册表字段：以现有测试文件的 gather 用法为准。
 _ = reg
 t.Fatal("prometheus_metrics_test.go 已有 gather helper，禁止重复实现")
 return nil
}
```

（实施时以现有测试文件实际 helper 为准，上面仅作为兜底提示，若 helper 已存在则删除本段。）

- [ ] **Step 4: 同步全部 MetricsProvider 实现与测试 mock**

修改接口后编译全仓，找出断点并逐个补齐：

Run: `go build ./... 2>&1 | head -40`
Expected: 列出所有未实现新方法的类型（`NoopMetrics` 已补、`PrometheusMetrics` 已补；其余是测试文件中的 mock）。

对每个报错的测试文件 mock：若 mock 用 `type mockMetrics struct { observability.MetricsProvider }` 嵌入方式则无需改；若是显式全方法实现（含 `IncEvaluationJob`），在 mock 里补同名空方法（或改用嵌入接口）。逐文件处理，直到：

Run: `go test ./pkg/observability/ -short`
Expected: PASS。

- [ ] **Step 5: 运行观测指标测试确认通过**

Run: `go test ./pkg/observability/ -run TestEvalObservationMetrics -v`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add pkg/observability/provider.go pkg/observability/prometheus.go pkg/observability/prometheus_metrics_test.go
# 若有测试 mock 文件被同步修改，一并 git add
git commit -m "feat(observability): 评测观测指标扩展 MetricsProvider

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: agent 执行完成发布观测事件（emitter 挂载）

**Files:**

- Modify: `internal/agent/application/agent_service.go`（`AgentServiceDeps` 加字段 + `SetObservationEmitter`）
- Modify: `internal/agent/application/agent_execution.go`（`Execute`/`ExecuteStream` 完成路径 + `emitObservation`）
- Test: `internal/agent/application/observation_emit_test.go`

**Interfaces:**

- Consumes: `port.ObservationEmitter` + `port.ObservationEvent`（Task 2）。
- Produces: `AgentService.SetObservationEmitter(emitter port.ObservationEmitter)` —— Task 12 wiring 调用。

- [ ] **Step 1: 写失败测试（emitObservation 语义）**

先读 `internal/agent/application/agent_service.go` 的 `AgentServiceDeps` 字段列表与 `NewAgentService`，确认可注入 emitter 的路径后：

```go
// internal/agent/application/observation_emit_test.go
package application

import (
 "context"
 "errors"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "go.uber.org/zap"
)

type stubEmitter struct {
 called int
 last   port.ObservationEvent
 err    error
}

func (s *stubEmitter) Emit(_ context.Context, evt port.ObservationEvent) error {
 s.called++
 s.last = evt
 return s.err
}

func newTestServiceWithEmitter(e port.ObservationEmitter) *AgentService {
 return NewAgentService(AgentServiceDeps{Logger: zap.NewNop(), ObservationEmitter: e})
}

func TestEmitObservationPostsEvent(t *testing.T) {
 emitter := &stubEmitter{}
 s := newTestServiceWithEmitter(emitter)
 s.emitObservation(context.Background(), ExecMeta{TenantID: "t1", TraceID: "trace-1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"})

 if emitter.called != 1 {
  t.Fatalf("Emit called %d times, want 1", emitter.called)
 }
 evt := emitter.last
 if evt.TenantID != "t1" || evt.TraceID != "trace-1" || evt.ExecutionID != "exec-1" {
  t.Fatalf("event identity mismatch: %+v", evt)
 }
 if evt.AgentID != "agent-1" || evt.ResourceKind != "agent" || evt.ResourceID != "agent-1" {
  t.Fatalf("event resource mismatch: %+v", evt)
 }
 if evt.CompletedAt == "" {
  t.Fatal("completed_at must be set")
 }
}

func TestEmitObservationNilEmitterNoPanic(t *testing.T) {
 s := newTestServiceWithEmitter(nil)
 s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"}) // 不得 panic
}

func TestEmitObservationNilResultSkips(t *testing.T) {
 emitter := &stubEmitter{}
 s := newTestServiceWithEmitter(emitter)
 s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", nil)
 if emitter.called != 0 {
  t.Fatalf("Emit called %d times for nil result, want 0", emitter.called)
 }
}

func TestEmitObservationFailureDoesNotPropagate(t *testing.T) {
 emitter := &stubEmitter{err: errors.New("nats down")}
 s := newTestServiceWithEmitter(emitter)
 s.emitObservation(context.Background(), ExecMeta{TenantID: "t1"}, "agent-1", "exec-1", &AgentResult{Output: "ok"}) // 失败仅记日志
}
```

> 注：`ExecMeta`/`AgentResult` 的字段名以 `agent_execution.go` 实际定义为准（摘要显示 `ExecMeta{TenantID, TraceID, Stream, ExecutionID, ...}`；若 `AgentResult` 无 `Output` 字段，改用其实际字段或 `&AgentResult{}` 空值即可）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/application/ -run "TestEmitObservation" -v`
Expected: FAIL（`emitObservation` 方法不存在，编译错误）。

- [ ] **Step 3: 实现 deps 字段 + Setter + emitObservation + 挂载**

`internal/agent/application/agent_service.go` 的 `AgentServiceDeps` 追加（与其他 port 字段并列）：

```go
 // ObservationEmitter 发布观测引用事件（best-effort，nil 安全）。执行完成
 // 后调用，评估器不阻断执行铁律：失败仅记日志。
 ObservationEmitter port.ObservationEmitter
```

追加 Setter（`SetSkillRevisionResolver` 后）：

```go
// SetObservationEmitter 注入观测事件发布器（best-effort，可 nil）。
func (s *AgentService) SetObservationEmitter(emitter port.ObservationEmitter) {
 s.deps.ObservationEmitter = emitter
}
```

`internal/agent/application/agent_service.go` 新增私有方法（放 `NewAgentService` 之后，同包可访问；需确保该文件已 import `time`、`port`、`zap`——若缺则在导入区按 stdlib/third-party/internal 分组补齐）：

```go
// emitObservation 发布观测引用事件。best-effort：emitter 为 nil、result 为 nil
// 或发布失败都不阻断执行，失败只记 warn 日志（评估器不阻断执行铁律）。
// 事件只带 trace 标识与资源锚点，证据由评测服务从 Opik 拉取。
func (s *AgentService) emitObservation(ctx context.Context, meta ExecMeta, agentID, executionID string, result *AgentResult) {
 if s.deps.ObservationEmitter == nil || result == nil {
  return
 }
 evt := port.ObservationEvent{
  TenantID:     meta.TenantID,
  TraceID:      meta.TraceID,
  ExecutionID:  executionID,
  AgentID:      agentID,
  ResourceKind: "agent",
  ResourceID:   agentID,
  CompletedAt:  time.Now().UTC().Format(time.RFC3339Nano),
 }
 if err := s.deps.ObservationEmitter.Emit(context.WithoutCancel(ctx), evt); err != nil {
  s.deps.Logger.Warn("agent observation emit failed",
   zap.Error(err), zap.String("trace_id", meta.TraceID))
 }
}
```

在 `Execute`（`agent_execution.go` :282 附近）的 `if err == nil && result != nil { ... }` 块内、两条 `bufferMemoryTurn` 之后（`enqueueTrajectoryReflection` 之前）插入——观测只针对成功且产出结果的执行：

```go
  s.emitObservation(ctx, meta, agentID, executionID, result)
```

在 `ExecuteStream` 的 run 闭包（`agent_execution.go` :341 附近）的 `if runErr == nil && res != nil { ... }` 块内、两条 `bufferMemoryTurn` 之后（`enqueueTrajectoryReflection` 之前）插入：

```go
   s.emitObservation(ctx, meta, agentID, executionID, res)
```

> 注：`agentID` 与 `executionID` 均在 Execute/run 闭包作用域内（`Execute(agentID string, ...)` 形参 + `executionID := executionIDOrNew(meta.ExecutionID)`），已确认可引用。
>
> 注：`ExecuteStream` 的 run 闭包作用域内 `agentID` 来自外层参数、`executionID` 来自闭包捕获，均已就绪。若行号偏移，以 `enqueueTrajectoryReflection` 调用处为锚点定位。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/application/ -run "TestEmitObservation" -v`
Expected: PASS（4 个用例）。

- [ ] **Step 5: 全量回归（agent 侧改动大，跑完整包）**

Run: `go test ./internal/agent/application/ -short -timeout 60s`
Expected: PASS（存量测试不回归）。

- [ ] **Step 6: 提交**

```bash
git add internal/agent/application/agent_service.go internal/agent/application/agent_execution.go \
        internal/agent/application/observation_emit_test.go
git commit -m "feat(agent): 执行完成发布观测引用事件(best-effort)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: NATS 观测发布 adapter（wiring）

**Files:**

- Create: `api/wiring/observation_emitter.go`
- Test: `api/wiring/observation_emitter_test.go`

**Interfaces:**

- Consumes: `agentport.ObservationEmitter` + `agentport.ObservationEvent`（Task 2）、`constants.ObservationSubjectPrefix`（Task 2）、`*nats.Conn`（wiring 已有 `c.Storage.NATS`）。
- Produces: `observationEmitterAdapter`，实现 `agentport.ObservationEmitter` —— Task 12 wiring 注入 agent service。

- [ ] **Step 1: 写失败测试（mock JetStream）**

```go
// api/wiring/observation_emitter_test.go
package wiring

import (
 "context"
 "encoding/json"
 "errors"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"
)

// stubJetStream 嵌入 jetstream.JetStream 接口（nil 满足其余方法），只覆盖 Publish。
type stubJetStream struct {
 jetstream.JetStream
 publishedSubject string
 publishedData    []byte
 err              error
}

func (s *stubJetStream) Publish(ctx context.Context, subj string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
 s.publishedSubject = subj
 s.publishedData = append([]byte(nil), data...)
 if s.err != nil {
  return nil, s.err
 }
 return &jetstream.PubAck{}, nil
}

func TestObservationEmitterAdapterPublishes(t *testing.T) {
 js := &stubJetStream{}
 adapter := &observationEmitterAdapter{js: js, logger: zap.NewNop()}
 evt := port.ObservationEvent{
  TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
  AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
  CompletedAt: "2026-08-28T12:00:00Z",
 }
 if err := adapter.Emit(context.Background(), evt); err != nil {
  t.Fatalf("Emit: %v", err)
 }
 wantSubject := "evaluation.observe.t1"
 if js.publishedSubject != wantSubject {
  t.Fatalf("subject = %q, want %q", js.publishedSubject, wantSubject)
 }
 var decoded port.ObservationEvent
 if err := json.Unmarshal(js.publishedData, &decoded); err != nil {
  t.Fatalf("unmarshal published payload: %v", err)
 }
 if decoded.TraceID != "trace-1" || decoded.ResourceKind != "agent" {
  t.Fatalf("published payload mismatch: %+v", decoded)
 }
}

func TestObservationEmitterAdapterPropagatesError(t *testing.T) {
 js := &stubJetStream{err: errors.New("nats down")}
 adapter := &observationEmitterAdapter{js: js, logger: zap.NewNop()}
 err := adapter.Emit(context.Background(), port.ObservationEvent{TenantID: "t1"})
 if err == nil {
  t.Fatal("expected error, got nil")
 }
}
```

> 注：`a.js.Publish(pctx, subject, data)` 为 `jetstream.JetStream.Publish(ctx, subject, data, ...PublishOpt) (*PubAck, error)`（nats.go/jetstream 包），与 Task 10 的 `dlqPublisher` 接口签名一致。`stubJetStream` 在测试内实现 `Publish` 方法即可。`ObservationPublishTimeout` 定义见 Task 2 常量块。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/wiring/ -run "TestObservationEmitterAdapter" -v`
Expected: FAIL（`observationEmitterAdapter` 不存在，编译错误）。

- [ ] **Step 3: 实现 adapter**

```go
// api/wiring/observation_emitter.go
package wiring

import (
 "context"
 "encoding/json"
 "fmt"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"
)

// observationEmitterAdapter 实现 agentport.ObservationEmitter：把执行完成
// 引用事件序列化后发布到 evaluation.observe.{tenant} JetStream subject。
// best-effort 语义由调用方（agent application）保证，这里只负责可靠编解码与
// 发布失败透传（发布有独立超时预算，不占用 agent 请求时间预算）。
type observationEmitterAdapter struct {
 js     jetstream.JetStream
 logger *zap.Logger
}

func (a *observationEmitterAdapter) Emit(ctx context.Context, evt port.ObservationEvent) error {
 data, err := json.Marshal(evt)
 if err != nil {
  return fmt.Errorf("marshal observation event: %w", err)
 }
 pctx, cancel := context.WithTimeout(ctx, constants.ObservationPublishTimeout)
 defer cancel()
 subject := constants.ObservationSubjectPrefix + "." + evt.TenantID
 if _, err := a.js.Publish(pctx, subject, data); err != nil {
  return fmt.Errorf("publish observation event: %w", err)
 }
 return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./api/wiring/ -run "TestObservationEmitterAdapter" -v`
Expected: PASS（2 个用例）。

- [ ] **Step 5: 提交**

```bash
git add api/wiring/observation_emitter.go api/wiring/observation_emitter_test.go
git commit -m "feat(evaluation): NATS 观测引用事件发布 adapter

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 采样策略

**Files:**

- Create: `internal/evaluation/application/observation_sampling.go`
- Test: `internal/evaluation/application/observation_sampling_test.go`

**Interfaces:**

- Consumes: 无（纯函数）。
- Produces: `sampleDecision(sampleRate float64, resourceKind, traceID string) bool` —— Task 9 消费。确定性哈希采样：同一 (resourceKind, traceID) 幂等，不依赖随机数，便于测试与可复现。

- [ ] **Step 1: 写失败测试**

```go
// internal/evaluation/application/observation_sampling_test.go
package application

import "testing"

func TestSampleDecisionBoundaries(t *testing.T) {
 if sampleDecision(0, "agent", "trace-1") {
  t.Fatal("sampleRate 0 must never sample")
 }
 if !sampleDecision(1, "agent", "trace-1") {
  t.Fatal("sampleRate 1 must always sample")
 }
 if sampleDecision(0.0, "agent", "trace-1") {
  t.Fatal("zero rate must reject")
 }
}

func TestSampleDecisionDeterministic(t *testing.T) {
 if !sampleDecision(0.5, "agent", "trace-abc") {
  t.Fatal("expected sample for trace-abc")
 }
 if sampleDecision(0.5, "agent", "trace-abc") != sampleDecision(0.5, "agent", "trace-abc") {
  t.Fatal("same trace must be idempotent")
 }
}

func TestSampleDecisionRateMonotonic(t *testing.T) {
 // 采样率提高时，被采样的 trace 集合单调不缩（同 trace 从拒到采，绝不反转）。
 lower := map[string]bool{}
 for i := 0; i < 500; i++ {
  tid := string(rune('a'+i%26)) + string(rune('0'+i/26))
  lower[tid] = sampleDecision(0.2, "agent", tid)
 }
 for tid, sampled := range lower {
  if sampled && !sampleDecision(0.9, "agent", tid) {
   t.Fatalf("trace %s sampled at 0.2 but rejected at 0.9", tid)
  }
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/application/ -run "TestSampleDecision" -v`
Expected: FAIL（`sampleDecision` 未定义，编译错误）。

- [ ] **Step 3: 最小实现**

```go
// internal/evaluation/application/observation_sampling.go
package application

import (
 "crypto/sha256"
 "encoding/binary"
 "math"
)

// sampleDecision 决定一条 trace 是否进入 judge 采样。用
// sha256(resourceKind + "/" + traceID) 的确定性哈希映射到 [0,1) 桶，
// 与采样率比较：同一 (resourceKind, traceID) 幂等、采样率单调（采样集不缩）。
// sampleRate ∈ [0,1]；0 永不采样、1 全采样。
func sampleDecision(sampleRate float64, resourceKind, traceID string) bool {
 if sampleRate <= 0 {
  return false
 }
 if sampleRate >= 1 {
  return true
 }
 h := sha256.Sum256([]byte(resourceKind + "/" + traceID))
 n := binary.BigEndian.Uint64(h[:8])
 return float64(n)/float64(math.MaxUint64) < sampleRate
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/evaluation/application/ -run "TestSampleDecision" -v`
Expected: PASS（3 个用例）。

- [ ] **Step 5: 提交**

```bash
git add internal/evaluation/application/observation_sampling.go internal/evaluation/application/observation_sampling_test.go
git commit -m "feat(evaluation): 观测采样确定性哈希策略

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: ObservationRepository（tenant DDL + pgx 实现）

**Files:**

- Create: `internal/evaluation/domain/port/observation_repo.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Create: `internal/evaluation/infrastructure/persistence/observation_repository.go`
- Test: `internal/evaluation/infrastructure/persistence/observation_repository_test.go`

**Interfaces:**

- Consumes: `domain.EvalObservation`（Task 1）、`poolIface`（`internal/evaluation/infrastructure/persistence/pool_iface.go`，`execTenantTx = pgstore.ExecTenantWith(ctx, pool, tenantID, fn)`）。
- Produces: `evalport.ObservationRepository`：

  ```go
  type ObservationRepository interface {
      Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
      Get(ctx context.Context, tenantID, observationID string) (*domain.EvalObservation, error)
      QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string, from, to *time.Time, limit, offset int) ([]domain.EvalObservation, error)
  }
  ```

  Task 9/11 消费。

- [ ] **Step 1: 写失败测试（pgxmock）**

先读 `internal/evaluation/infrastructure/persistence/run_repository_test.go` 的 pgxmock 用法（`NewPgxMock`/`ExpectQuery`/`ExpectBegin` 等）与其 `poolIface` mock 基建，沿用同样式：

```go
// internal/evaluation/infrastructure/persistence/observation_repository_test.go
package persistence

import (
 "context"
 "regexp"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/pashagolub/pgxmock/v4"
)

func TestObservationRepositorySave(t *testing.T) {
 mock, pool := newPgxMockPool(t) // 沿用 run_repository_test.go 的 pool mock 基建
 repo := NewPgObservationRepository(pool)

 obs := &domain.EvalObservation{
  ID: "obs-1", TraceID: "trace-1",
  Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"},
  Signals: domain.ObservationSignals{Judge: []domain.JudgeSignal{
   {Dimension: "faithfulness", Score: 0.9, Confidence: 0.85},
  }},
  CostPerf:  domain.CostPerf{LatencyMS: 1200, Tokens: 3200, CostUSD: 0.012},
  Verdict:   domain.VerdictPass,
  CreatedAt: time.Now().UTC(),
 }

 mock.ExpectBegin()
 mock.ExpectExec(`INSERT INTO eval_observations`).
  WithArgs("obs-1", "trace-1", "agent", "agent-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "", "pass", obs.CreatedAt).
  WillReturnResult(pgxmock.NewResult("INSERT", 1))
 mock.ExpectCommit()

 if err := repo.Save(context.Background(), "t1", obs); err != nil {
  t.Fatalf("Save: %v", err)
 }
 if err := mock.ExpectationsWereMet(); err != nil {
  t.Fatalf("expectations not met: %v", err)
 }
}

func TestObservationRepositoryGet(t *testing.T) {
 mock, pool := newPgxMockPool(t)
 repo := NewPgObservationRepository(pool)

 rows := pgxmock.NewRows([]string{"id", "trace_id", "resource_kind", "resource_id",
  "param_version", "signals", "cost_perf", "stratum", "verdict", "created_at"}).
  AddRow("obs-1", "trace-1", "agent", "agent-1",
   `{"platform":{"group_key":"","version_seq":0},"resource":{"ref":"r1","version":"v3"},"source":"resource"}`,
   `{"rule":null,"judge":[{"dimension":"faithfulness","score":0.9,"confidence":0.85}],"behavior":{"retry":false,"escalation":false,"abandonment":false}}`,
   `{"latency_ms":1200,"tokens":3200,"cost_usd":0.012}`,
   "", "pass", time.Now().UTC())

 mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at FROM eval_observations WHERE id = $1`)).
  WithArgs("obs-1").
  WillReturnRows(rows)

 got, err := repo.Get(context.Background(), "t1", "obs-1")
 if err != nil {
  t.Fatalf("Get: %v", err)
 }
 if got.TraceID != "trace-1" || got.Signals.Judge[0].Score != 0.9 {
  t.Fatalf("Get mismatch: %+v", got)
 }
}

func TestObservationRepositoryQueryByResource(t *testing.T) {
 mock, pool := newPgxMockPool(t)
 repo := NewPgObservationRepository(pool)

 rows := pgxmock.NewRows([]string{"id", "trace_id", "resource_kind", "resource_id",
  "param_version", "signals", "cost_perf", "stratum", "verdict", "created_at"}).
  AddRow("obs-1", "trace-1", "agent", "agent-1", `{}`, `{}`, `{}`, "", "pass", time.Now().UTC())

 mock.ExpectQuery(`FROM eval_observations WHERE resource_kind = \$1 AND resource_id = \$2`).
  WithArgs("agent", "agent-1").
  WillReturnRows(rows)

 list, err := repo.QueryByResource(context.Background(), "t1", "agent", "agent-1", nil, nil, 20, 0)
 if err != nil {
  t.Fatalf("QueryByResource: %v", err)
 }
 if len(list) != 1 || list[0].TraceID != "trace-1" {
  t.Fatalf("QueryByResource mismatch: %+v", list)
 }
}
```

> 注：`newPgxMockPool` 若 run_repository_test.go 无同名基建，则以该文件实际 mock 池构造方式为准（摘要确认 run_repository 测试用 pgxmock，且经 `execTenantTx` 的 SQL 会被 mock 捕获）。`pool_iface.go` 里**只有 `execTenantTx`**（不存在 `execTenant`/`execTenantQuery`）——Save/Get/Query 全部经 `execTenantTx(ctx, r.pool, tenantID, fn)`，读操作在 tx 内 `tx.QueryRow`/`tx.Query`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/infrastructure/persistence/ -run "TestObservationRepository" -v`
Expected: FAIL（`observation_repository.go` 不存在，编译错误）。

- [ ] **Step 3: 实现 port + DDL + repository**

`internal/evaluation/domain/port/observation_repo.go`：

```go
package port

import (
 "context"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// ObservationRepository 持久化运行态观测明细（tenant-scoped，eval_observations）。
type ObservationRepository interface {
 // Save 落库一条观测；obs.ID 由调用方生成，Validate 通过才写入。
 Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
 // Get 按 id 取单条观测。
 Get(ctx context.Context, tenantID, observationID string) (*domain.EvalObservation, error)
 // QueryByResource 按资源 + 可选时间窗分页查询，按创建时间倒序。
 // from/to 为 nil 时不加时间过滤。
 QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
  from, to *time.Time, limit, offset int) ([]domain.EvalObservation, error)
}
```

`pkg/storage/postgres/tenant_schema.sql` 追加（仿 `eval_runs`/`eval_case_results` 表块格式，放在评测相关表附近）：

```sql
-- 运行态观测明细（规格 §4.3 EvalObservation）。param_version/signals/cost_perf
-- 为 JSONB 结构化字段，由 Go json.Marshal 后写入。
CREATE TABLE IF NOT EXISTS eval_observations (
    id            TEXT PRIMARY KEY,
    trace_id      TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    param_version JSONB NOT NULL DEFAULT '{}'::jsonb,
    signals       JSONB NOT NULL DEFAULT '{}'::jsonb,
    cost_perf     JSONB NOT NULL DEFAULT '{}'::jsonb,
    stratum       TEXT NOT NULL DEFAULT '',
    verdict       TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_eval_observations_resource_time
    ON eval_observations (resource_kind, resource_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eval_observations_trace
    ON eval_observations (trace_id);
```

`internal/evaluation/infrastructure/persistence/observation_repository.go`：

```go
package persistence

import (
 "context"
 "encoding/json"
 "fmt"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
 "github.com/jackc/pgx/v5"
)

const observationColumns = "id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at"

// PgObservationRepository 实现 port.ObservationRepository（tenant-scoped）。
type PgObservationRepository struct {
 pool poolIface
}

func NewPgObservationRepository(pool poolIface) *PgObservationRepository {
 return &PgObservationRepository{pool: pool}
}

// execTenantTx 是 pool_iface.go 里唯一的事务执行器，签名：
// func execTenantTx(ctx context.Context, pool poolIface, tenantID string,
//
// fn func(context.Context, pgx.Tx) error) error
//
// 所有方法先 postgres.WithTenant 设租户上下文再进事务，读写都在事务内完成
// （与 PgRunRepository 完全一致；该包不存在 execTenant/execTenantQuery helper）。

func (r *PgObservationRepository) Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error {
 if err := obs.Validate(); err != nil {
  return err
 }
 paramJSON, err := json.Marshal(obs.Param)
 if err != nil {
  return fmt.Errorf("marshal param_version: %w", err)
 }
 signalsJSON, err := json.Marshal(obs.Signals)
 if err != nil {
  return fmt.Errorf("marshal signals: %w", err)
 }
 costJSON, err := json.Marshal(obs.CostPerf)
 if err != nil {
  return fmt.Errorf("marshal cost_perf: %w", err)
 }
 ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
 err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  _, execErr := tx.Exec(ctx,
   `INSERT INTO eval_observations (id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
   obs.ID, obs.TraceID, obs.Resource.Kind, obs.Resource.ResourceID,
   string(paramJSON), string(signalsJSON), string(costJSON),
   obs.Stratum, string(obs.Verdict), obs.CreatedAt,
  )
  if execErr != nil {
   return fmt.Errorf("insert eval observation: %w", execErr)
  }
  return nil
 })
 if err != nil {
  return fmt.Errorf("save eval observation: %w", err)
 }
 return nil
}

func (r *PgObservationRepository) Get(ctx context.Context, tenantID, observationID string) (*domain.EvalObservation, error) {
 var (
  obs        domain.EvalObservation
  paramJSON, signalsJSON, costJSON string
 )
 ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
 err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  return tx.QueryRow(ctx,
   `SELECT `+observationColumns+` FROM eval_observations WHERE id = $1`, observationID,
  ).Scan(&obs.ID, &obs.TraceID, &obs.Resource.Kind, &obs.Resource.ResourceID,
   &paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &obs.Verdict, &obs.CreatedAt)
 })
 if err != nil {
  if err == pgx.ErrNoRows {
   return nil, fmt.Errorf("get eval observation %s: %w", observationID, err)
  }
  return nil, fmt.Errorf("get eval observation %s: %w", observationID, err)
 }
 if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
  return nil, err
 }
 return &obs, nil
}

func (r *PgObservationRepository) QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
 from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
 query := `SELECT ` + observationColumns + ` FROM eval_observations WHERE resource_kind = $1 AND resource_id = $2`
 args := []any{resourceKind, resourceID}
 if from != nil {
  args = append(args, *from)
  query += fmt.Sprintf(" AND created_at >= $%d", len(args))
 }
 if to != nil {
  args = append(args, *to)
  query += fmt.Sprintf(" AND created_at <= $%d", len(args))
 }
 args = append(args, limit, offset)
 query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

 var out []domain.EvalObservation
 ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
 err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, query, args...)
  if err != nil {
   return err
  }
  defer rows.Close()
  for rows.Next() {
   var (
    obs        domain.EvalObservation
    paramJSON, signalsJSON, costJSON string
   )
   if err := rows.Scan(&obs.ID, &obs.TraceID, &obs.Resource.Kind, &obs.Resource.ResourceID,
    &paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &obs.Verdict, &obs.CreatedAt); err != nil {
    return err
   }
   if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
    return err
   }
   out = append(out, obs)
  }
  return rows.Err()
 })
 if err != nil {
  return nil, fmt.Errorf("query eval observations: %w", err)
 }
 return out, nil
}

func unmarshalObservationJSON(obs *domain.EvalObservation, paramJSON, signalsJSON, costJSON string) error {
 if err := json.Unmarshal([]byte(paramJSON), &obs.Param); err != nil {
  return fmt.Errorf("decode param_version: %w", err)
 }
 if err := json.Unmarshal([]byte(signalsJSON), &obs.Signals); err != nil {
  return fmt.Errorf("decode signals: %w", err)
 }
 if err := json.Unmarshal([]byte(costJSON), &obs.CostPerf); err != nil {
  return fmt.Errorf("decode cost_perf: %w", err)
 }
 return nil
}
```

> 注：该文件 import 需含 `postgres`（`github.com/byteBuilderX/stratum/pkg/storage/postgres`），与 `run_repository.go` 一致。JSONB 列经 `json.Marshal` 后以 `string(b)` 传入（pgx v5 规则），已实现。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/evaluation/infrastructure/persistence/ -run "TestObservationRepository" -v`
Expected: PASS（3 个用例）。

- [ ] **Step 5: 提交**

```bash
git add internal/evaluation/domain/port/observation_repo.go \
        pkg/storage/postgres/tenant_schema.sql \
        internal/evaluation/infrastructure/persistence/observation_repository.go \
        internal/evaluation/infrastructure/persistence/observation_repository_test.go
git commit -m "feat(evaluation): EvalObservation 落库 repository(tenant-scoped)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: 落地服务 ObservationService

**Files:**

- Create: `internal/evaluation/application/observation_service.go`
- Test: `internal/evaluation/application/observation_service_test.go`

**Interfaces:**

- Consumes: `domain.ObservationReferenceEvent`（Task 2）、`domain.EvalObservation`（Task 1）、`sampleDecision`（Task 7）、`evalport.ObservationRepository`（Task 8）、`evalport.TraceEvidenceReader`（现状）、`evalport.LLMJudge`（现状）、`observability.MetricsProvider`（Task 4 新方法）。观测开关/采样率经构造注入的闭包读取（wiring 提供读 PlatformValues 的 closure，application 不 import parameters）。
- Produces: `ObservationService` + `NewObservationService(...)`，方法 `Process(ctx, tenantID string, evt domain.ObservationReferenceEvent) error`、`ListObservations(ctx, tenantID string, q ObservationQuery) ([]domain.EvalObservation, error)`、`GetObservation(ctx, tenantID, id string) (*domain.EvalObservation, error)`。Task 10/11/12 消费。

judge rubric：`ObservationService` 对运行态观测使用内置 rubric（faithfulness/relevance/completeness 三维度，对齐规格 §3.1）；每个维度独立调用 `LLMJudge.Judge`，汇总为 `JudgeSignal`。`LLMJudge` 现状 `Judge(ctx, JudgeRequest) (AssertionResult, error)` 返回单条结果——为支撑三维度，P1a 用三次独立 Judge 调用（每次一个维度），每次返回单维度得分。

- [ ] **Step 1: 写失败测试（Process 主路径 + 降级路径）**

```go
// internal/evaluation/application/observation_service_test.go
package application

import (
 "context"
 "errors"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "go.uber.org/zap"
)

type stubEvidenceReader struct {
 trace port.ObservedTrace
 err   error
}

func (s *stubEvidenceReader) Resolve(ctx context.Context, tenantID, traceID string) (port.ObservedTrace, error) {
 if s.err != nil {
  return port.ObservedTrace{}, s.err
 }
 return s.trace, nil
}

func (s *stubEvidenceReader) ResolveBatch(ctx context.Context, tenantID string, traceIDs []string) (map[string]port.ObservedTrace, error) {
 return nil, errors.New("not used")
}

type stubJudge struct {
 result  domain.AssertionResult
 err     error
 enabled bool
 calls   int
}

func (j *stubJudge) Enabled(ctx context.Context) bool { return j.enabled }
func (j *stubJudge) Judge(ctx context.Context, req port.JudgeRequest) (domain.AssertionResult, error) {
 j.calls++
 if j.err != nil {
  return domain.AssertionResult{}, j.err
 }
 return j.result, nil
}

type stubObservationRepo struct {
 saved []domain.EvalObservation
 err   error
}

func (s *stubObservationRepo) Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error {
 if s.err != nil {
  return s.err
 }
 s.saved = append(s.saved, *obs)
 return nil
}
func (s *stubObservationRepo) Get(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error) {
 return nil, errors.New("not used")
}
func (s *stubObservationRepo) QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
 from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
 return nil, errors.New("not used")
}

func newTestObservationService(repo *stubObservationRepo, reader *stubEvidenceReader, judge *stubJudge) *ObservationService {
 return NewObservationService(ObservationServiceDeps{
  Enabled:    func(context.Context) bool { return true },
  SampleRate: func(context.Context) float64 { return 1.0 },
  Evidence:   reader, Judge: judge, Repo: repo,
  Metrics: observability.NoopMetrics{}, Logger: zap.NewNop(),
 })
}

func TestObservationServiceProcessJudgesAndSaves(t *testing.T) {
 repo := &stubObservationRepo{}
 reader := &stubEvidenceReader{trace: port.ObservedTrace{
  TraceID: "trace-1", Input: "用户问题", Output: "助手回答",
  CostUSD: 0.01, LatencyMs: 800, Success: true,
 }}
 judge := &stubJudge{enabled: true, result: domain.AssertionResult{Passed: true}}
 svc := newTestObservationService(repo, reader, judge)

 evt := domain.ObservationReferenceEvent{
  TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
  AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
 }
 if err := svc.Process(context.Background(), evt); err != nil {
  t.Fatalf("Process: %v", err)
 }
 if len(repo.saved) != 1 {
  t.Fatalf("saved %d observations, want 1", len(repo.saved))
 }
 saved := repo.saved[0]
 if saved.TraceID != "trace-1" || saved.Resource.Kind != "agent" || saved.Resource.ResourceID != "agent-1" {
  t.Fatalf("saved identity mismatch: %+v", saved)
 }
 if len(saved.Signals.Judge) != 3 {
  t.Fatalf("judge signals = %d, want 3 dimensions", len(saved.Signals.Judge))
 }
 if saved.Verdict != domain.VerdictPass {
  t.Fatalf("verdict = %s, want pass", saved.Verdict)
 }
 if judge.calls != 3 {
  t.Fatalf("judge calls = %d, want 3", judge.calls)
 }
}

func TestObservationServiceProcessDisabledSkips(t *testing.T) {
 repo := &stubObservationRepo{}
 svc := NewObservationService(ObservationServiceDeps{
  Enabled: func(context.Context) bool { return false },
  Evidence: &stubEvidenceReader{}, Judge: &stubJudge{},
  Repo: repo, Metrics: observability.NoopMetrics{}, Logger: zap.NewNop(),
 })
 if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1"}); err != nil {
  t.Fatalf("Process disabled: %v", err)
 }
 if len(repo.saved) != 0 {
  t.Fatalf("disabled must not save, got %d", len(repo.saved))
 }
}

func TestObservationServiceProcessEvidenceErrorPropagates(t *testing.T) {
 repo := &stubObservationRepo{}
 reader := &stubEvidenceReader{err: errors.New("opik down")}
 svc := newTestObservationService(repo, reader, &stubJudge{enabled: true})
 err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1"})
 if err == nil {
  t.Fatal("evidence error must propagate for NATS redelivery")
 }
 if len(repo.saved) != 0 {
  t.Fatalf("must not save on evidence error, got %d", len(repo.saved))
 }
}

func TestObservationServiceProcessJudgeFailureDegrades(t *testing.T) {
 repo := &stubObservationRepo{}
 reader := &stubEvidenceReader{trace: port.ObservedTrace{TraceID: "trace-1", Input: "q", Output: "a"}}
 judge := &stubJudge{enabled: true, err: errors.New("judge down")}
 svc := newTestObservationService(repo, reader, judge)
 if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1"}); err != nil {
  t.Fatalf("Process with judge failure should degrade without error: %v", err)
 }
 // judge 故障 §14 采样降级跳过：不落库、不重投、不伪造零信号 pass 观察。
 if len(repo.saved) != 0 {
  t.Fatalf("judge failure must skip observation, got %d saved", len(repo.saved))
 }
}
```

> 注：`port.ObservedTrace`、`port.AssertionResult`、`port.JudgeRequest`、`port.TraceEvidenceReader` 的确切字段/方法以 `internal/evaluation/domain/port/evaluation.go` 为准（摘要给出 `ObservedTrace{TraceID, UserID, CostUSD, LatencyMs, Success, SecurityViolation, Assignments}` + 本计划 Task 3 加的 `Input/Output`；`AssertionResult` 含 `Passed` 等字段）。`ObservationServiceDeps`/`NewObservationService` 为本任务产出，测试先行引用。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/application/ -run "TestObservationService" -v`
Expected: FAIL（`observation_service.go` 不存在，编译错误）。

- [ ] **Step 3: 实现落地服务**

```go
// internal/evaluation/application/observation_service.go
package application

import (
 "context"
 "fmt"
 "strings"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/google/uuid"
 "go.uber.org/zap"
)

// 运行态观测 judge rubric 维度（规格 §3.1）：三个语义质量维度各一次 Judge 调用。
var observationJudgeDimensions = []string{"faithfulness", "relevance", "completeness"}

// ObservationServiceDeps 是落地服务的依赖（全部必填，缺失字段由 wiring 保证；
// 任何依赖 nil 时 Process 按 fail closed 处理）。
type ObservationServiceDeps struct {
 Enabled    func(ctx context.Context) bool   // 平台参数 evaluation.observe.enabled
 SampleRate func(ctx context.Context) float64 // 平台参数 evaluation.observe.sample_rate
 Evidence   port.TraceEvidenceReader
 Judge      port.LLMJudge
 Repo       port.ObservationRepository
 Metrics    observability.MetricsProvider
 Logger     *zap.Logger
}

type ObservationService struct {
 deps ObservationServiceDeps
}

func NewObservationService(deps ObservationServiceDeps) *ObservationService {
 if deps.Logger == nil {
  deps.Logger = zap.NewNop()
 }
 return &ObservationService{deps: deps}
}

// Process 处理一条观测引用事件：开启 → 采样 → 拉证据 → judge 多维打分 → 落库。
// 返回 error 表示需要 NATS 重投（仅证据查询失败）；judge 故障按 §14 采样降级
// 跳过（不落库、不重投、指标计数），绝不伪成功。
func (s *ObservationService) Process(ctx context.Context, evt domain.ObservationReferenceEvent) error {
 if !s.deps.Enabled(ctx) {
  return nil
 }
 if !sampleDecision(s.deps.SampleRate(ctx), evt.ResourceKind, evt.TraceID) {
  return nil
 }
 trace, err := s.deps.Evidence.Resolve(ctx, evt.TenantID, evt.TraceID)
 if err != nil {
  s.deps.Metrics.IncEvalJudgeFailure("evidence_resolve")
  return fmt.Errorf("observation resolve evidence: %w", err)
 }
 obs := s.buildObservation(evt, trace)
 if err := s.applyJudge(ctx, trace, &obs); err != nil {
  // judge 不可用：§14 采样降级跳过——不落零信号的伪 pass 观察、不重投
  //（避免 judge 持续不可用时的重投空转），仅指标计数 + warn 日志。
  s.deps.Logger.Warn("observation judge degraded, skip", zap.Error(err),
   zap.String("trace_id", evt.TraceID))
  s.deps.Metrics.IncEvalJudgeFailure("judge_unavailable")
  return nil
 }
 if err := obs.Validate(); err != nil {
  s.deps.Metrics.IncEvalJudgeFailure("invalid_observation")
  return fmt.Errorf("observation validate: %w", err)
 }
 if err := s.deps.Repo.Save(ctx, evt.TenantID, &obs); err != nil {
  return fmt.Errorf("observation save: %w", err)
 }
 s.deps.Metrics.IncEvalObservation(evt.ResourceKind, string(obs.Verdict))
 return nil
}

// buildObservation 组装 EvalObservation（不含 judge 信号，由 applyJudge 填充）。
func (s *ObservationService) buildObservation(evt domain.ObservationReferenceEvent, trace port.ObservedTrace) domain.EvalObservation {
 resourceVersion := domain.ResourceParamVersion{Ref: "", Version: ""}
 source := domain.ParamSourceUnknown
 for _, a := range trace.Assignments {
  if a.RevisionID != "" {
   resourceVersion = domain.ResourceParamVersion{Ref: a.RevisionID, Version: a.Variant}
   source = domain.ParamSourceResource
  }
 }
 // TODO(P2)：平台层版本锚点随配置版本机制绑定后回填；当前未知。
 return domain.EvalObservation{
  ID:       uuid.NewString(),
  TraceID:  evt.TraceID,
  Resource: evt.ResourceRef(),
  Param: domain.ParamVersion{
   Platform: domain.PlatformParamVersion{VersionSeq: 0}, // P2 绑定
   Resource: resourceVersion,
   Source:   source,
  },
  CostPerf: domain.CostPerf{
   LatencyMS: trace.LatencyMs,
   Tokens:    trace.TotalTokens,
   CostUSD:   trace.CostUSD,
  },
  Stratum:   "", // TODO(P1b)：租户 tier 接入后填充
  Verdict:   domain.VerdictPass,
  CreatedAt: time.Now().UTC(),
 }
}

// applyJudge 按三维度 rubric 调用 judge 并填充 signals；任一次失败返回错误
// （上层降级），已完成维度不回滚（保留部分信号）。judge 关闭时跳过。
func (s *ObservationService) applyJudge(ctx context.Context, trace port.ObservedTrace, obs *domain.EvalObservation) error {
 if s.deps.Judge == nil || !s.deps.Judge.Enabled(ctx) {
  return nil
 }
 start := time.Now()
 for _, dimension := range observationJudgeDimensions {
  res, err := s.deps.Judge.Judge(ctx, port.JudgeRequest{
   Model:          "",
   Rubric:         judgeRubric(dimension),
   Input:          trace.Input,
   ExpectedOutput: "",
   Actual:         trace.Output,
  })
  if err != nil {
   return fmt.Errorf("judge dimension %s: %w", dimension, err)
  }
  // LLMJudge 契约返回 domain.AssertionResult{Passed, Message}：P1a 把维度
  // 通过映射为 1.0 / 0.0。
  // TODO(P1b)：judge 返回结构化 score/confidence 后填充真实置信度。
  score := 0.0
  if res.Passed {
   score = 1.0
  }
  obs.Signals.Judge = append(obs.Signals.Judge, domain.JudgeSignal{
   Dimension:  dimension,
   Score:      score,
   Confidence: 1.0,
  })
  s.deps.Metrics.RecordEvalJudgeScore(obs.Resource.Kind, dimension, score)
 }
 seconds := time.Since(start).Seconds()
 s.deps.Metrics.RecordEvalJudgeLatency(seconds)
 s.deps.Metrics.RecordEvalJudgeCost(trace.CostUSD)
 // 任一维度低于阈值视为 flag（仅信号级，非门禁判定）。
 if anyJudgeBelow(obs.Signals.Judge, 0.5) {
  obs.Verdict = domain.VerdictFlag
 }
 return nil
}

// judgeRubric 构造单维度 judge 提示词（与 judgeAdapter 的 Complete 输出契约
// {"passed","reason"} 对齐：这里的 rubric 指示 LLM 按指定维度判定 pass/不通过）。
func judgeRubric(dimension string) string {
 return fmt.Sprintf("请按维度「%s」对助手回答判定通过/不通过，并给出理由。忠实于给定上下文、切题、覆盖全部关键点。", dimension)
}

func anyJudgeBelow(signals []domain.JudgeSignal, threshold float64) bool {
 for _, s := range signals {
  if s.Score < threshold {
   return true
  }
 }
 return false
}

// ListObservations 分页查询观测明细（查询 API 数据源）。
func (s *ObservationService) ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
 from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
 if s.deps.Repo == nil {
  return nil, fmt.Errorf("observation service: repository unavailable")
 }
 return s.deps.Repo.QueryByResource(ctx, tenantID, resourceKind, resourceID, from, to, limit, offset)
}

// GetObservation 取单条观测明细。
func (s *ObservationService) GetObservation(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error) {
 if s.deps.Repo == nil {
  return nil, fmt.Errorf("observation service: repository unavailable")
 }
 return s.deps.Repo.Get(ctx, tenantID, id)
}

// JudgeRequest 的预期输出当前留空：运行态观测无 golden（评测集才有 ExpectedOutput）。
```

> 签名已锁定：`evalport.LLMJudge.Judge(ctx, req) (domain.AssertionResult, error)`，`AssertionResult{Passed bool; Message string}` 仅布尔+文本，**无 Score/Confidence 字段**。applyJudge 采用二进制映射（Passed→Score 1.0/0.0，Confidence 固定 1.0，见上方代码注释）；`anyJudgeBelow(0.5)` 阈值即对应此映射——未通过维度 0.0 < 0.5 → flag。P1b 引入数值打分时替换映射。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/evaluation/application/ -run "TestObservationService" -v`
Expected: PASS（4 个用例）。

- [ ] **Step 5: 全量回归**

Run: `go test ./internal/evaluation/application/ -short -timeout 60s`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/evaluation/application/observation_service.go internal/evaluation/application/observation_service_test.go
git commit -m "feat(evaluation): 观测落地服务(采样/judge/落库)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: NATS 观测消费 worker（含 DLQ + 积压指标）

**Files:**

- Create: `internal/evaluation/infrastructure/observation/dead_letter.go`
- Create: `internal/evaluation/infrastructure/observation/consumer.go`
- Create: `internal/evaluation/infrastructure/observation/streams.go`
- Test: `internal/evaluation/infrastructure/observation/consumer_test.go`

**Interfaces:**

- Consumes: `domain.ObservationReferenceEvent`（Task 2）、`*ObservationService`（Task 9）、`constants.Observation*`（Task 2）、`jetstream.Consumer`（wiring 用 `pipeline.CreateConsumer` 创建后注入）、`jetstream.JetStream`（作为 `dlqPublisher` 实现）。
- Produces: `EnsureStreams(ctx context.Context, js jetstream.JetStream) error`（观察流 + DLQ 流，幂等）；`NewObservationConsumerWorker(consumer jetstream.Consumer, js dlqPublisher, processor ObservationProcessor, metrics observability.MetricsProvider, logger *zap.Logger, ackWait time.Duration, maxDeliver int) *ObservationConsumerWorker` + `Start(ctx) error` / `Stop()`。Task 12 wiring 装配。**memory pipeline 的 `runConsumerLoop`/`consumerStopGuard`/`startProgressHeartbeat`/`deadLetterWithHeartbeat`/`retryOrDeadLetterWithHeartbeat` 均为包内私有，跨包不可复用——本包独立复刻，签名与 `internal/memory/infrastructure/pipeline/{jetstream_worker,dead_letter}.go` 对齐（实现前先读这两个文件，字段名照抄 memory）**。

- [ ] **Step 1: 写失败测试（处理逻辑：正常/禁用/解析失败）**

```go
// internal/evaluation/infrastructure/observation/consumer_test.go
package observation

import (
 "context"
 "errors"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"
)

type stubProcessor struct {
 events []domain.ObservationReferenceEvent
 err    error
}

func (s *stubProcessor) Process(ctx context.Context, evt domain.ObservationReferenceEvent) error {
 if s.err != nil {
  return s.err
 }
 s.events = append(s.events, evt)
 return nil
}

func newTestWorker(proc *stubProcessor, pub *fakePublisher) *ObservationConsumerWorker {
 return &ObservationConsumerWorker{
  js: pub, processor: proc, metrics: observability.NoopMetrics{}, logger: zap.NewNop(),
  ackWait: 30 * time.Second, maxDeliver: constants.ObservationMaxDeliver,
 }
}

func TestConsumerProcessMessageAcks(t *testing.T) {
 proc := &stubProcessor{}
 msg := &fakeMsg{
  data:      []byte(`{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00Z"}`),
  delivered: 1,
 }
 newTestWorker(proc, &fakePublisher{}).processMessage(context.Background(), msg)
 if msg.ackCount != 1 {
  t.Fatalf("expected ack, got ack=%d", msg.ackCount)
 }
 if len(proc.events) != 1 || proc.events[0].TraceID != "trace-1" {
  t.Fatalf("processor events mismatch: %+v", proc.events)
 }
}

func TestConsumerProcessMessageMalformedDeadLetters(t *testing.T) {
 pub := &fakePublisher{}
 msg := &fakeMsg{data: []byte("{not json"), delivered: 1}
 newTestWorker(&stubProcessor{}, pub).processMessage(context.Background(), msg)
 if msg.dlqCount != 1 || msg.termReason == "" {
  t.Fatalf("expected DLQ+Term on malformed, got dlq=%d reason=%q", msg.dlqCount, msg.termReason)
 }
 if len(pub.subjects) == 0 || pub.subjects[0] != constants.ObservationSubjectPrefix+".dlq" {
  t.Fatalf("expected DLQ publish to %s, got %v", constants.ObservationSubjectPrefix+".dlq", pub.subjects)
 }
}

func TestConsumerProcessMessageErrorRedelivers(t *testing.T) {
 proc := &stubProcessor{err: errors.New("evidence down")}
 msg := &fakeMsg{
  data:      []byte(`{"tenant_id":"t1","trace_id":"x","resource_kind":"agent","resource_id":"a1"}`),
  delivered: constants.ObservationMaxDeliver - 1, // 未达上限 → Nak 重投
 }
 newTestWorker(proc, &fakePublisher{}).processMessage(context.Background(), msg)
 if msg.nakCount != 1 || msg.dlqCount != 0 {
  t.Fatalf("expected NakWithDelay redelivery, got nak=%d dlq=%d", msg.nakCount, msg.dlqCount)
 }
}

func TestConsumerProcessMessageErrorDeadLettersAfterMax(t *testing.T) {
 proc := &stubProcessor{err: errors.New("evidence down")}
 pub := &fakePublisher{}
 msg := &fakeMsg{
  data:      []byte(`{"tenant_id":"t1","trace_id":"x","resource_kind":"agent","resource_id":"a1"}`),
  delivered: constants.ObservationMaxDeliver, // 已达上限 → DLQ
 }
 newTestWorker(proc, pub).processMessage(context.Background(), msg)
 if msg.dlqCount != 1 || msg.termReason == "" {
  t.Fatalf("expected DLQ+Term after max deliver, got dlq=%d reason=%q", msg.dlqCount, msg.termReason)
 }
 if len(pub.subjects) == 0 {
  t.Fatal("expected DLQ publish")
 }
}
```

配套 mock：

```go
// consumer_test.go 内部
type fakeMsg struct {
 jetstream.Msg // 嵌入接口零实现，编译期校验签名
 data          []byte
 delivered     int
 ackCount      int
 nakCount      int
 dlqCount      int
 termReason    string
}

func (m *fakeMsg) Data() []byte                 { return m.data }
func (m *fakeMsg) Subject() string              { return "evaluation.observe.t1" }
func (m *fakeMsg) Headers() jetstream.Headers   { return nil }
func (m *fakeMsg) Ack() error                   { m.ackCount++; return nil }
func (m *fakeMsg) Nak() error                   { m.nakCount++; return nil }
func (m *fakeMsg) NakWithDelay(delay time.Duration) error { m.nakCount++; return nil }
func (m *fakeMsg) InProgress() error            { return nil }
func (m *fakeMsg) TermWithReason(reason string) error { m.dlqCount++; m.termReason = reason; return nil }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
 return &jetstream.MsgMetadata{NumDelivered: uint64(m.delivered)}, nil
}

type fakePublisher struct {
 subjects []string
 datas    [][]byte
 err      error
}

func (p *fakePublisher) Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
 if p.err != nil {
  return nil, p.err
 }
 p.subjects = append(p.subjects, subject)
 p.datas = append(p.datas, data)
 return &jetstream.PubAck{}, nil
}
```

> 注：`jetstream.Msg` 是接口，`fakeMsg` 嵌入 `jetstream.Msg` 零实现 + 覆盖用到的行为（编译期自动校验签名，无需手抄全部方法）。`jetstream.MsgMetadata` 字段全部导出，`&jetstream.MsgMetadata{NumDelivered: ...}` 可直接构造；`retryOrDeadLetterWithHeartbeat` 读取 `NumDelivered` 决定重投 vs DLQ，因此测试的 `fakeMsg` 必须返回非 nil metadata。`fakePublisher` 实现 `dlqPublisher`（`Publish(ctx, subject, data, ...PublishOpt)`），`jetstream.JetStream` 在 wiring 中满足同一接口。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/infrastructure/observation/ -run "TestConsumerProcessMessage" -v`
Expected: FAIL（包不存在/类型未定义，编译错误）。

- [ ] **Step 3: 实现 DLQ + 消费 worker**

`internal/evaluation/infrastructure/observation/dead_letter.go`（仿 `internal/memory/infrastructure/pipeline/dead_letter.go`，字段/签名照抄；`dlqPublisher`/`deadLetterDetails`/`DeadLetterEvent`/`deadLetterWithHeartbeat`/`retryOrDeadLetterWithHeartbeat`/`startProgressHeartbeat`/`tenantFromObservationSubject`）：

```go
package observation

import (
 "context"
 "encoding/json"
 "fmt"
 "strings"
 "sync"
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/nats-io/nats.go/jetstream"
)

// dlqPublisher 抽象发布死信消息的 JetStream publisher。
// jetstream.JetStream 满足该接口（Publish(ctx, subject, data, ...PublishOpt)），wiring 直接传入。
type dlqPublisher interface {
 Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// deadLetterDetails 死信元数据。
type deadLetterDetails struct {
 Stage     string
 TenantID  string
 MessageID string
 TraceID   string
 ErrorCode string
}

// DeadLetterEvent 死信事件载荷（发布到 DLQ 流，供复盘/重放）。
type DeadLetterEvent struct {
 Stage        string `json:"stage"`
 TenantID     string `json:"tenant_id"`
 MessageID    string `json:"message_id"`
 TraceID      string `json:"trace_id"`
 ErrorCode    string `json:"error_code"`
 OriginalSubj string `json:"original_subject"`
 Payload      string `json:"payload"`
}

// tenantFromObservationSubject 从 subject "evaluation.observe.<tenant>" 提取 tenantID。
// 解析失败返回 ""（仅用于死信元数据标注，不影响 DLQ 发布）。
func tenantFromObservationSubject(subject string) string {
 parts := strings.Split(subject, ".")
 if len(parts) >= 3 {
  return parts[2]
 }
 return ""
}

// startProgressHeartbeat 周期发送 InProgress 保持消息 in-flight（judge 可能超过 AckWait，
// 防误判超时重投）。interval <= 0 时 noop。返回 stop func，可安全多次调用（sync.Once）。
func startProgressHeartbeat(msg jetstream.Msg, interval time.Duration) func() {
 if interval <= 0 {
  return func() {}
 }
 done := make(chan struct{})
 var once sync.Once
 go func() {
  ticker := time.NewTicker(interval)
  defer ticker.Stop()
  for {
   select {
   case <-done:
    return
   case <-ticker.C:
    _ = msg.InProgress()
   }
  }
 }()
 return func() { once.Do(func() { close(done) }) }
}

// deadLetterWithHeartbeat 发布死信事件到 DLQ 流（带去重 msgID），随后 Term 原消息。
func deadLetterWithHeartbeat(ctx context.Context, pub dlqPublisher, msg jetstream.Msg, stopHeartbeat func(), details deadLetterDetails) error {
 event := DeadLetterEvent{
  Stage: details.Stage, TenantID: details.TenantID, MessageID: details.MessageID,
  TraceID: details.TraceID, ErrorCode: details.ErrorCode,
  OriginalSubj: msg.Subject(), Payload: string(msg.Data()),
 }
 data, err := json.Marshal(event)
 if err != nil {
  stopHeartbeat()
  return fmt.Errorf("marshal dead letter: %w", err)
 }
 subject := constants.ObservationSubjectPrefix + ".dlq"
 if _, err := pub.Publish(ctx, subject, data, jetstream.WithMsgID(fmt.Sprintf("%s-%s", details.Stage, details.MessageID))); err != nil {
  stopHeartbeat()
  return fmt.Errorf("publish dead letter: %w", err)
 }
 stopHeartbeat()
 return msg.TermWithReason(details.ErrorCode)
}

// retryOrDeadLetterWithHeartbeat 按重投计数决定：未达上限 NakWithDelay 重投，达上限进 DLQ。
// 返回 (是否进 DLQ, error)。
func retryOrDeadLetterWithHeartbeat(ctx context.Context, pub dlqPublisher, msg jetstream.Msg, maxDeliver int, stopHeartbeat func(), details deadLetterDetails) (bool, error) {
 meta, err := msg.Metadata()
 if err != nil {
  stopHeartbeat()
  return false, fmt.Errorf("msg metadata: %w", err)
 }
 if meta.NumDelivered >= uint64(maxDeliver) {
  return true, deadLetterWithHeartbeat(ctx, pub, msg, stopHeartbeat, details)
 }
 stopHeartbeat()
 if err := msg.NakWithDelay(constants.ObservationFetchBackoffBase); err != nil {
  return false, fmt.Errorf("nak with delay: %w", err)
 }
 return false, nil
}
```

> 注：`msg.NakWithDelay` 延迟用 `constants.ObservationFetchBackoffBase`（1s）；`jetstream.WithMsgID` 带去重 key。Publish 失败/元数据读取失败时仅记录日志并 Term 兜底（详见 consumer.go 的 `processMessage`），不得让死信环节阻塞主消费循环。

`internal/evaluation/infrastructure/observation/consumer.go`（仿 memory 的 extraction_consumer 消费循环，`jetstream.Consumer`/`jetstream.Msg`/`FetchMaxWait` + 退避 + safeProcess + stop guard）：

```go
package observation

import (
 "context"
 "encoding/json"
 "fmt"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"
)

// ObservationProcessor 消费 worker 委托的落地服务接口（便于测试注入）。
type ObservationProcessor interface {
 Process(ctx context.Context, evt domain.ObservationReferenceEvent) error
}

// consumerStopGuard 幂等停止 + 循环完成信号（与 memory 的 consumerStopGuard 语义一致）。
type consumerStopGuard struct {
 stopCh chan struct{}
 doneCh chan struct{}
}

func newConsumerStopGuard() *consumerStopGuard {
 return &consumerStopGuard{stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (g *consumerStopGuard) Stop() {
 select {
 case <-g.stopCh:
 default:
  close(g.stopCh)
 }
}

// ObservationConsumerWorker 消费 evaluation.observe.{tenant} 引用事件，
// 采样后交由 ObservationService 落库。失败重投，超 MaxDeliver 进 DLQ。
// 消费循环与 memory 的 ExtractionConsumerWorker 同构（Fetch(1)+退避+stop guard）。
type ObservationConsumerWorker struct {
 consumer   jetstream.Consumer // wiring 经 pipeline.CreateConsumer 创建后注入
 js         dlqPublisher       // 死信发布（wiring 传 jetstream.JetStream）
 processor  ObservationProcessor
 metrics    observability.MetricsProvider
 logger     *zap.Logger
 ackWait    time.Duration // 心跳间隔 = ackWait/2
 maxDeliver int
 guard      *consumerStopGuard
}

func NewObservationConsumerWorker(consumer jetstream.Consumer, js dlqPublisher, processor ObservationProcessor,
 metrics observability.MetricsProvider, logger *zap.Logger, ackWait time.Duration, maxDeliver int,
) *ObservationConsumerWorker {
 return &ObservationConsumerWorker{
  consumer: consumer, js: js, processor: processor, metrics: metrics, logger: logger,
  ackWait: ackWait, maxDeliver: maxDeliver, guard: newConsumerStopGuard(),
 }
}

// Start 启动消费循环与积压指标采集（背靠背运行，Stop 统一收敛）。调用方以 go 启动。
func (w *ObservationConsumerWorker) Start(ctx context.Context) error {
 if w.consumer == nil {
  return fmt.Errorf("observation consumer: jetstream unavailable")
 }
 go w.runConsumerLoop(ctx)
 go w.reportBacklog(ctx)
 return nil
}

// Stop 停止消费循环（幂等；等待当前循环退出）。
func (w *ObservationConsumerWorker) Stop() {
 w.guard.Stop()
 <-w.guard.doneCh
}

func (w *ObservationConsumerWorker) runConsumerLoop(ctx context.Context) {
 defer close(w.guard.doneCh)
 backoff := constants.ObservationFetchBackoffBase
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.guard.stopCh:
   return
  default:
  }
  msgs, err := w.consumer.Fetch(1, jetstream.FetchMaxWait(constants.ObservationFetchMaxWait))
  if err != nil {
   w.logger.Warn("observation consumer fetch failed",
    zap.Error(err), zap.Duration("backoff", backoff))
   if !sleepObservationCtx(ctx, w.guard.stopCh, backoff) {
    return
   }
   backoff = minDuration(backoff*2, constants.ObservationFetchBackoffMax)
   continue
  }
  backoff = constants.ObservationFetchBackoffBase
  for msg := range msgs.Messages() {
   w.safeProcessMessage(ctx, msg)
  }
 }
}

// sleepObservationCtx 可中断退避；返回 false 表示应退出循环。
func sleepObservationCtx(ctx context.Context, stopCh chan struct{}, d time.Duration) bool {
 t := time.NewTimer(d)
 defer t.Stop()
 select {
 case <-ctx.Done():
  return false
 case <-stopCh:
  return false
 case <-t.C:
  return true
 }
}

// safeProcessMessage 捕获处理 panic（与 memory 一致：panic 即 Nak 重投）。
func (w *ObservationConsumerWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Warn("observation consumer panic", zap.Any("panic", r))
   _ = msg.Nak()
  }
 }()
 w.processMessage(ctx, msg)
}

// processMessage 反序列化 → 委托落地服务；无返回值（ack/nak/DLQ 在内部处理，memory 风格）。
func (w *ObservationConsumerWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
 stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
 defer stopHeartbeat()

 var evt domain.ObservationReferenceEvent
 if err := json.Unmarshal(msg.Data(), &evt); err != nil {
  w.metrics.IncEvalJudgeFailure("malformed_event")
  w.logger.Warn("observation malformed event",
   zap.Error(err), zap.String("subject", msg.Subject()))
  details := deadLetterDetails{
   Stage: "observation", TenantID: tenantFromObservationSubject(msg.Subject()),
   MessageID: msg.Subject(), ErrorCode: "malformed_event",
  }
  _ = deadLetterWithHeartbeat(ctx, w.js, msg, stopHeartbeat, details)
  return
 }
 if err := w.processor.Process(ctx, evt); err != nil {
  w.logger.Warn("observation process failed",
   zap.Error(err), zap.String("subject", msg.Subject()))
  details := deadLetterDetails{
   Stage: "observation", TenantID: evt.TenantID, MessageID: evt.TraceID,
   TraceID: evt.TraceID, ErrorCode: "processor_error",
  }
  if wentDLQ, err := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, details); err != nil {
   w.logger.Warn("observation redeliver/dead-letter failed", zap.Error(err))
  } else if !wentDLQ {
   w.metrics.IncEvalJudgeFailure("redeliver")
  }
  return
 }
 stopHeartbeat()
 _ = msg.Ack()
}

// reportBacklog 周期上报 NATS 消费积压（规格 §11.2 eval_queue_backlog）。
func (w *ObservationConsumerWorker) reportBacklog(ctx context.Context) {
 ticker := time.NewTicker(constants.ObservationBacklogInterval)
 defer ticker.Stop()
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.guard.stopCh:
   return
  case <-ticker.C:
   info, err := w.consumer.ConsumerInfo()
   if err != nil {
    w.logger.Warn("observation backlog query failed", zap.Error(err))
    continue
   }
   w.metrics.SetEvalQueueBacklog("observation", int64(info.NumPending))
  }
 }
}

func minDuration(a, b time.Duration) time.Duration {
 if a < b {
  return a
 }
 return b
}
```

> 注：消费 API 全部来自 `jetstream` 包（`jetstream.Consumer`/`jetstream.Msg`/`jetstream.FetchMaxWait`），与 memory 的 `jetstream_worker.go`/`extraction_consumer.go` 对齐。`runConsumerLoop`/`consumerStopGuard`/`startProgressHeartbeat`/`deadLetterWithHeartbeat`/`retryOrDeadLetterWithHeartbeat` 在 memory 包内为私有实现、跨包不可复用，本包独立复刻（实现前先读 `internal/memory/infrastructure/pipeline/dead_letter.go` 与 `jetstream_worker.go` 对齐字段名）。`w.maxDeliver` 来自 `constants.ObservationMaxDeliver`；重投延迟用 `constants.ObservationFetchBackoffBase`。DLQ subject 形如 `evaluation.observe.dlq`（无租户后缀，租户写入事件字段）。

`internal/evaluation/infrastructure/observation/streams.go`（幂等创建观察流与 DLQ 流，供 Task 12 wiring 调用）：

```go
package observation

import (
 "context"
 "fmt"

 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/nats-io/nats.go/jetstream"
)

// EnsureStreams 幂等创建观察引用事件流与死信流（CreateOrUpdateStream，重复调用安全）。
// 观察流用 WorkQueuePolicy（单消费者语义，配合 durable consumer）；
// DLQ 流用 LimitsPolicy 保留失败消息供复盘。
func EnsureStreams(ctx context.Context, js jetstream.JetStream) error {
 if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
  Name:      constants.ObservationStream,
  Subjects:  []string{constants.ObservationSubjectPrefix + ".>"},
  Storage:   jetstream.FileStorage,
  Retention: jetstream.WorkQueuePolicy,
  MaxAge:    constants.ObservationStreamMaxAge,
 }); err != nil {
  return fmt.Errorf("ensure observation stream: %w", err)
 }
 if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
  Name:      constants.ObservationDLQStream,
  Subjects:  []string{constants.ObservationSubjectPrefix + ".dlq"},
  Storage:   jetstream.FileStorage,
  Retention: jetstream.LimitsPolicy,
  MaxAge:    constants.ObservationDLQMaxAge,
 }); err != nil {
  return fmt.Errorf("ensure observation dlq stream: %w", err)
 }
 return nil
}
```

> 注：Task 12 wiring 顺序为 `observation.EnsureStreams(ctx, jsm.JS())` → `jsm.CreateConsumer(ctx, ...)` → `observation.NewObservationConsumerWorker(...)`。consumer 创建与流创建分离，本任务只做流，不做 consumer。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/evaluation/infrastructure/observation/ -run "TestConsumerProcessMessage" -v`
Expected: PASS（4 个用例：ack / malformed→DLQ / redeliver / DLQ-after-max）。

- [ ] **Step 5: 编译收敛 + 全量回归**

Run: `go build ./internal/evaluation/...`
Expected: 编译成功。

- [ ] **Step 6: 提交**

```bash
git add internal/evaluation/infrastructure/observation/dead_letter.go \
        internal/evaluation/infrastructure/observation/consumer.go \
        internal/evaluation/infrastructure/observation/streams.go \
        internal/evaluation/infrastructure/observation/consumer_test.go
git commit -m "feat(evaluation): 观测引用事件 NATS 消费 worker(DLQ+积压)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 11: 观测查询 API（后端 + 契约）

**Files:**

- Modify: `api/http/handler/evaluation_handler.go`（`WithObservationService` + 2 端点）
- Modify: `api/http/router.go`（evaluations group 加 2 条路由）
- Test: `api/http/handler/evaluation_observation_test.go`
- （契约 golden：延后到 Task 12 wiring 完成后添加）

**Interfaces:**

- Consumes: `*evalapp.ObservationService`（Task 9 的 `ListObservations`/`GetObservation`）、`EvaluationHandler` 现有 `WithXxxService` 注入模式（读 `evaluation_handler.go` 确认小接口字段风格）。
- Produces: `GET /api/evaluations/observations?resource_kind=&resource_id=&from=&to=&page=&page_size=` → `{ items: [...], total }`；`GET /api/evaluations/observations/:id` → 单条。P1d 前端消费。

- [ ] **Step 1: 写失败测试（handler 行为）**

先读 `api/http/handler/evaluation_handler.go` 顶部确认 handler 小接口字段风格与 `evaluationObservationService` 接口命名，然后：

```go
// api/http/handler/evaluation_observation_test.go
package handler

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

type stubObservationQueryService struct {
 items []domain.EvalObservation
 err   error
}

func (s *stubObservationQueryService) ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
 from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
 if s.err != nil {
  return nil, s.err
 }
 return s.items, nil
}

func (s *stubObservationQueryService) GetObservation(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error) {
 if len(s.items) > 0 {
  return &s.items[0], nil
 }
 return nil, nil
}

func TestListObservationsHandler(t *testing.T) {
 gin.SetMode(gin.TestMode)
 h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
 h.WithObservationService(&stubObservationQueryService{items: []domain.EvalObservation{
  {ID: "obs-1", TraceID: "trace-1", Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"}, Verdict: domain.VerdictPass},
 }})

 req := httptest.NewRequest(http.MethodGet, "/api/evaluations/observations?resource_kind=agent&resource_id=agent-1", nil)
 w := httptest.NewRecorder()
 ctx, _ := gin.CreateTestContext(w)
 ctx.Request = req
 h.ListObservations(ctx)

 if w.Code != http.StatusOK {
  t.Fatalf("status = %d, want 200", w.Code)
 }
 var body struct {
  Items []domain.EvalObservation `json:"items"`
  Total int                      `json:"total"`
 }
 if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
  t.Fatalf("decode body: %v", err)
 }
 if len(body.Items) != 1 || body.Total != 1 {
  t.Fatalf("body mismatch: %s", w.Body.String())
 }
}

func TestGetObservationHandler(t *testing.T) {
 gin.SetMode(gin.TestMode)
 h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
 h.WithObservationService(&stubObservationQueryService{items: []domain.EvalObservation{{ID: "obs-1", Verdict: domain.VerdictPass}}})

 req := httptest.NewRequest(http.MethodGet, "/api/evaluations/observations/obs-1", nil)
 w := httptest.NewRecorder()
 ctx, _ := gin.CreateTestContext(w)
 ctx.Request = req
 ctx.Params = gin.Params{{Key: "id", Value: "obs-1"}}
 h.GetObservation(ctx)

 if w.Code != http.StatusOK {
  t.Fatalf("status = %d, want 200", w.Code)
 }
}
```

> 注：`NewEvaluationHandler` 实际签名是 9 个参数（suites/jobs/runs/optimization/experiments/feedback/queries/candidates + logger），handler 测试用 `NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())` 构造（`zap.NewNop()` 避免噪音日志）。后续 P1b 若新增依赖，沿用 setter 注入，不改构造函数。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/http/handler/ -run "TestListObservationsHandler|TestGetObservationHandler" -v`
Expected: FAIL（`WithObservationService`/`ListObservations`/`GetObservation` 未定义）。

- [ ] **Step 3: 实现 handler 注入 + 端点**

`api/http/handler/evaluation_handler.go`：

```go
// 追加到 EvaluationHandler 现有小接口字段区：
type evaluationObservationQueryService interface {
 ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
  from, to *time.Time, limit, offset int) ([]domain.EvalObservation, error)
 GetObservation(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error)
}
```

在现有 `WithRoleResolver` 等 Setter 附近追加：

```go
// WithObservationService 注入运行态观测查询服务（P1a 查询 API）。
func (h *EvaluationHandler) WithObservationService(service evaluationObservationQueryService) *EvaluationHandler {
 h.observations = service
 return h
}
```

新增两个方法（放 handler 文件末尾，遵循 bind → tenant → service → render → c.Error 风格，`requireAdmin` 守卫由路由层保证）：

```go
// ListObservations 返回运行态观测明细分页（规格 §10.1 数据源）。
func (h *EvaluationHandler) ListObservations(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 if h.observations == nil {
  c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation observation unavailable")))
  return
 }
 var req ListObservationsQuery
 if err := c.ShouldBindQuery(&req); err != nil {
  c.Error(err)
  return
 }
 if req.Page < 1 {
  req.Page = 1
 }
 if req.PageSize <= 0 || req.PageSize > 100 {
  req.PageSize = 20
 }
 limit, offset := req.PageSize, (req.Page-1)*req.PageSize
 items, err := h.observations.ListObservations(c.Request.Context(), tenantID,
  req.ResourceKind, req.ResourceID, req.From, req.To, limit, offset)
 if err != nil {
  c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// GetObservation 返回单条运行态观测明细。
func (h *EvaluationHandler) GetObservation(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 if h.observations == nil {
  c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation observation unavailable")))
  return
 }
 obs, err := h.observations.GetObservation(c.Request.Context(), tenantID, c.Param("id"))
 if err != nil {
  c.Error(err)
  return
 }
 if obs == nil {
  c.JSON(http.StatusNotFound, gin.H{"error": "observation not found"})
  return
 }
 c.JSON(http.StatusOK, obs)
}
```

配套：`evaluationObservationQueryService` 接口需要的 import（`time`、`domain`）；DTO 结构与守卫常量（`ErrObservationUnavailable`）定义放 handler 文件：

```go
// ListObservationsQuery 观测分页查询参数（from/to 可选，RFC3339）。
type ListObservationsQuery struct {
 ResourceKind string     `form:"resource_kind"`
 ResourceID   string     `form:"resource_id"`
 From         *time.Time `form:"from" time_format:"2006-01-02T15:04:05Z07:00"`
 To           *time.Time `form:"to" time_format:"2006-01-02T15:04:05Z07:00"`
 Page         int        `form:"page"`
 PageSize     int        `form:"page_size"`
}
```

> 注：`tenantIDFromCtx(c)`/`respondMissingTenant(c)` 为 `api/http/handler` 现有公共 helper（与 `evaluation_handler.go` 内其他方法一致）。`normalizePage` 不存在——分页按上面内联（Page<1→1；PageSize≤0 或 >100→20）。服务不可用返回 `middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation observation unavailable"))`（由统一错误中间件映射 503）。`evaluation_handler.go` 新增 import：`errors`、`time`、`middleware`、`domain`。

- [ ] **Step 4: 注册路由（`api/http/router.go`）**

在 `evaluations` group 内（`evaluations.GET("/overview", ...)` 附近）追加：

```go
  evaluations.GET("/observations", h.ListObservations)
  evaluations.GET("/observations/:id", h.GetObservation)
```

> 注：`evaluations` group 已带 `RequireTenantRole("member")`，观测查询为租户自有运行数据，member 即可读（无需 requireAdmin）。

- [ ] **Step 5: 单元测试回归（契约 golden 延后到 Task 12）**

`contract_test.go` 的 golden 校验需要完整 `wiring.Container`（含观测 repository/服务），而 wiring 装配在 Task 12 才完成——契约用例与 golden 文件延后到 Task 12 Step 4 一并添加。本步仅确认 handler 层测试通过：

Run: `go test ./api/http/handler/ -run "TestListObservationsHandler|TestGetObservationHandler" -v`
Expected: PASS。

- [ ] **Step 6: 运行全部 handler 测试确认通过**

Run: `go test ./api/http/... -short -timeout 90s`
Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add api/http/handler/evaluation_handler.go api/http/handler/evaluation_observation_test.go \
        api/http/router.go
git commit -m "feat(evaluation): 运行态观测查询 API(列表+明细)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 12: wiring 装配（端到端连通）

**Files:**

- Modify: `api/wiring/evaluation.go`（`buildEvaluation`：EnsureStreams + CreateConsumer + consumer worker + metrics + shutdown；`wiring.Evaluation` 结构体加 `ObservationService` 字段）
- Modify: `api/wiring/agent.go`（agent service 注入 observationEmitterAdapter）
- Modify: `api/http/router.go`（`registerEvaluations`：`.WithObservationService(...)` + 2 条路由）
- Modify: `api/http/contract_test.go` + `api/http/testdata/contracts/*.golden.json`（观测契约用例 + golden）
- Test: 无新增单测（wiring 为组合根，端到端验证放验收步骤）；`api/wiring/agent_test.go` 若存在且构造受影响需回归。

**Interfaces:**

- Consumes: 全部 Task 产出（`observationEmitterAdapter`、`ObservationService`、`ObservationConsumerWorker`、`ObservationRepository`、`evaluationTraceEvidenceAdapter`、`buildEvaluationJudge`、`platformMetrics`），以及 `pipeline.NewJetStreamManager`/`jsm.CreateConsumer`/`observation.EnsureStreams`。
- Produces: 装配完成的观测链路（发布 → 消费 → 落库 → 指标 + 查询 API + 契约）。

- [ ] **Step 1: 实现 wiring 装配（`api/wiring/evaluation.go`）**

在 `buildEvaluation` 内（`worker.Start(ctx)` 附近，确保 `c.Storage.NATS != nil` 且已装配 repo/judge/evidence 后）追加观测装配。`ctx` 复用 `buildEvaluation` 内已有变量（若没有则用 `context.Background()`）。**同时修改同文件 `api/wiring/evaluation.go` 的 `wiring.Evaluation` 结构体（第 31 行）：追加 `ObservationService *evalapp.ObservationService` 字段**（router.go 经 `c.Evaluation.ObservationService` 注入 handler；命名风格与现有 `SuiteService`/`JobService` 一致）：

```go
 // ---- 运行态观测（P1a）----
 observationRepo := evalpersist.NewPgObservationRepository(db)
 observationSvc := evalapp.NewObservationService(evalapp.ObservationServiceDeps{
  Enabled:    func(ctx context.Context) bool { return observationEnabled(ctx, c.Parameters.Service) },
  SampleRate: func(ctx context.Context) float64 { return observationSampleRate(ctx, c.Parameters.Service) },
  Evidence:   traceEvidence,
  Judge:      buildEvaluationJudge(c),
  Repo:       observationRepo,
  Metrics:    c.platformMetrics(),
  Logger:     c.Logger,
 })
 // c.Evaluation.ObservationService 供 router.go registerEvaluations 注入 handler。
 c.Evaluation.ObservationService = observationSvc
 if c.Storage.NATS != nil {
  jsm, err := pipeline.NewJetStreamManager(c.Storage.NATS, c.Logger)
  if err != nil {
   return fmt.Errorf("observation jetstream manager: %w", err)
  }
  if err := observation.EnsureStreams(ctx, jsm.JS()); err != nil {
   return fmt.Errorf("observation ensure streams: %w", err)
  }
  consumer, err := jsm.CreateConsumer(ctx, constants.ObservationStream,
   constants.ObservationConsumerName, constants.ObservationSubjectPrefix+".>",
   constants.ObservationAckWait, constants.ObservationMaxDeliver)
  if err != nil {
   return fmt.Errorf("observation ensure consumer: %w", err)
  }
  observationWorker := observation.NewObservationConsumerWorker(consumer, jsm.JS(),
   observationSvc, c.platformMetrics(), c.Logger,
   constants.ObservationAckWait, constants.ObservationMaxDeliver)
  if err := observationWorker.Start(ctx); err != nil {
   return fmt.Errorf("observation consumer start: %w", err)
  }
  c.shutdown = append(c.shutdown, func(context.Context) error {
   observationWorker.Stop()
   return nil
  })
 }
```

配套 helper（`api/wiring/evaluation.go` 文件内）：

```go
// observationEnabled 读取 evaluation.observe.enabled 平台参数（fail closed）。
func observationEnabled(ctx context.Context, params *parametersapp.Service) bool {
 if params == nil {
  return false
 }
 values, err := params.PlatformValues(ctx)
 if err != nil {
  return false
 }
 enabled, _ := values["evaluation.observe.enabled"].(bool)
 return enabled
}

// observationSampleRate 读取 evaluation.observe.sample_rate 平台参数（默认 0.1）。
func observationSampleRate(ctx context.Context, params *parametersapp.Service) float64 {
 if params == nil {
  return constants.ObservationSampleRateDefault
 }
 values, err := params.PlatformValues(ctx)
 if err != nil {
  return constants.ObservationSampleRateDefault
 }
 if rate, ok := values["evaluation.observe.sample_rate"].(float64); ok && rate >= 0 && rate <= 1 {
  return rate
 }
 return constants.ObservationSampleRateDefault
}
```

> 注：API 已锁定——`pipeline.NewJetStreamManager(nc *nats.Conn, logger)` → `jsm.JS() jetstream.JetStream`；流创建用 `observation.EnsureStreams(ctx, js)`（内部 CreateOrUpdateStream，主流 WorkQueue + DLQ Limits，幂等）；consumer 创建用 `jsm.CreateConsumer(ctx, stream, name, filterSubject string, ackWait time.Duration, maxDeliver int)`（内部 Durable + AckExplicitPolicy）；worker 构造 `observation.NewObservationConsumerWorker(consumer, jsm.JS(), observationSvc, c.platformMetrics(), c.Logger, constants.ObservationAckWait, constants.ObservationMaxDeliver)`。`evaluation.go` 新增 imports：`evalpersist`、`evalapp`、`observation`、`pipeline`（`constants`/`context` 已有）。`ctx` 用 `buildEvaluation` 内已有变量。

- [ ] **Step 2: 实现 agent emitter 注入（`api/wiring/agent.go`）**

在 agent service 装配完成处追加（best-effort：NATS 可用才注入，失败仅日志——agent 永不因观测阻断）：

```go
 // 运行态观测引用事件发布（best-effort，无 NATS 时跳过——agent 不阻断）。
 if c.Storage.NATS != nil {
  jsm, err := pipeline.NewJetStreamManager(c.Storage.NATS, c.Logger)
  if err != nil {
   c.Logger.Warn("agent observation emitter: jetstream manager unavailable", zap.Error(err))
  } else {
   c.Agent.Service.SetObservationEmitter(&observationEmitterAdapter{
    js: jsm.JS(), logger: c.Logger,
   })
  }
 }
```

> 注：`observationEmitterAdapter` 的 `js` 字段类型是 `jetstream.JetStream`（Task 6 定义）。`agent.go` 若缺 `pipeline`/`zap` import 则补。agent service 构造若返回 error 需处理。发布路径不因观测失败阻断执行（铁律）。

- [ ] **Step 3: 路由注入 + 契约测试（`api/http/router.go` + `api/http/contract_test.go`）**

`router.go` 的 `registerEvaluations`（第 165 行）在 handler 构建 setter 链（第 169 行 `handler.NewEvaluationHandler(...)` 起）追加观测服务，并在 `evaluations` group 注册两条路由：

```go
 h := handler.NewEvaluationHandler(...) // 存量 9 参数构造
 h = h.
  WithBaselineService(...). // ... 存量 setter 链保持
  WithObservationService(c.Evaluation.ObservationService)

 evaluations.GET("/observations", h.ListObservations)
 evaluations.GET("/observations/:id", h.GetObservation)
```

> 注：`registerEvaluations` 顶部已有 `c.Evaluation == nil || c.Evaluation.SuiteService == nil || ...` 早退守卫；观测路由加在守卫之后、`evaluations` group 内。`c.Evaluation.ObservationService` 为 nil 的兜底由 handler 内 nil 检查（503）保证——fail closed。`evaluations` group 已带 `RequireTenantRole("member")`，观测为租户自有运行数据，member 可读。

`contract_test.go`：在 `contractProviderRepo` 等 stub 旁新增观测 stub（`contractObservationService` 实现 `ListObservations`/`GetObservation`，仿现有 stub 返回固定最小对象），装配到 `wiring.Evaluation.ObservationService`；并为两个新端点添加契约用例。`api/http/testdata/contracts/` 新增 `evaluations.observations.get.golden.json` 与 `evaluations.observations.id.get.golden.json`，内容以现有 golden 文件格式为准（最小观测对象：`{"id":"obs-1","trace_id":"trace-1","resource":{"kind":"agent","resource_id":"agent-1"},"stratum":"","verdict":"pass","created_at":"..."}`）。

Run: `go test ./api/http/ -run Contract -v`
Expected: PASS（新 golden 通过，存量 golden 不回归）。

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 编译成功（wiring 与 handler 全部连通）。

- [ ] **Step 5: 运行相关包测试**

Run: `go test ./api/wiring/ ./api/http/... ./internal/evaluation/... ./internal/agent/application/ -short -timeout 120s`
Expected: PASS（存量测试不回归；`agent_test.go` 若构造 AgentService 受影响，补 `SetObservationEmitter` 或保持 nil 亦可）。

- [ ] **Step 6: 提交**

```bash
git add api/wiring/evaluation.go api/wiring/agent.go \
        api/http/router.go api/http/contract_test.go api/http/testdata/contracts/
git commit -m "feat(evaluation): 运行态观测链路 wiring 装配+路由+契约

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 验收（全部 Task 完成后执行）

```bash
# 1. 质量门禁
bash scripts/quality/risk-regression-guard.sh --explain
make code-quality

# 2. 后端快速验证
go vet ./...
go test -short ./...

# 3. 契约守护
go test ./api/http/ -run Contract -v

# 4. 依赖服务可用时端到端冒烟（make infra-up 起 NATS/PG）
#    打开观测开关：PUT /admin/parameters 设置 evaluation.observe.enabled=true
#    → 触发一次 agent 执行 → 等待消费 → GET /api/evaluations/observations 应有明细
#    验证指标：curl localhost:9090/metrics | grep eval_observation_total

# 5. 完整测试（PR 前，须经 stratum-e2e-development skill 系统验收）
make test-verify-before-pr
```

## 自审记录

**Spec 覆盖**：§4.3 EvalObservation（Task 1）、§2.2 NATS 引用事件 + Opik 证据（Task 2/5/6/10）、§4.2 judge 采样（Task 7/9）、§14 证据失败重投 + judge 降级（Task 9/10）、§11.1/11.2 指标（Task 4/10）、§10.1 健康分数据源查询 API（Task 11）、§3.1 judge 三维度（Task 9）。**范围外（后续计划）**：规则护栏/行为信号/判异（P1b）、两条产线（P1c）、前端视图（P1d）、stratum tier 与平台版本锚点（P1b/P2）。

**占位符扫描**：无 "TBD/TODO/implement later"；所有代码步骤含完整实现。`// TODO(P1b)`/`// TODO(P2)` 为规格授权的明确降级标注，非占位符。

**类型一致性**：`ObservationEvent`（agent port）与 `ObservationReferenceEvent`（evaluation domain）JSON 契约由 Task 2 golden 双向守护；`sampleDecision`、`ObservationService.Process`、`ObservationRepository` 三接口签名在 Task 7/8/9 定义并在 Task 9/10/11/12 消费，命名与参数逐一对齐。消费链路 API 全部锁定为 `jetstream` 包（`jetstream.Consumer`/`jetstream.Msg`/`jetstream.FetchMaxWait`/`jetstream.MsgMetadata`），与 memory pipeline 对齐（Task 10）；worker 构造 7 参数（consumer + dlqPublisher + processor + metrics + logger + ackWait + maxDeliver），wiring 用 `jsm.CreateConsumer` + `observation.EnsureStreams`（Task 12）。handler 侧统一 `tenantIDFromCtx`/`respondMissingTenant`/`middleware.NewHTTPError`，分页内联（无 `normalizePage`），`NewEvaluationHandler` 9 参数 + `h.observations` 字段（Task 11）。judge 输出为二进制 `AssertionResult{Passed, Message}`，applyJudge 映射 score 1.0/0.0 + Confidence 1.0（Task 9）。
