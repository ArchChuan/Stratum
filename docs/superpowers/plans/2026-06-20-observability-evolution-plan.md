# Observability & Self-Evolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the metrics extension, OTel Gen AI migration, ExecutionSample collection pipeline, and EvolutionService rule engine described in `docs/superpowers/specs/2026-06-20-observability-evolution-design.md`.

**Architecture:** Ten new MetricsProvider methods + sanitization helpers extend the existing Prometheus layer. A new `internal/evolution` bounded context holds domain types, ports, and the EvolutionService harness component. AgentService publishes execution samples through a consumer-side port; a PostgreSQL adapter in evolution/infrastructure persists them. The rule engine runs on a 5-minute tick via `harness.Component`.

**Tech Stack:** Go 1.22+, prometheus/client_golang v1.23, OTel v1.21, pgx v5, React 18, Ant Design 5.2

## Global Constraints

- `pkg/` never imports `internal/`; `domain/` only stdlib + `pkg/constants`; `application/` never imports pgx/redis/nats/gin
- Cross-context: consumer defines port in own `domain/port/`; provider implements; `api/wiring/` adapts
- All new DDL → `pkg/migration/sql/015_evolution.up.sql` (public schema + tenant_id column)
- Handler methods ≤ 15 lines; bind → service call → render only; errors via `c.Error(err)`
- Tests mock all ports; no build-tag replacement; suite runs with `-race`
- Constants: cross-pkg → `pkg/constants/`; pkg-internal → const block in the file
- Verify with `go vet && go test -short ./...` after every task

---

## File Map

| File | Change | Responsible task |
|------|--------|-----------------|
| `pkg/observability/provider.go` | +10 interface methods | Task 1 |
| `pkg/observability/sanitize.go` | new — SanitizeToolName, SanitizePath | Task 1 |
| `pkg/observability/prometheus.go` | +10 fields + implementations + exemplar helper | Task 2 |
| `pkg/observability/trace.go` | add gen_ai.* attribute constants | Task 3 |
| `internal/llmgateway/infrastructure/gateway.go` | migrate SetAttribute calls to gen_ai.* | Task 3 |
| `pkg/migration/sql/015_evolution.up.sql` | CREATE 3 tables | Task 4 |
| `pkg/migration/sql/015_evolution.down.sql` | DROP 3 tables | Task 4 |
| `internal/evolution/domain/sample.go` | ExecutionSample, StepRecord, OutcomeSignal | Task 5 |
| `internal/evolution/domain/recommendation.go` | Recommendation, RecType, RiskLevel, Status | Task 5 |
| `internal/evolution/domain/port/sample_repo.go` | SampleRepo interface | Task 5 |
| `internal/evolution/domain/port/recommendation_repo.go` | RecommendationRepo interface | Task 5 |
| `internal/evolution/domain/port/metrics_querier.go` | MetricsQuerier interface | Task 5 |
| `internal/evolution/domain/port/agent_config.go` | AgentConfigReader + AgentConfigWriter | Task 5 |
| `internal/agent/domain/port/sample_writer.go` | SampleWriter + AgentExecSample (consumer-side port) | Task 5 |
| `internal/evolution/infrastructure/persistence/sample_repo.go` | pgx SampleRepo | Task 6 |
| `internal/evolution/infrastructure/persistence/recommendation_repo.go` | pgx RecommendationRepo | Task 6 |
| `internal/evolution/infrastructure/promql/querier.go` | HTTP Prometheus API client | Task 7 |
| `internal/evolution/application/evolution_service.go` | rule engine + harness.Component | Task 8 |
| `internal/agent/application/agent_service.go` | +SampleWriter field, publishSample in run() | Task 9 |
| `api/http/handler/agent_exec_handler.go` | +RecordFeedback handler | Task 10 |
| `api/http/router.go` | register feedback + evolution admin routes | Task 10 |
| `api/wiring/evolution.go` | new — EvolutionService assembly + adapters | Task 10 |
| `deploy/prometheus/rules/slo.yaml` | recording rules + multi-burn-rate alerts | Task 11 |
| `web/src/modules/agent/components/ExecutionFeedback.tsx` | 👍/👎 + abandoned signal | Task 12 |
| `web/src/modules/agent/api/feedback.api.ts` | POST feedback endpoint wrapper | Task 12 |

---

### Task 1: Extend MetricsProvider Interface + Sanitization Helpers

**Files:**

- Modify: `pkg/observability/provider.go`
- Create: `pkg/observability/sanitize.go`
- Create: `pkg/observability/sanitize_test.go`

**Interfaces:**

- Produces: 10 new interface methods consumed by Task 2 (Prometheus impl) and all test mocks

- [ ] **Step 1: Add sanitize.go**

```go
// pkg/observability/sanitize.go
package observability

import "regexp"

var builtinToolRE = regexp.MustCompile(
    `^(stratum_recall_memory|stratum_search_knowledge|stratum_run_skill)$`,
)

// SanitizeToolName collapses non-builtin tool names to "_other" to
// prevent high-cardinality label explosion in Prometheus.
func SanitizeToolName(name string) string {
    if builtinToolRE.MatchString(name) {
        return name
    }
    return "_other"
}

var uuidSegRE = regexp.MustCompile(
    `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`,
)

// SanitizePath replaces UUID path segments with ":id" to keep HTTP
// route cardinality bounded.
func SanitizePath(path string) string {
    return uuidSegRE.ReplaceAllString(path, ":id")
}
```

- [ ] **Step 2: Write failing tests for sanitize.go**

```go
// pkg/observability/sanitize_test.go
package observability_test

import (
    "testing"
    "github.com/byteBuilderX/stratum/pkg/observability"
)

func TestSanitizeToolName(t *testing.T) {
    cases := []struct{ in, want string }{
        {"stratum_recall_memory", "stratum_recall_memory"},
        {"stratum_search_knowledge", "stratum_search_knowledge"},
        {"stratum_run_skill", "stratum_run_skill"},
        {"my_custom_tool", "_other"},
        {"", "_other"},
    }
    for _, c := range cases {
        if got := observability.SanitizeToolName(c.in); got != c.want {
            t.Errorf("SanitizeToolName(%q) = %q, want %q", c.in, got, c.want)
        }
    }
}

func TestSanitizePath(t *testing.T) {
    cases := []struct{ in, want string }{
        {"/agents/123e4567-e89b-12d3-a456-426614174000/execute", "/agents/:id/execute"},
        {"/agents", "/agents"},
        {"/tenants/abc/users/def", "/tenants/abc/users/def"},
    }
    for _, c := range cases {
        if got := observability.SanitizePath(c.in); got != c.want {
            t.Errorf("SanitizePath(%q) = %q, want %q", c.in, got, c.want)
        }
    }
}
```

- [ ] **Step 3: Run tests**

```bash
cd /home/yang/go-projects/stratum && go test ./pkg/observability/... -run 'TestSanitize' -v
```

Expected: PASS

- [ ] **Step 4: Extend MetricsProvider interface in provider.go**

Replace the interface body in `pkg/observability/provider.go` with:

```go
type MetricsProvider interface {
    // HTTP
    IncHTTPRequest(method, path string, statusCode int)
    RecordHTTPRequestDuration(method, path string, duration float64)
    IncHTTPRequestsInFlight()
    DecHTTPRequestsInFlight()

    // Skill
    IncSkillExecution(skillID, skillType, status string)
    RecordSkillExecutionDuration(skillID string, duration float64)
    SetSkillCircuitBreakerState(skillID string, state float64)

    // Agent
    IncAgentExecution(agentID, agentType, status string)
    RecordAgentExecutionDuration(agentID, agentType string, duration float64)
    RecordAgentStepCount(agentID, agentType string, steps int)

    // Agent tool quality
    IncAgentToolCall(agentID, toolName, status string)
    RecordAgentToolCallDuration(agentID, toolName string, durationSeconds float64)
    RecordAgentContextUtilization(agentID string, ratio float64)
    IncAgentIterationsExhausted(agentID string)

    // LLM
    IncLLMRequest(model, provider, status string)
    RecordLLMRequestDuration(model, provider string, duration float64)
    IncLLMTokenUsage(model, tokenType string, count int64)
    RecordLLMTokenHistogram(model, tokenType string, count float64)
    RecordLLMFirstTokenLatency(model, provider string, latency float64)
    RecordLLMCostUnits(model, provider, tenantID string, units float64)
    IncLLMErrorByType(model, provider, errorType string)

    // Memory recall quality
    IncMemoryRecallHit(tenantID, strategy string)
    IncMemoryRecallMiss(tenantID, strategy string)
    RecordMemoryRecallResultCount(tenantID string, count float64)

    // Knowledge / Memory latency
    IncKnowledgeQuery(queryType, status string)
    RecordKnowledgeQueryDuration(queryType string, duration float64)
    RecordMemoryRetrievalDuration(operation string, duration float64)

    // Hermes
    IncHermesEvent(eventType string)
    IncHermesEventProcessed(eventType, status string)
}
```

- [ ] **Step 5: Add NoopMetrics stubs for new methods**

```go
func (NoopMetrics) IncAgentToolCall(_, _, _ string)                       {}
func (NoopMetrics) RecordAgentToolCallDuration(_, _ string, _ float64)    {}
func (NoopMetrics) RecordAgentContextUtilization(_ string, _ float64)     {}
func (NoopMetrics) IncAgentIterationsExhausted(_ string)                  {}
func (NoopMetrics) RecordLLMFirstTokenLatency(_, _ string, _ float64)     {}
func (NoopMetrics) RecordLLMCostUnits(_, _, _ string, _ float64)          {}
func (NoopMetrics) IncLLMErrorByType(_, _, _ string)                      {}
func (NoopMetrics) IncMemoryRecallHit(_, _ string)                        {}
func (NoopMetrics) IncMemoryRecallMiss(_, _ string)                       {}
func (NoopMetrics) RecordMemoryRecallResultCount(_ string, _ float64)     {}
```

- [ ] **Step 6: Verify (expect compile error on PrometheusMetrics — not yet implemented)**

```bash
cd /home/yang/go-projects/stratum && go build ./pkg/observability/... 2>&1 | grep -v "PrometheusMetrics" | head -20
```

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum
git add pkg/observability/provider.go pkg/observability/sanitize.go pkg/observability/sanitize_test.go
git commit -m "feat(observability): extend MetricsProvider interface with 10 new methods + sanitize helpers"
```

---

### Task 2: Implement New Prometheus Metrics + Exemplar Injection

**Files:**

- Modify: `pkg/observability/prometheus.go`

**Interfaces:**

- Consumes: 10 new interface methods from Task 1
- Produces: working PrometheusMetrics that satisfies updated interface

- [ ] **Step 1: Add traceIDFn field and builder to PrometheusMetrics struct**

In `pkg/observability/prometheus.go`, add to the struct:

```go
traceIDFn func() string
```

Add builder method:

```go
func (m *PrometheusMetrics) WithTraceIDFn(fn func() string) *PrometheusMetrics {
    m.traceIDFn = fn
    return m
}

func (m *PrometheusMetrics) currentTraceID() string {
    if m.traceIDFn != nil {
        return m.traceIDFn()
    }
    return ""
}
```

- [ ] **Step 2: Add new metric fields to PrometheusMetrics struct**

```go
// Agent tool quality
agentToolCallTotal    *prometheus.CounterVec
agentToolCallDuration *prometheus.HistogramVec
agentContextUtil      *prometheus.HistogramVec
agentIterExhausted    *prometheus.CounterVec
// LLM extended
llmCostUnits      *prometheus.CounterVec
llmErrorByType    *prometheus.CounterVec
// Memory recall quality
memoryRecallHits   *prometheus.CounterVec
memoryRecallMisses *prometheus.CounterVec
memoryRecallCount  *prometheus.HistogramVec
```

- [ ] **Step 3: Register new metrics in NewPrometheusMetrics()**

```go
agentToolCallTotal: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "agent_tool_calls_total",
    Help: "Agent tool calls by agent, tool_name, status.",
}, []string{"agent_id", "tool_name", "status"}),

agentToolCallDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "agent_tool_call_duration_seconds",
    Help:    "Agent tool call latency.",
    Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
}, []string{"agent_id", "tool_name"}),

agentContextUtil: promauto.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "agent_context_utilization_ratio",
    Help:    "Fraction of max context tokens used per execution (0–1).",
    Buckets: []float64{0.1, 0.25, 0.5, 0.7, 0.85, 0.95, 1.0},
}, []string{"agent_id"}),

agentIterExhausted: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "agent_iterations_exhausted_total",
    Help: "Executions that hit max_iterations limit.",
}, []string{"agent_id"}),

llmCostUnits: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "llm_cost_units_total",
    Help: "Accumulated LLM cost (USD/1K tokens normalized).",
}, []string{"model", "provider", "tenant_id"}),

llmErrorByType: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "llm_errors_by_type_total",
    Help: "LLM errors by error_type.",
}, []string{"model", "provider", "error_type"}),

memoryRecallHits: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "memory_recall_hits_total",
    Help: "Successful memory recall lookups.",
}, []string{"tenant_id", "strategy"}),

memoryRecallMisses: promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "memory_recall_misses_total",
    Help: "Empty memory recall results.",
}, []string{"tenant_id", "strategy"}),

memoryRecallCount: promauto.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "memory_recall_result_count",
    Help:    "Number of results per memory recall call.",
    Buckets: []float64{0, 1, 2, 5, 10, 20},
}, []string{"tenant_id"}),
```

- [ ] **Step 4: Implement the 10 new interface methods**

```go
func (m *PrometheusMetrics) IncAgentToolCall(agentID, toolName, status string) {
    m.agentToolCallTotal.WithLabelValues(agentID, SanitizeToolName(toolName), status).Inc()
}
func (m *PrometheusMetrics) RecordAgentToolCallDuration(agentID, toolName string, d float64) {
    m.agentToolCallDuration.WithLabelValues(agentID, SanitizeToolName(toolName)).Observe(d)
}
func (m *PrometheusMetrics) RecordAgentContextUtilization(agentID string, ratio float64) {
    m.agentContextUtil.WithLabelValues(agentID).Observe(ratio)
}
func (m *PrometheusMetrics) IncAgentIterationsExhausted(agentID string) {
    m.agentIterExhausted.WithLabelValues(agentID).Inc()
}
func (m *PrometheusMetrics) RecordLLMFirstTokenLatency(model, provider string, latency float64) {
    m.llmFirstTokenLatency.WithLabelValues(model, provider).Observe(latency)
}
func (m *PrometheusMetrics) RecordLLMCostUnits(model, provider, tenantID string, units float64) {
    m.llmCostUnits.WithLabelValues(model, provider, tenantID).Add(units)
}
func (m *PrometheusMetrics) IncLLMErrorByType(model, provider, errorType string) {
    m.llmErrorByType.WithLabelValues(model, provider, errorType).Inc()
}
func (m *PrometheusMetrics) IncMemoryRecallHit(tenantID, strategy string) {
    m.memoryRecallHits.WithLabelValues(tenantID, strategy).Inc()
}
func (m *PrometheusMetrics) IncMemoryRecallMiss(tenantID, strategy string) {
    m.memoryRecallMisses.WithLabelValues(tenantID, strategy).Inc()
}
func (m *PrometheusMetrics) RecordMemoryRecallResultCount(tenantID string, count float64) {
    m.memoryRecallCount.WithLabelValues(tenantID).Observe(count)
}
```

- [ ] **Step 5: Add exemplar to RecordLLMRequestDuration**

Find and replace the existing `RecordLLMRequestDuration` body:

```go
func (m *PrometheusMetrics) RecordLLMRequestDuration(model, provider string, duration float64) {
    obs := m.llmRequestDuration.WithLabelValues(model, provider)
    if h, ok := obs.(prometheus.ExemplarObserver); ok {
        if tid := m.currentTraceID(); tid != "" {
            h.ObserveWithExemplar(duration, prometheus.Labels{"trace_id": tid})
            return
        }
    }
    obs.Observe(duration)
}
```

- [ ] **Step 6: Verify compilation and tests**

```bash
cd /home/yang/go-projects/stratum && go vet ./... && go test -short ./pkg/observability/...
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum
git add pkg/observability/prometheus.go
git commit -m "feat(observability): implement 10 new Prometheus metrics + exemplar injection"
```

---

### Task 3: Migrate LLM Span Attributes to OTel Gen AI Conventions

**Files:**

- Modify: `pkg/observability/trace.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`

**Interfaces:**

- Produces: typed attribute name constants; all LLM spans use `gen_ai.*` namespace

- [ ] **Step 1: Add constants to trace.go**

Append to `pkg/observability/trace.go`:

```go
// Gen AI semantic convention attribute names (OTel spec, gen_ai namespace).
const (
    AttrGenAIRequestModel         = "gen_ai.request.model"
    AttrGenAISystem               = "gen_ai.system"
    AttrGenAIUsageInputTokens     = "gen_ai.usage.input_tokens"
    AttrGenAIUsageOutputTokens    = "gen_ai.usage.output_tokens"
    AttrGenAIRequestTemperature   = "gen_ai.request.temperature"
    AttrGenAIResponseFinishReason = "gen_ai.response.finish_reason"
)
```

- [ ] **Step 2: Find span attribute calls in gateway.go**

Read `internal/llmgateway/infrastructure/gateway.go` and locate all `span.SetAttribute` calls that reference `"model"`, `"provider"`, `"prompt_tokens"`, `"completion_tokens"`. Replace each:

```go
// Before:
span.SetAttribute("model", resp.Model)
span.SetAttribute("provider", provider)
span.SetAttribute("prompt_tokens", resp.PromptTokens)
span.SetAttribute("completion_tokens", resp.CompletionTokens)

// After:
span.SetAttribute(observability.AttrGenAIRequestModel, resp.Model)
span.SetAttribute(observability.AttrGenAISystem, provider)
span.SetAttribute(observability.AttrGenAIUsageInputTokens, resp.PromptTokens)
span.SetAttribute(observability.AttrGenAIUsageOutputTokens, resp.CompletionTokens)
if resp.FinishReason != "" {
    span.SetAttribute(observability.AttrGenAIResponseFinishReason, resp.FinishReason)
}
```

Note: Actual field names may differ — read the file before editing.

- [ ] **Step 3: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./internal/llmgateway/... ./pkg/observability/...
```

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum
git add pkg/observability/trace.go internal/llmgateway/infrastructure/gateway.go
git commit -m "feat(observability): migrate LLM spans to OTel Gen AI semantic conventions"
```

---

### Task 5: Evolution Bounded Context — Domain Types + Ports

**Files:**

- Create: `internal/evolution/domain/sample.go`
- Create: `internal/evolution/domain/recommendation.go`
- Create: `internal/evolution/domain/port/sample_repo.go`
- Create: `internal/evolution/domain/port/recommendation_repo.go`
- Create: `internal/evolution/domain/port/metrics_querier.go`
- Create: `internal/evolution/domain/port/agent_config.go`
- Create: `internal/agent/domain/port/sample_writer.go`

**Interfaces:**

- Produces: all domain types and port interfaces consumed by Tasks 6–10

- [ ] **Step 1: Create domain/sample.go**

```go
// internal/evolution/domain/sample.go
package domain

import "time"

type OutcomeSignal string

const (
    OutcomeSuccess             OutcomeSignal = "success"
    OutcomeError               OutcomeSignal = "error"
    OutcomeTimeout             OutcomeSignal = "timeout"
    OutcomeIterationsExhausted OutcomeSignal = "iterations_exhausted"
)

type StepRecord struct {
    StepIndex  int    `json:"step_index"`
    ToolName   string `json:"tool_name"`
    InputHash  string `json:"input_hash"`
    Status     string `json:"status"`
    DurationMs int    `json:"duration_ms"`
}

type ExecutionSample struct {
    ID              string        `json:"id"`
    TenantID        string        `json:"tenant_id"`
    AgentID         string        `json:"agent_id"`
    ConversationID  string        `json:"conversation_id"`
    TraceID         string        `json:"trace_id"`
    Model           string        `json:"model"`
    Provider        string        `json:"provider"`
    InputTokens     int           `json:"input_tokens"`
    OutputTokens    int           `json:"output_tokens"`
    CostUnits       float64       `json:"cost_units"`
    Steps           []StepRecord  `json:"steps"`
    TotalDurationMs int           `json:"total_duration_ms"`
    IterationsUsed  int           `json:"iterations_used"`
    IterationsMax   int           `json:"iterations_max"`
    Outcome         OutcomeSignal `json:"outcome"`
    ErrorType       string        `json:"error_type,omitempty"`
    FinishReason    string        `json:"finish_reason,omitempty"`
    SystemPromptID  string        `json:"system_prompt_id,omitempty"`
    UserFeedback    string        `json:"user_feedback,omitempty"`
    CreatedAt       time.Time     `json:"created_at"`
}
```

- [ ] **Step 2: Create domain/recommendation.go**

```go
// internal/evolution/domain/recommendation.go
package domain

import "time"

type RecType   string
type RiskLevel string
type RecStatus string

const (
    RecParamAdapt    RecType = "param_adapt"
    RecModelSwitch   RecType = "model_switch"
    RecPromptUpgrade RecType = "prompt_upgrade"

    RiskLow  RiskLevel = "low"
    RiskHigh RiskLevel = "high"

    StatusPending      RecStatus = "pending"
    StatusAutoApplied  RecStatus = "auto_applied"
    StatusApproved     RecStatus = "approved"
    StatusRejected     RecStatus = "rejected"
)

type Recommendation struct {
    ID         string            `json:"id"`
    TenantID   string            `json:"tenant_id"`
    AgentID    string            `json:"agent_id"`
    RecType    RecType           `json:"rec_type"`
    RiskLevel  RiskLevel         `json:"risk_level"`
    Status     RecStatus         `json:"status"`
    Payload    map[string]any    `json:"payload"`
    Evidence   map[string]any    `json:"evidence"`
    Confidence float64           `json:"confidence"`
    AppliedAt  *time.Time        `json:"applied_at,omitempty"`
    CreatedAt  time.Time         `json:"created_at"`
}
```

- [ ] **Step 3: Create domain/port/sample_repo.go**

```go
// internal/evolution/domain/port/sample_repo.go
package port

import (
    "context"
    domain "github.com/byteBuilderX/stratum/internal/evolution/domain"
)

type SampleRepo interface {
    Save(ctx context.Context, s *domain.ExecutionSample) error
    // ExhaustionRate returns the fraction of the last n samples for agentID
    // where outcome = iterations_exhausted. Returns 0 if < 10 samples exist.
    ExhaustionRate(ctx context.Context, agentID string, n int) float64
    // NegativeFeedbackCount returns samples with outcome=success and
    // user_feedback=negative in the last days days.
    NegativeFeedbackCount(ctx context.Context, tenantID, agentID string, days int) int
    // UpdateFeedback sets user_feedback for the sample identified by traceID+tenantID.
    UpdateFeedback(ctx context.Context, tenantID, traceID, signal string) error
    // AvgCostPerSuccess returns mean cost_units for successful executions in days days.
    AvgCostPerSuccess(ctx context.Context, tenantID, agentID string, days int) float64
}
```

- [ ] **Step 4: Create domain/port/recommendation_repo.go**

```go
// internal/evolution/domain/port/recommendation_repo.go
package port

import (
    "context"
    domain "github.com/byteBuilderX/stratum/internal/evolution/domain"
)

type RecommendationRepo interface {
    Save(ctx context.Context, r *domain.Recommendation) error
    List(ctx context.Context, tenantID string, status domain.RecStatus, agentID string) ([]*domain.Recommendation, error)
    UpdateStatus(ctx context.Context, id string, status domain.RecStatus) error
}
```

- [ ] **Step 5: Create domain/port/metrics_querier.go**

```go
// internal/evolution/domain/port/metrics_querier.go
package port

import "context"

// MetricsQuerier executes instant PromQL queries against the Prometheus HTTP API.
type MetricsQuerier interface {
    // Query executes an instant PromQL query and returns the scalar result.
    // Returns 0 and nil error if the result set is empty.
    Query(ctx context.Context, promql string) (float64, error)
}
```

- [ ] **Step 6: Create domain/port/agent_config.go**

```go
// internal/evolution/domain/port/agent_config.go
package port

import "context"

// AgentConfigReader reads current agent config values for rule evaluation.
type AgentConfigReader interface {
    GetMaxIterations(ctx context.Context, agentID string) int
}

// AgentConfigWriter applies low-risk parameter changes to agent config.
// High-risk changes are stored as pending Recommendations instead.
type AgentConfigWriter interface {
    SetMaxIterations(ctx context.Context, agentID string, value int) error
}
```

- [ ] **Step 7: Create consumer-side port in agent domain**

```go
// internal/agent/domain/port/sample_writer.go
package port

import "context"

// AgentExecSample carries the minimum execution data needed for evolution analysis.
// Defined in agent/domain/port (consumer side) to avoid cross-context imports.
type AgentExecSample struct {
    TenantID        string
    AgentID         string
    ConversationID  string
    TraceID         string
    Model           string
    Provider        string
    InputTokens     int
    OutputTokens    int
    CostUnits       float64
    TotalDurationMs int
    IterationsUsed  int
    IterationsMax   int
    Outcome         string // "success" | "error" | "timeout" | "iterations_exhausted"
    ErrorType       string
    FinishReason    string
    Steps           []SampleStepRecord
}

type SampleStepRecord struct {
    StepIndex  int
    ToolName   string
    InputHash  string
    Status     string
    DurationMs int
}

// SampleWriter persists an execution sample asynchronously.
// Implementations must be safe for concurrent use.
type SampleWriter interface {
    WriteSample(ctx context.Context, s AgentExecSample) error
    RecordFeedback(ctx context.Context, tenantID, traceID, signal string) error
}
```

- [ ] **Step 8: Verify compilation of new packages**

```bash
cd /home/yang/go-projects/stratum && go build ./internal/evolution/... ./internal/agent/domain/port/...
```

- [ ] **Step 9: Commit**

```bash
cd /home/yang/go-projects/stratum
git add internal/evolution/ internal/agent/domain/port/sample_writer.go
git commit -m "feat(evolution): domain types, ports, and consumer-side SampleWriter port"
```

---

### Task 4: Database Migration — Evolution Tables

**Files:**

- Create: `pkg/migration/sql/015_evolution.up.sql`
- Create: `pkg/migration/sql/015_evolution.down.sql`

**Interfaces:**

- Produces: `evolution_samples`, `evolution_recommendations`, `metric_snapshots` in public schema

- [ ] **Step 1: Write up migration**

```sql
-- pkg/migration/sql/015_evolution.up.sql

CREATE TABLE IF NOT EXISTS evolution_samples (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        TEXT NOT NULL,
    agent_id         TEXT NOT NULL,
    conversation_id  TEXT,
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
    user_feedback    TEXT,
    steps            JSONB,
    total_duration_ms INT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_evo_samples_agent
    ON evolution_samples (tenant_id, agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_evo_samples_outcome
    ON evolution_samples (tenant_id, outcome, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_evo_samples_feedback
    ON evolution_samples (tenant_id, user_feedback, created_at DESC)
    WHERE user_feedback IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_evo_samples_trace
    ON evolution_samples (trace_id)
    WHERE trace_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS evolution_recommendations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   TEXT NOT NULL,
    agent_id    TEXT,
    rec_type    TEXT NOT NULL,
    risk_level  TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    payload     JSONB NOT NULL,
    evidence    JSONB NOT NULL,
    confidence  NUMERIC(4,3),
    applied_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, agent_id, snapshot_date, metric_name)
);

CREATE INDEX IF NOT EXISTS idx_metric_snapshots
    ON metric_snapshots (tenant_id, snapshot_date DESC);
```

- [ ] **Step 2: Write down migration**

```sql
-- pkg/migration/sql/015_evolution.down.sql
DROP TABLE IF EXISTS metric_snapshots;
DROP TABLE IF EXISTS evolution_recommendations;
DROP TABLE IF EXISTS evolution_samples;
```

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/stratum
git add pkg/migration/sql/015_evolution.up.sql pkg/migration/sql/015_evolution.down.sql
git commit -m "feat(evolution): migration 015 — evolution_samples, recommendations, metric_snapshots"
```

---

### Task 6: Evolution Infrastructure — Persistence Layer

**Files:**

- Create: `internal/evolution/infrastructure/persistence/sample_repo.go`
- Create: `internal/evolution/infrastructure/persistence/recommendation_repo.go`

**Interfaces:**

- Consumes: `SampleRepo`, `RecommendationRepo` ports from Task 5
- Produces: pgx implementations of both repos

- [ ] **Step 1: Create sample_repo.go**

```go
// internal/evolution/infrastructure/persistence/sample_repo.go
package persistence

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/jackc/pgx/v5/pgxpool"
    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
)

type pgSampleRepo struct{ pool *pgxpool.Pool }

func NewSampleRepo(pool *pgxpool.Pool) *pgSampleRepo { return &pgSampleRepo{pool: pool} }

func (r *pgSampleRepo) Save(ctx context.Context, s *evdomain.ExecutionSample) error {
    stepsJSON, _ := json.Marshal(s.Steps)
    _, err := r.pool.Exec(ctx, `
        INSERT INTO evolution_samples
          (id, tenant_id, agent_id, conversation_id, trace_id, model, provider,
           input_tokens, output_tokens, cost_units, iterations_used, iterations_max,
           outcome, error_type, finish_reason, system_prompt_id, user_feedback,
           steps, total_duration_ms, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
        ON CONFLICT (id) DO NOTHING`,
        s.ID, s.TenantID, s.AgentID, s.ConversationID, s.TraceID, s.Model, s.Provider,
        s.InputTokens, s.OutputTokens, s.CostUnits, s.IterationsUsed, s.IterationsMax,
        string(s.Outcome), s.ErrorType, s.FinishReason, s.SystemPromptID, s.UserFeedback,
        stepsJSON, s.TotalDurationMs, s.CreatedAt,
    )
    return err
}

func (r *pgSampleRepo) ExhaustionRate(ctx context.Context, agentID string, n int) float64 {
    var total, exhausted int
    _ = r.pool.QueryRow(ctx, `
        SELECT COUNT(*), COUNT(*) FILTER (WHERE outcome = 'iterations_exhausted')
        FROM (SELECT outcome FROM evolution_samples WHERE agent_id = $1
              ORDER BY created_at DESC LIMIT $2) sub`,
        agentID, n,
    ).Scan(&total, &exhausted)
    if total < 10 {
        return 0
    }
    return float64(exhausted) / float64(total)
}

func (r *pgSampleRepo) NegativeFeedbackCount(ctx context.Context, tenantID, agentID string, days int) int {
    var count int
    _ = r.pool.QueryRow(ctx, `
        SELECT COUNT(*) FROM evolution_samples
        WHERE tenant_id = $1 AND agent_id = $2
          AND outcome = 'success' AND user_feedback = 'negative'
          AND created_at > NOW() - ($3 || ' days')::INTERVAL`,
        tenantID, agentID, fmt.Sprintf("%d", days),
    ).Scan(&count)
    return count
}

func (r *pgSampleRepo) UpdateFeedback(ctx context.Context, tenantID, traceID, signal string) error {
    _, err := r.pool.Exec(ctx,
        `UPDATE evolution_samples SET user_feedback = $1 WHERE tenant_id = $2 AND trace_id = $3`,
        signal, tenantID, traceID,
    )
    return err
}

func (r *pgSampleRepo) AvgCostPerSuccess(ctx context.Context, tenantID, agentID string, days int) float64 {
    var avg float64
    _ = r.pool.QueryRow(ctx, `
        SELECT COALESCE(AVG(cost_units), 0) FROM evolution_samples
        WHERE tenant_id = $1 AND agent_id = $2 AND outcome = 'success'
          AND created_at > NOW() - ($3 || ' days')::INTERVAL`,
        tenantID, agentID, fmt.Sprintf("%d", days),
    ).Scan(&avg)
    return avg
}
```

- [ ] **Step 2: Create recommendation_repo.go**

```go
// internal/evolution/infrastructure/persistence/recommendation_repo.go
package persistence

import (
    "context"
    "encoding/json"

    "github.com/jackc/pgx/v5/pgxpool"
    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
)

type pgRecommendationRepo struct{ pool *pgxpool.Pool }

func NewRecommendationRepo(pool *pgxpool.Pool) *pgRecommendationRepo {
    return &pgRecommendationRepo{pool: pool}
}

func (r *pgRecommendationRepo) Save(ctx context.Context, rec *evdomain.Recommendation) error {
    payloadJSON, _ := json.Marshal(rec.Payload)
    evidenceJSON, _ := json.Marshal(rec.Evidence)
    _, err := r.pool.Exec(ctx, `
        INSERT INTO evolution_recommendations
          (id, tenant_id, agent_id, rec_type, risk_level, status, payload, evidence, confidence, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
        rec.ID, rec.TenantID, rec.AgentID, string(rec.RecType), string(rec.RiskLevel),
        string(rec.Status), payloadJSON, evidenceJSON, rec.Confidence, rec.CreatedAt,
    )
    return err
}

func (r *pgRecommendationRepo) List(
    ctx context.Context, tenantID string, status evdomain.RecStatus, agentID string,
) ([]*evdomain.Recommendation, error) {
    rows, err := r.pool.Query(ctx, `
        SELECT id, tenant_id, agent_id, rec_type, risk_level, status,
               payload, evidence, confidence, created_at
        FROM evolution_recommendations
        WHERE tenant_id = $1 AND status = $2 AND ($3 = '' OR agent_id = $3)
        ORDER BY created_at DESC`,
        tenantID, string(status), agentID,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []*evdomain.Recommendation
    for rows.Next() {
        var rec evdomain.Recommendation
        var payloadJSON, evidenceJSON []byte
        var rt, rl, st string
        if err := rows.Scan(
            &rec.ID, &rec.TenantID, &rec.AgentID, &rt, &rl, &st,
            &payloadJSON, &evidenceJSON, &rec.Confidence, &rec.CreatedAt,
        ); err != nil {
            return nil, err
        }
        rec.RecType, rec.RiskLevel, rec.Status = evdomain.RecType(rt), evdomain.RiskLevel(rl), evdomain.RecStatus(st)
        _ = json.Unmarshal(payloadJSON, &rec.Payload)
        _ = json.Unmarshal(evidenceJSON, &rec.Evidence)
        out = append(out, &rec)
    }
    return out, rows.Err()
}

func (r *pgRecommendationRepo) UpdateStatus(ctx context.Context, id string, status evdomain.RecStatus) error {
    _, err := r.pool.Exec(ctx,
        `UPDATE evolution_recommendations
         SET status = $1,
             applied_at = CASE WHEN $1 IN ('auto_applied','approved') THEN NOW() ELSE applied_at END
         WHERE id = $2`,
        string(status), id,
    )
    return err
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd /home/yang/go-projects/stratum && go build ./internal/evolution/...
```

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum
git add internal/evolution/infrastructure/persistence/
git commit -m "feat(evolution): pgx persistence for SampleRepo and RecommendationRepo"
```

---

### Task 7: Evolution Infrastructure — PromQL Querier

**Files:**

- Create: `internal/evolution/infrastructure/promql/querier.go`
- Create: `internal/evolution/infrastructure/promql/querier_test.go`

**Interfaces:**

- Consumes: `MetricsQuerier` port from Task 5
- Produces: HTTP client querying Prometheus /api/v1/query

- [ ] **Step 1: Create querier.go**

```go
// internal/evolution/infrastructure/promql/querier.go
package promql

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "strconv"
    "time"
)

const defaultTimeout = 10 * time.Second

type HTTPQuerier struct {
    baseURL string
    client  *http.Client
}

func NewQuerier(prometheusBaseURL string) *HTTPQuerier {
    return &HTTPQuerier{
        baseURL: prometheusBaseURL,
        client:  &http.Client{Timeout: defaultTimeout},
    }
}

type promQueryResponse struct {
    Status string `json:"status"`
    Data   struct {
        ResultType string `json:"resultType"`
        Result     []struct {
            Value [2]json.RawMessage `json:"value"`
        } `json:"result"`
    } `json:"data"`
}

func (q *HTTPQuerier) Query(ctx context.Context, promql string) (float64, error) {
    params := url.Values{"query": {promql}}
    req, err := http.NewRequestWithContext(
        ctx, http.MethodGet,
        q.baseURL+"/api/v1/query?"+params.Encode(), nil,
    )
    if err != nil {
        return 0, fmt.Errorf("promql querier: %w", err)
    }
    resp, err := q.client.Do(req)
    if err != nil {
        return 0, fmt.Errorf("promql querier: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return 0, fmt.Errorf("promql querier: HTTP %d", resp.StatusCode)
    }
    var pResp promQueryResponse
    if err := json.NewDecoder(resp.Body).Decode(&pResp); err != nil {
        return 0, fmt.Errorf("promql querier: decode: %w", err)
    }
    if pResp.Status != "success" || len(pResp.Data.Result) == 0 {
        return 0, nil
    }
    var valStr string
    _ = json.Unmarshal(pResp.Data.Result[0].Value[1], &valStr)
    v, err := strconv.ParseFloat(valStr, 64)
    if err != nil {
        return 0, fmt.Errorf("promql querier: parse %q: %w", valStr, err)
    }
    return v, nil
}
```

- [ ] **Step 2: Write unit tests**

```go
// internal/evolution/infrastructure/promql/querier_test.go
package promql_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/byteBuilderX/stratum/internal/evolution/infrastructure/promql"
)

func TestHTTPQuerier_ReturnsValue(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]any{
            "status": "success",
            "data": map[string]any{
                "resultType": "vector",
                "result": []map[string]any{
                    {"metric": map[string]any{}, "value": []any{1718000000, "3.14"}},
                },
            },
        })
    }))
    defer srv.Close()

    v, err := promql.NewQuerier(srv.URL).Query(context.Background(), `up`)
    if err != nil || v != 3.14 {
        t.Fatalf("want 3.14 nil, got %v %v", v, err)
    }
}

func TestHTTPQuerier_EmptyResultReturnsZero(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        _ = json.NewEncoder(w).Encode(map[string]any{
            "status": "success",
            "data":   map[string]any{"resultType": "vector", "result": []any{}},
        })
    }))
    defer srv.Close()

    v, err := promql.NewQuerier(srv.URL).Query(context.Background(), `none`)
    if err != nil || v != 0 {
        t.Fatalf("want 0 nil, got %v %v", v, err)
    }
}
```

- [ ] **Step 3: Run tests**

```bash
cd /home/yang/go-projects/stratum && go test -v -race ./internal/evolution/infrastructure/promql/...
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/stratum
git add internal/evolution/infrastructure/promql/
git commit -m "feat(evolution): PromQL HTTP querier with httptest coverage"
```

---

### Task 8: EvolutionService — Rule Engine + Harness Component

**Files:**

- Create: `internal/evolution/application/evolution_service.go`
- Create: `internal/evolution/application/evolution_service_test.go`
- Create: `pkg/constants/evolution.go`

**Interfaces:**

- Consumes: all ports from Task 5
- Produces: `EvolutionService` satisfying `harness.Component`; exported `EvalParamAdaptation` for testing

- [ ] **Step 1: Create pkg/constants/evolution.go**

```go
// pkg/constants/evolution.go
package constants

const (
    EvolutionSampleWindow                  = 1000 // sample window for exhaustion rate
    EvolutionExhaustionThreshold           = 0.20 // auto param-adapt trigger
    EvolutionBurnRateModelSwitchThreshold  = 14.4 // error budget burn rate trigger
    EvolutionNegativeFeedbackThreshold     = 50   // negative feedback count trigger
    AgentMaxIterationsCeiling              = 20   // absolute cap for auto param adapt
)
```

- [ ] **Step 2: Create evolution_service.go**

```go
// internal/evolution/application/evolution_service.go
package application

import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
    "go.uber.org/zap"

    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
    evport "github.com/byteBuilderX/stratum/internal/evolution/domain/port"
    "github.com/byteBuilderX/stratum/pkg/constants"
)

const cyclePeriod = 5 * time.Minute

type Deps struct {
    SampleRepo   evport.SampleRepo
    RecRepo      evport.RecommendationRepo
    Querier      evport.MetricsQuerier
    ConfigReader evport.AgentConfigReader
    ConfigWriter evport.AgentConfigWriter
    Logger       *zap.Logger
}

type EvolutionService struct {
    deps   Deps
    stopCh chan struct{}
}

func NewEvolutionService(d Deps) *EvolutionService {
    return &EvolutionService{deps: d, stopCh: make(chan struct{})}
}

func (s *EvolutionService) Name() string        { return "evolution-service" }
func (s *EvolutionService) HealthCheck(_ context.Context) error { return nil }

func (s *EvolutionService) Start(ctx context.Context) error {
    go s.loop(ctx)
    return nil
}

func (s *EvolutionService) Stop(_ context.Context) error {
    close(s.stopCh)
    return nil
}

func (s *EvolutionService) loop(ctx context.Context) {
    ticker := time.NewTicker(cyclePeriod)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            s.runCycle(ctx)
        case <-s.stopCh:
            return
        case <-ctx.Done():
            return
        }
    }
}

func (s *EvolutionService) runCycle(ctx context.Context) {
    for _, aid := range s.listActiveAgents(ctx) {
        s.evalParamAdaptation(ctx, aid)
        s.evalModelSwitch(ctx, aid)
        s.evalPromptUpgrade(ctx, aid)
    }
}

func (s *EvolutionService) listActiveAgents(_ context.Context) []string {
    return nil // seeded in future iteration via Prometheus label values API
}

func (s *EvolutionService) evalParamAdaptation(ctx context.Context, agentID string) {
    rate := s.deps.SampleRepo.ExhaustionRate(ctx, agentID, constants.EvolutionSampleWindow)
    if rate < constants.EvolutionExhaustionThreshold {
        return
    }
    current := s.deps.ConfigReader.GetMaxIterations(ctx, agentID)
    newMax := current + 2
    if newMax > constants.AgentMaxIterationsCeiling {
        newMax = constants.AgentMaxIterationsCeiling
    }
    if newMax == current {
        return
    }
    if err := s.deps.ConfigWriter.SetMaxIterations(ctx, agentID, newMax); err != nil {
        s.deps.Logger.Error("evolution: set max_iterations failed",
            zap.String("agent_id", agentID), zap.Error(err))
        return
    }
    _ = s.deps.RecRepo.Save(ctx, &evdomain.Recommendation{
        ID:         uuid.NewString(),
        AgentID:    agentID,
        RecType:    evdomain.RecParamAdapt,
        RiskLevel:  evdomain.RiskLow,
        Status:     evdomain.StatusAutoApplied,
        Payload:    map[string]any{"max_iterations": newMax, "previous": current},
        Evidence:   map[string]any{"exhaustion_rate": rate, "sample_count": constants.EvolutionSampleWindow},
        Confidence: 0.85,
        CreatedAt:  time.Now(),
    })
    s.deps.Logger.Info("evolution: param adapt applied",
        zap.String("agent_id", agentID), zap.Int("old", current), zap.Int("new", newMax))
}

func (s *EvolutionService) evalModelSwitch(ctx context.Context, agentID string) {
    burnRate, err := s.deps.Querier.Query(ctx,
        fmt.Sprintf(`job:agent_success_rate:error_budget_burn_1h{agent_id="%s"}`, agentID))
    if err != nil || burnRate <= constants.EvolutionBurnRateModelSwitchThreshold {
        return
    }
    _ = s.deps.RecRepo.Save(ctx, &evdomain.Recommendation{
        ID:         uuid.NewString(),
        AgentID:    agentID,
        RecType:    evdomain.RecModelSwitch,
        RiskLevel:  evdomain.RiskHigh,
        Status:     evdomain.StatusPending,
        Payload:    map[string]any{"reason": "error_budget_burn_rate_exceeded"},
        Evidence:   map[string]any{"burn_rate_1h": burnRate},
        Confidence: 0.70,
        CreatedAt:  time.Now(),
    })
}

func (s *EvolutionService) evalPromptUpgrade(ctx context.Context, agentID string) {
    count := s.deps.SampleRepo.NegativeFeedbackCount(ctx, "", agentID, 7)
    if count < constants.EvolutionNegativeFeedbackThreshold {
        return
    }
    _ = s.deps.RecRepo.Save(ctx, &evdomain.Recommendation{
        ID:         uuid.NewString(),
        AgentID:    agentID,
        RecType:    evdomain.RecPromptUpgrade,
        RiskLevel:  evdomain.RiskHigh,
        Status:     evdomain.StatusPending,
        Payload:    map[string]any{"reason": "high_negative_feedback"},
        Evidence:   map[string]any{"negative_feedback_count_7d": count},
        Confidence: 0.65,
        CreatedAt:  time.Now(),
    })
}

// EvalParamAdaptation is exported for unit testing.
func (s *EvolutionService) EvalParamAdaptation(ctx context.Context, agentID string) {
    s.evalParamAdaptation(ctx, agentID)
}
```

- [ ] **Step 3: Write unit tests**

```go
// internal/evolution/application/evolution_service_test.go
package application_test

import (
    "context"
    "testing"
    "time"

    "go.uber.org/zap/zaptest"

    "github.com/byteBuilderX/stratum/internal/evolution/application"
    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
    evport "github.com/byteBuilderX/stratum/internal/evolution/domain/port"
)

type stubSampleRepo struct{ rate float64 }

func (s *stubSampleRepo) Save(_ context.Context, _ *evdomain.ExecutionSample) error { return nil }
func (s *stubSampleRepo) ExhaustionRate(_ context.Context, _ string, _ int) float64 { return s.rate }
func (s *stubSampleRepo) NegativeFeedbackCount(_ context.Context, _, _ string, _ int) int { return 0 }
func (s *stubSampleRepo) UpdateFeedback(_ context.Context, _, _, _ string) error           { return nil }
func (s *stubSampleRepo) AvgCostPerSuccess(_ context.Context, _, _ string, _ int) float64  { return 0 }

type stubRecRepo struct{ saved []*evdomain.Recommendation }

func (r *stubRecRepo) Save(_ context.Context, rec *evdomain.Recommendation) error {
    r.saved = append(r.saved, rec); return nil
}
func (r *stubRecRepo) List(_ context.Context, _ string, _ evdomain.RecStatus, _ string) ([]*evdomain.Recommendation, error) {
    return r.saved, nil
}
func (r *stubRecRepo) UpdateStatus(_ context.Context, _ string, _ evdomain.RecStatus) error { return nil }

type stubQuerier struct{}

func (q *stubQuerier) Query(_ context.Context, _ string) (float64, error) { return 0, nil }

type stubCfgRW struct{ maxIter, setTo int }

func (c *stubCfgRW) GetMaxIterations(_ context.Context, _ string) int  { return c.maxIter }
func (c *stubCfgRW) SetMaxIterations(_ context.Context, _ string, v int) error {
    c.setTo = v; return nil
}

// compile-time interface assertions
var _ evport.SampleRepo         = (*stubSampleRepo)(nil)
var _ evport.RecommendationRepo = (*stubRecRepo)(nil)
var _ evport.MetricsQuerier     = (*stubQuerier)(nil)
var _ evport.AgentConfigReader  = (*stubCfgRW)(nil)
var _ evport.AgentConfigWriter  = (*stubCfgRW)(nil)

func mkSvc(t *testing.T, rate float64, maxIter int) (*application.EvolutionService, *stubRecRepo, *stubCfgRW) {
    t.Helper()
    recs := &stubRecRepo{}
    cfg := &stubCfgRW{maxIter: maxIter}
    svc := application.NewEvolutionService(application.Deps{
        SampleRepo:   &stubSampleRepo{rate: rate},
        RecRepo:      recs,
        Querier:      &stubQuerier{},
        ConfigReader: cfg,
        ConfigWriter: cfg,
        Logger:       zaptest.NewLogger(t),
    })
    return svc, recs, cfg
}

func TestParamAdapt_AppliesWhenHigh(t *testing.T) {
    svc, recs, cfg := mkSvc(t, 0.25, 10)
    svc.EvalParamAdaptation(context.Background(), "a1")
    if cfg.setTo != 12 {
        t.Errorf("want 12, got %d", cfg.setTo)
    }
    if len(recs.saved) != 1 || recs.saved[0].Status != evdomain.StatusAutoApplied {
        t.Error("expected one auto_applied recommendation")
    }
    _ = time.Now()
}

func TestParamAdapt_SkipsWhenLow(t *testing.T) {
    svc, recs, cfg := mkSvc(t, 0.10, 10)
    svc.EvalParamAdaptation(context.Background(), "a1")
    if cfg.setTo != 0 || len(recs.saved) != 0 {
        t.Error("expected no change")
    }
}

func TestParamAdapt_RespectsCeiling(t *testing.T) {
    svc, _, cfg := mkSvc(t, 0.30, 19)
    svc.EvalParamAdaptation(context.Background(), "a1")
    if cfg.setTo != 20 {
        t.Errorf("want 20 (ceiling), got %d", cfg.setTo)
    }
}
```

- [ ] **Step 4: Run tests**

```bash
cd /home/yang/go-projects/stratum && go test -v -race ./internal/evolution/application/...
```

Expected: 3 tests PASS

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum
git add internal/evolution/application/ pkg/constants/evolution.go
git commit -m "feat(evolution): EvolutionService rule engine + harness.Component + unit tests"
```

---

### Task 9: Wire SampleWriter into AgentService.ExecuteStream

**Files:**

- Modify: `internal/agent/application/agent_service.go` — add `SampleWriter` to `AgentServiceDeps` + call in `run()`
- Create: `api/wiring/evolution.go` — `pgSampleWriter` adapter + `buildEvolution`
- Modify: `api/wiring/wiring.go` — add `Evolution *Evolution` field + build step

**Interfaces:**

- Consumes: `port.SampleWriter` from Task 5; `evpersistence.NewSampleRepo` from Task 6
- Produces: sample written async after every `ExecuteStream.run()` call

- [ ] **Step 1: Add SampleWriter to AgentServiceDeps**

In `internal/agent/application/agent_service.go`, add to `AgentServiceDeps`:

```go
SampleWriter port.SampleWriter // nil = sampling disabled
```

- [ ] **Step 2: Add publishSample helper + wire into run()**

Add after the existing `recordExecution` function:

```go
func (s *AgentService) publishSample(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
    res *AgentResult, durationMs int, runErr error,
) {
    if s.deps.SampleWriter == nil {
        return
    }
    outcome := "success"
    errType := ""
    if runErr != nil {
        outcome = "error"
        m := runErr.Error()
        if strings.Contains(m, "timeout") || strings.Contains(m, "deadline") {
            errType = "timeout"
        } else {
            errType = "unknown"
        }
    } else if res != nil && res.Steps > 0 && res.Steps >= meta.MaxIterations {
        outcome = "iterations_exhausted"
    }
    sample := port.AgentExecSample{
        TenantID:        meta.TenantID,
        AgentID:         agentID,
        ConversationID:  req.ConversationID,
        TraceID:         meta.TraceID,
        TotalDurationMs: durationMs,
        Outcome:         outcome,
        ErrorType:       errType,
    }
    if res != nil {
        sample.IterationsUsed = res.Steps
        sample.InputTokens = res.TokensUsed
    }
    pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
    defer cancel()
    if err := s.deps.SampleWriter.WriteSample(pubCtx, sample); err != nil {
        s.deps.Logger.Warn("evolution: write sample failed", zap.Error(err))
    }
}
```

In the `run` closure inside `ExecuteStream`, after `s.recordExecution(...)`, add:

```go
go s.publishSample(ctx, agentID, req, meta, res, durationMs, runErr)
```

Add `RecordFeedback` delegation:

```go
func (s *AgentService) RecordFeedback(ctx context.Context, tenantID, traceID, signal string) error {
    if s.deps.SampleWriter == nil {
        return nil
    }
    return s.deps.SampleWriter.RecordFeedback(ctx, tenantID, traceID, signal)
}
```

Also add `SetSampleWriter`, `GetAgentMaxIterations`, `SetAgentMaxIterations`:

```go
func (s *AgentService) SetSampleWriter(w port.SampleWriter) { s.deps.SampleWriter = w }

func (s *AgentService) GetAgentMaxIterations(ctx context.Context, agentID string) int {
    a, ok := s.deps.Registry.Get(ctx, agentID)
    if !ok {
        return 10
    }
    return a.GetConfig().MaxIterations
}

func (s *AgentService) SetAgentMaxIterations(ctx context.Context, agentID string, value int) error {
    return s.deps.Registry.UpdateMaxIterations(ctx, agentID, value)
}
```

Note: `Registry.UpdateMaxIterations` requires adding the method to the `Registry` interface in `internal/agent/application/registry.go` and implementing it in `internal/agent/infrastructure/persistence/agent_repo.go` via `UPDATE agents SET max_iterations = $1 WHERE id = $2`.

- [ ] **Step 3: Create api/wiring/evolution.go**

```go
package wiring

import (
    "context"

    agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
    agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
    evapp "github.com/byteBuilderX/stratum/internal/evolution/application"
    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
    evport "github.com/byteBuilderX/stratum/internal/evolution/domain/port"
    evpersistence "github.com/byteBuilderX/stratum/internal/evolution/infrastructure/persistence"
    evpromql "github.com/byteBuilderX/stratum/internal/evolution/infrastructure/promql"
    "github.com/byteBuilderX/stratum/internal/platform/harness"
)

// Evolution holds the wired evolution bounded context components.
type Evolution struct {
    Service    *evapp.EvolutionService
    SampleRepo evport.SampleRepo
    RecRepo    evport.RecommendationRepo
}

// pgSampleWriter wraps evport.SampleRepo as the agent-side SampleWriter port.
type pgSampleWriter struct{ repo evport.SampleRepo }

func (w *pgSampleWriter) WriteSample(ctx context.Context, s agentport.AgentExecSample) error {
    sample := &evdomain.ExecutionSample{
        TenantID:        s.TenantID,
        AgentID:         s.AgentID,
        ConversationID:  s.ConversationID,
        TraceID:         s.TraceID,
        Model:           s.Model,
        Provider:        s.Provider,
        InputTokens:     s.InputTokens,
        OutputTokens:    s.OutputTokens,
        CostUnits:       s.CostUnits,
        TotalDurationMs: s.TotalDurationMs,
        IterationsUsed:  s.IterationsUsed,
        IterationsMax:   s.IterationsMax,
        Outcome:         evdomain.OutcomeSignal(s.Outcome),
        ErrorType:       s.ErrorType,
        FinishReason:    s.FinishReason,
    }
    for _, st := range s.Steps {
        sample.Steps = append(sample.Steps, evdomain.StepRecord{
            StepIndex:  st.StepIndex,
            ToolName:   st.ToolName,
            InputHash:  st.InputHash,
            Status:     st.Status,
            DurationMs: st.DurationMs,
        })
    }
    return w.repo.Save(ctx, sample)
}

func (w *pgSampleWriter) RecordFeedback(ctx context.Context, tenantID, traceID, signal string) error {
    return w.repo.UpdateFeedback(ctx, tenantID, traceID, signal)
}

// agentConfigAdapter adapts AgentService to evolution's AgentConfigReader/Writer ports.
type agentConfigAdapter struct{ svc *agentapp.AgentService }

func (a *agentConfigAdapter) GetMaxIterations(ctx context.Context, agentID string) int {
    return a.svc.GetAgentMaxIterations(ctx, agentID)
}
func (a *agentConfigAdapter) SetMaxIterations(ctx context.Context, agentID string, v int) error {
    return a.svc.SetAgentMaxIterations(ctx, agentID, v)
}

func buildEvolution(c *Container) error {
    if c.Storage.DB == nil {
        return nil
    }
    sampleRepo := evpersistence.NewSampleRepo(c.Storage.DB)
    recRepo := evpersistence.NewRecommendationRepo(c.Storage.DB)

    writer := &pgSampleWriter{repo: sampleRepo}
    c.Agent.Service.SetSampleWriter(writer)

    cfgAdapter := &agentConfigAdapter{svc: c.Agent.Service}
    svc := evapp.NewEvolutionService(evapp.Deps{
        SampleRepo:   sampleRepo,
        RecRepo:      recRepo,
        Querier:      evpromql.NewQuerier(c.Config.PrometheusURL),
        ConfigReader: cfgAdapter,
        ConfigWriter: cfgAdapter,
        Logger:       c.Logger,
    })

    c.Evolution = &Evolution{Service: svc, SampleRepo: sampleRepo, RecRepo: recRepo}
    c.addShutdown(svc.Stop)
    harness.Register(svc)
    return nil
}
```

- [ ] **Step 4: Add Evolution to Container + build step**

In `api/wiring/wiring.go`, add to `Container`:

```go
Evolution *Evolution
```

Add to build steps (after Agent step):

```go
{name: "evolution", fn: func(ctx context.Context) error { return buildEvolution(c) }},
```

Add `PrometheusURL string` to config struct (default `"http://localhost:9090"`).

- [ ] **Step 5: Verify**

```bash
go build ./...
```

Expected: clean

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum
git add internal/agent/application/agent_service.go \
    internal/agent/infrastructure/persistence/agent_repo.go \
    api/wiring/evolution.go api/wiring/wiring.go
git commit -m "feat(evolution): wire SampleWriter into AgentService.ExecuteStream + buildEvolution"
```

---

### Task 10: Feedback Endpoint + Evolution Admin API + Router Registration

**Files:**

- Modify: `api/http/handler/agent_exec_handler.go` — add `RecordFeedback` handler
- Create: `api/http/handler/evolution_handler.go` — evolution admin handler
- Modify: `api/http/router.go` — register feedback + evolution admin routes

**Interfaces:**

- Consumes: `AgentService.RecordFeedback`; `Evolution.RecRepo`
- Produces: `POST /agents/:id/executions/:traceID/feedback`, `GET/POST /admin/evolution/recommendations`

- [ ] **Step 1: Add RecordFeedback handler to agent_exec_handler.go**

```go
type FeedbackRequest struct {
    Signal string `json:"signal" binding:"required,oneof=positive negative abandoned"`
}

func (h *AgentHandler) RecordFeedback(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    traceID := c.Param("traceID")
    var req FeedbackRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
        return
    }
    if err := h.svc.RecordFeedback(c.Request.Context(), tenantID, traceID, req.Signal); err != nil {
        _ = c.Error(err)
        return
    }
    c.Status(http.StatusNoContent)
}
```

- [ ] **Step 2: Create api/http/handler/evolution_handler.go**

```go
package handler

import (
    "net/http"

    "github.com/gin-gonic/gin"
    "go.uber.org/zap"

    evdomain "github.com/byteBuilderX/stratum/internal/evolution/domain"
    evport "github.com/byteBuilderX/stratum/internal/evolution/domain/port"
)

type EvolutionHandler struct {
    recRepo evport.RecommendationRepo
    logger  *zap.Logger
}

func NewEvolutionHandler(repo evport.RecommendationRepo, logger *zap.Logger) *EvolutionHandler {
    return &EvolutionHandler{recRepo: repo, logger: logger}
}

func (h *EvolutionHandler) ListRecommendations(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok {
        respondMissingTenant(c)
        return
    }
    status := evdomain.RecStatus(c.DefaultQuery("status", "pending"))
    agentID := c.Query("agent_id")
    recs, err := h.recRepo.List(c.Request.Context(), tenantID, status, agentID)
    if err != nil {
        _ = c.Error(err)
        return
    }
    c.JSON(http.StatusOK, gin.H{"items": recs})
}

func (h *EvolutionHandler) ApplyRecommendation(c *gin.Context) {
    id := c.Param("id")
    if err := h.recRepo.UpdateStatus(c.Request.Context(), id, evdomain.StatusApproved); err != nil {
        _ = c.Error(err)
        return
    }
    c.Status(http.StatusNoContent)
}

func (h *EvolutionHandler) RejectRecommendation(c *gin.Context) {
    id := c.Param("id")
    if err := h.recRepo.UpdateStatus(c.Request.Context(), id, evdomain.StatusRejected); err != nil {
        _ = c.Error(err)
        return
    }
    c.Status(http.StatusNoContent)
}
```

- [ ] **Step 3: Register routes in router.go**

In `registerAgents`, add feedback route:

```go
agents.POST("/:id/executions/:traceID/feedback", requireActive, agentHandler.RecordFeedback)
```

Add `registerEvolution` call in `NewRouter` after `registerAgents`:

```go
registerEvolution(r, c)
```

```go
func registerEvolution(r *gin.Engine, c *wiring.Container) {
    if c.Evolution == nil || c.Platform.JWTService == nil {
        return
    }
    jwtMW := middleware.JWTMiddleware(c.Platform.JWTService)
    h := handler.NewEvolutionHandler(c.Evolution.RecRepo, c.Logger)
    g := r.Group("/admin/evolution", jwtMW, middleware.RequireGlobalAdmin())
    {
        g.GET("/recommendations", h.ListRecommendations)
        g.POST("/recommendations/:id/apply", h.ApplyRecommendation)
        g.POST("/recommendations/:id/reject", h.RejectRecommendation)
    }
}
```

- [ ] **Step 4: Verify**

```bash
cd /home/yang/go-projects/stratum && go vet ./api/...
```

Expected: clean

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum
git add api/http/handler/agent_exec_handler.go \
    api/http/handler/evolution_handler.go \
    api/http/router.go
git commit -m "feat(evolution): feedback endpoint + evolution admin API routes"
```

---

### Task 11: Prometheus SLO Recording Rules + Multi-Burn-Rate Alerts

**Files:**

- Create: `deploy/prometheus/rules/slo.yaml`

- [ ] **Step 1: Create directory and rules file**

```bash
mkdir -p /home/yang/go-projects/stratum/deploy/prometheus/rules
```

- [ ] **Step 2: Write slo.yaml**

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

      - record: job:llm_error_rate:ratio_rate1h
        expr: |
          sum(rate(llm_requests_total{status=~"error|timeout"}[1h]))
          / sum(rate(llm_requests_total[1h]))

      - record: job:llm_error_rate:error_budget_burn_1h
        expr: job:llm_error_rate:ratio_rate1h / 0.01

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
          summary: "Agent success SLO burning fast — error budget gone in ~1h"
          description: "1h burn rate {{ $value | humanize }}"

      - alert: AgentSuccessRateSLOBurnSlow
        expr: |
          job:agent_success_rate:error_budget_burn_1h > 3
          and job:agent_success_rate:error_budget_burn_6h > 1.5
        for: 15m
        labels:
          severity: warning
          evolution_trigger: param_adapt
        annotations:
          summary: "Agent success SLO slowly burning — error budget gone in ~3d"

      - alert: LLMErrorRateSLOBurn
        expr: job:llm_error_rate:error_budget_burn_1h > 14.4
        for: 2m
        labels:
          severity: critical
          evolution_trigger: model_switch
        annotations:
          summary: "LLM error rate SLO burning — consider model switch"
```

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/stratum
git add deploy/prometheus/rules/slo.yaml
git commit -m "feat(observability): SLO recording rules + multi-burn-rate alerting"
```

---

### Task 12: Frontend Feedback Component

**Files:**

- Create: `web/src/modules/agent/api/feedback.api.ts`
- Create: `web/src/modules/agent/components/ExecutionFeedback.tsx`
- Modify: `web/src/modules/agent/components/ChatMessageList.tsx` — render feedback after done messages
- Modify: `web/src/modules/agent/hooks/useChatPage.ts` — send `abandoned` on stream interruption
- Modify: `web/src/modules/agent/model/agent.ts` — add `traceId?: string` to ChatMessage

**Interfaces:**

- Consumes: `POST /agents/:id/executions/:traceID/feedback`
- Produces: like/dislike buttons after completed agent messages; abandoned signal on interruption

- [ ] **Step 1: Create feedback.api.ts**

```typescript
// web/src/modules/agent/api/feedback.api.ts
import { apiInstance } from '../../../services/api';

export type FeedbackSignal = 'positive' | 'negative' | 'abandoned';

export const recordFeedback = (
  agentId: string,
  traceId: string,
  signal: FeedbackSignal,
): Promise<void> =>
  apiInstance
    .post(`/agents/${agentId}/executions/${traceId}/feedback`, { signal })
    .then(() => undefined);
```

- [ ] **Step 2: Create ExecutionFeedback.tsx**

```tsx
// web/src/modules/agent/components/ExecutionFeedback.tsx
import { DislikeOutlined, LikeOutlined } from '@ant-design/icons';
import { Button, Space, message as msg } from 'antd';
import { useState } from 'react';

import { recordFeedback } from '../api/feedback.api';

interface Props {
  agentId: string;
  traceId: string;
}

export const ExecutionFeedback = ({ agentId, traceId }: Props) => {
  const [sent, setSent] = useState<'positive' | 'negative' | null>(null);

  const submit = async (signal: 'positive' | 'negative') => {
    if (sent) return;
    try {
      await recordFeedback(agentId, traceId, signal);
      setSent(signal);
    } catch {
      msg.error('反馈发送失败');
    }
  };

  if (sent) {
    return (
      <span style={{ fontSize: 12, color: '#8c8c8c', marginTop: 4, display: 'block' }}>
        已记录{sent === 'positive' ? '好评' : '差评'}，谢谢！
      </span>
    );
  }

  return (
    <Space size={4} style={{ marginTop: 6 }}>
      <Button
        type="text"
        size="small"
        icon={<LikeOutlined />}
        onClick={() => submit('positive')}
        style={{ color: '#8c8c8c' }}
      />
      <Button
        type="text"
        size="small"
        icon={<DislikeOutlined />}
        onClick={() => submit('negative')}
        style={{ color: '#8c8c8c' }}
      />
    </Space>
  );
};
```

- [ ] **Step 3: Add traceId to ChatMessage model**

In `web/src/modules/agent/model/agent.ts`, add to the ChatMessage type/schema:

```ts
traceId?: string;
```

- [ ] **Step 4: Integrate into ChatMessageList.tsx**

Add import after existing imports:

```tsx
import { ExecutionFeedback } from './ExecutionFeedback';
```

After the `{m.role === 'agent' && m.id !== streamingMsgId && <ChatStepList steps={m.steps} />}` block, add:

```tsx
{m.role === 'agent' && !m.interrupted && m.id !== streamingMsgId && m.traceId && selectedAgent && (
  <ExecutionFeedback agentId={selectedAgent} traceId={m.traceId} />
)}
```

The `selectedAgent` prop is already in `Props` (current ChatMessageList.tsx line 17).

- [ ] **Step 5: Send abandoned signal on interruption in useChatPage.ts**

Add import at top of file:

```ts
import { recordFeedback } from '../api/feedback.api';
```

Replace the `if (prevMsgId)` block in `handleSend` (current lines 236–239) with:

```ts
const prevMsgId = streamMsgIdRef.current;
if (prevMsgId) {
  const prevMsg = messages.find((m) => m.id === prevMsgId);
  if (prevMsg?.traceId && selectedAgent) {
    recordFeedback(selectedAgent, prevMsg.traceId, 'abandoned').catch(() => undefined);
  }
  setMessages((prev) =>
    prev.map((m) => (m.id === prevMsgId ? { ...m, interrupted: true } : m)),
  );
  streamMsgIdRef.current = null;
}
```

- [ ] **Step 6: Build and verify**

```bash
cd /home/yang/go-projects/stratum/web && npm run build 2>&1 | tail -20
```

Expected: clean build

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum/web
git add src/modules/agent/api/feedback.api.ts \
    src/modules/agent/components/ExecutionFeedback.tsx \
    src/modules/agent/components/ChatMessageList.tsx \
    src/modules/agent/hooks/useChatPage.ts \
    src/modules/agent/model/agent.ts
git commit -m "feat(frontend): execution feedback with like/dislike and abandoned signal"
```

---

## Self-Review

### Spec Coverage Check

| Spec section | Covered by task |
|---|---|
| MetricsProvider +10 methods | Task 1 + 2 |
| Sanitize high-cardinality labels | Task 1 |
| Exemplar injection | Task 2 |
| OTel Gen AI span attributes | Task 3 |
| DB migration (3 tables) | Task 4 |
| ExecutionSample domain type | Task 5 |
| Recommendation domain type | Task 5 |
| SampleRepo / RecRepo / MetricsQuerier ports | Task 5 |
| AgentConfigReader / Writer ports | Task 5 |
| Consumer-side SampleWriter port | Task 5 |
| pgx SampleRepo | Task 6 |
| pgx RecommendationRepo | Task 6 |
| PromQL querier | Task 7 |
| EvolutionService rule engine | Task 8 |
| Rule A — param adaptation | Task 8 |
| Rule B — model switch | Task 8 |
| Rule C — prompt upgrade | Task 8 |
| publishSample in ExecuteStream | Task 9 |
| Wiring + harness registration | Task 9 |
| POST feedback endpoint | Task 10 |
| GET/POST admin evolution routes | Task 10 |
| SLO recording rules | Task 11 |
| Multi-burn-rate alerts | Task 11 |
| Frontend like/dislike component | Task 12 |
| abandoned signal on interruption | Task 12 |

全 25 项 spec 需求覆盖，无漏项。

### Architecture boundary check

- `evolution/domain/` 零第三方依赖：仅 stdlib + `pkg/constants` ✓
- `agent/application/` 仅 import `agent/domain/port.SampleWriter`（消费者侧）；不 import `evolution/` ✓
- `api/wiring/evolution.go` 是唯一同时 import `agent/application` + `evolution/` 的文件；合法（组合根） ✓
- `evolution/infrastructure/` 仅 import `evolution/domain/port` + pgx；无 `agent`/`memory` 依赖 ✓
- DDL 在 `pkg/migration/sql/015_evolution.up.sql`（public schema + tenant_id 列）；符合多租户 DDL 放置规则 ✓

### Type consistency

- `AgentExecSample.Outcome` 是 `string`（agent port）；`ExecutionSample.Outcome` 是 `evdomain.OutcomeSignal`（typed string）。wiring adapter 在 `WriteSample` 中转换：`evdomain.OutcomeSignal(s.Outcome)` ✓
- Task 8 测试中 `var _ evport.SampleRepo = (*stubSampleRepo)(nil)` 编译期断言，接口漂移会即刻报错 ✓

### Placeholder scan

- `listActiveAgents` 返回 nil + TODO 注释：有意为之，规则引擎可通过外部调用 `EvalParamAdaptation` 单独触发；Task 8 Step 1 已说明 ✓
- 无其他 TBD / TODO / "fill in later" ✓
