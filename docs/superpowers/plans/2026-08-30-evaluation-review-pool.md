# 评测人工评审池（P1c）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为运行态观测与评测集判定内联接入人工评审池：判异/低置信信号入池、纯监控台展示、人工 4 分类回写（含 promote 沉淀 + 校准样本 + 归因条目），并提供积压告警。

**Architecture:** 方案 A 内联触发 + 独立评审池模块。`ObservationService.Process` 落库后、`Service.runCase` judge 判定后各自调用 `ReviewEscalator.TryEscalate*`（fail-open：升级失败绝不阻断主流程）。触发规则为 domain 纯函数；回写副作用（promote 进评测集草稿、校准样本、归因条目）由 `ReviewService` 编排，经 `port.SuiteRepository.AddDraftCases` + `port.TraceEvidenceReader` 复用现有通道。

**Tech Stack:** Go 1.25 / pgx v5 / Gin / Prometheus / tenant-scoped schema；前端 React 18 + Ant Design 5 Tabs。

## Global Constraints

（逐条来自 spec `docs/superpowers/specs/2026-08-30-evaluation-review-pool-design.md`，本计划所有任务隐含遵守。）

- **评审池范围**：观测（`EvalObservation`）+ 评测集 judge 判定都覆盖。
- **judge 置信度**：扩展 `LLMJudge` 契约输出真实 `confidence`（0-1）；解析缺失/无效时**回退 1.0**。
- **fail-closed 语义**：纯监控台 + 积压告警；评审池**不阻塞执行、不改 run/观测结论**。`TryEscalate*` 失败仅记日志 + `IncEvalReviewEscalateFailure`，永远不返回错误阻断主流程。
- **回写动作**：完整闭环（轻量记录）——回写标记 + promote 沉淀 + 校准样本表 + 归因条目表，全部 tenant-scoped。
- **触发规则**（domain 纯函数，硬编码）：
  - `low_confidence`：任一 judge 维度 `confidence < 0.6`（常量 `ReviewLowConfidenceThreshold`）。
  - `dimension_split`：存在 `score >= 0.5` 且存在 `score < 0.5`（沿用 `JudgeBelowThreshold`）。
  - `judge_rule_conflict`：规则命中（`Signals.Rule` 非空）+ `verdict == block` + 全部 judge 维度 pass（无跌阈）。
  - `needs_review`：`EvalCase.NeedsReview == true`（评测集专用）。
- **幂等入池**：`UNIQUE(source_type, source_id, trigger_reason)`；同 key 已存在不插入。
- **DDD 分层**：domain 仅 stdlib；application 不 import pgx/Redis/NATS/Gin；跨 context 接口定义在消费方 `domain/port/`。
- **tenant DDL**：新表进 `pkg/storage/postgres/tenant_schema.sql`（`CREATE TABLE IF NOT EXISTS` 幂等），**禁止**进 `pkg/migration/sql/`；repository 全部走 `execTenantTx(ctx, pool, tenantID, fn)` + `postgres.WithTenant`；port 方法显式携带 `tenantID string`。
- **JSONB 写入**：自定义 Go struct 先 `json.Marshal` 再传 `string(b)`，禁止直接传 struct 或 `pgtype.JSONB{}`。
- **行宽 ≤120**；import 分组 stdlib → third-party → internal；错误逐层 `fmt.Errorf("...: %w", err)`；日志只用 Zap。
- **行为数字禁止内联**：跨包放 `pkg/constants/<domain>.go`，包内共享放 `internal/<pkg>/defaults.go`，单文件放本文件 `const` 块。
- **提交规范**：标题 `[type](scope): description`；结尾 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- **指标契约**：新增 `SetEvalReviewBacklog(count int64)`（Gauge）+ `IncEvalReviewEscalateFailure()`（Counter），须同步 `MetricsProvider` 接口、`NoopMetrics` stub、Prometheus 实现与注册。签名用 `int64` 而非 spec §8.3 的 `float64`，与既有 `SetEvalQueueBacklog(queue string, count int64)` 一致（Gauge Set 接受 float64，int64 隐式转换）。
- **cfg 来源（spec §10 裁剪声明）**：`ReviewConfig` 阈值在本阶段由 wiring 用 `constants.ReviewLowConfidenceThreshold`/`constants.JudgeBelowThreshold` 常量直连组装（见 Task 9 `buildReviewService`）。spec §10 的「平台参数系统动态读取」不实现——`evaluation.review.*` 参数落 `pkg/constants/evaluation.go` 常量即可；运行期参数覆盖留作后续增强，非本计划范围。
- **前端**：用户可见字符串中文；错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 3 })`；禁止 `alert()/confirm()`；组件 >200 行提取 hook/component。
- **E2E**：浏览器验证必须无头（Playwright headless）；改参数契约 = 改 proto 后 `make proto-gen`；黄金文件 `make record-contracts` 更新。

---

### Task 1: 领域实体与常量增强

**Files:**

- Modify: `internal/evaluation/domain/evaluation.go`（AssertionResult:25、EvalCase:30、EvalCaseResult:135）
- Modify: `internal/evaluation/domain/observation.go:54`（JudgeSignal）
- Modify: `internal/evaluation/domain/port/evaluation.go`（ObservedTrace、新增 ToolObservation）
- Modify: `api/wiring/evaluation.go:971`（mapEvaluationEvidence 映射 Tools）
- Modify: `pkg/constants/evaluation.go`（新增 review 阈值常量）
- Test: `internal/evaluation/domain/domain_test.go`（若存在则追加；否则新建 `internal/evaluation/domain/review_pool_test.go` 在本 Task 只放编译断言——完整触发逻辑测试在 Task 4）

**Interfaces:**

- Consumes: 现有 `AssertionResult{Passed, Message}`、`EvalCase`、`EvalCaseResult`、`JudgeSignal`、`ObservedTrace`。
- Produces:
  - `domain.AssertionResult{Passed bool, Message string, Confidence float64}`（Confidence 缺失回退 1.0 在 Task 3 的 parse 层做）
  - `domain.EvalCase.NeedsReview bool`（json `needs_review,omitempty`）
  - `domain.EvalCaseResult.ID string`（json `id,omitempty`；runCase 生成稳定 ID，SaveRun 不再重新生成）
  - `domain.JudgeSignal.Reason string`（json `reason,omitempty`）
  - `evalport.ToolObservation{ToolName, ToolType, StepIndex, ProviderType, CapabilityID, Arguments map[string]any, RawText}`（json 标签）
  - `evalport.ObservedTrace.Tools []ToolObservation`
  - `constants.ReviewLowConfidenceThreshold = 0.6`、`constants.ReviewBacklogAlertThreshold = 50`

- [ ] **Step 1: 写测试**（编译期契约断言 + JSON round-trip）

新建 `internal/evaluation/domain/review_pool_test.go`：

```go
package domain

import (
    "encoding/json"
    "testing"
)

// TestAssertionResultConfidenceRoundTrip 断言 Confidence 进入序列化契约；
// 缺失 confidence 反序列化为 0（解析层负责回退 1.0，domain 不静默改值）。
func TestAssertionResultConfidenceRoundTrip(t *testing.T) {
    raw := []byte(`{"passed":true,"message":"ok","confidence":0.8}`)
    var got AssertionResult
    if err := json.Unmarshal(raw, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if got.Confidence != 0.8 {
        t.Fatalf("confidence = %v, want 0.8", got.Confidence)
    }
    // 缺失 confidence 原样保留 0，由 parseJudgeResponse 层回退。
    if err := json.Unmarshal([]byte(`{"passed":false,"message":"x"}`), &got); err != nil {
        t.Fatalf("unmarshal missing confidence: %v", err)
    }
    if got.Confidence != 0 {
        t.Fatalf("confidence = %v, want 0 (unset)", got.Confidence)
    }
}

// TestEvalCaseResultIDJSON 断言 ID 字段存在且缺省为零值（runCase 生成前）。
func TestEvalCaseResultIDJSON(t *testing.T) {
    raw := []byte(`{"case_id":"c1","passed":true}`)
    var got EvalCaseResult
    if err := json.Unmarshal(raw, &got); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if got.ID != "" {
        t.Fatalf("ID = %q, want empty", got.ID)
    }
    got.ID = "r-1"
    out, err := json.Marshal(got)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    if !json.Valid(out) {
        t.Fatalf("invalid json: %s", out)
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/domain/ -run 'TestAssertionResultConfidenceRoundTrip|TestEvalCaseResultIDJSON'`
Expected: FAIL — `AssertionResult has no field or method Confidence` / `EvalCaseResult has no field or method ID`。

- [ ] **Step 3: 实现 domain 实体增强**

`internal/evaluation/domain/evaluation.go`，AssertionResult 增加 Confidence：

```go
type AssertionResult struct {
    Passed  bool   `json:"passed"`
    Message string `json:"message,omitempty"`
    // Confidence 是 judge 判定置信度（0-1）。规则断言不产生该值；judge 解析
    // 缺失/无效时由 parseJudgeResponse 回退 1.0（spec §6.2，本 domain 不静默改值）。
    Confidence float64 `json:"confidence,omitempty"`
}
```

`internal/evaluation/domain/evaluation.go`，EvalCase 增加 NeedsReview（放在 JudgeSpec 之后）：

```go
    JudgeSpec *JudgeSpec `json:"judge_spec,omitempty"`
    // NeedsReview 标记该 case 判定后必须进入人工评审池（spec §6.6 触发规则 4）。
    // 仅对 assertion_mode=judge 生效；规则断言不触发评审池。
    NeedsReview bool `json:"needs_review,omitempty"`
```

`internal/evaluation/domain/evaluation.go`，EvalCaseResult 增加 ID（struct 首字段）：

```go
type EvalCaseResult struct {
    // ID 是该结果行主键，与 eval_case_results.id 共用（评审池 source_id）。
    // runCase 内生成稳定 ID，SaveRun 持久化直接采用（为空才回退生成）。
    ID           string                 `json:"id,omitempty"`
    CaseID       string                 `json:"case_id"`
    Passed       bool                   `json:"passed"`
    Message      string                 `json:"message,omitempty"`
    Error        string                 `json:"error,omitempty"`
    Actual       any                    `json:"actual,omitempty"`
    TraceID      string                 `json:"trace_id,omitempty"`
    Tokens       int                    `json:"tokens"`
    CostUSD      float64                `json:"cost_usd"`
    DurationMs   int                    `json:"duration_ms"`
    TraceEvidence *ObservedTraceEvidence `json:"trace_evidence,omitempty"`
    RAGEvidence  *RAGEvidenceInfo       `json:"rag_evidence,omitempty"`
}
```

`internal/evaluation/domain/observation.go`，JudgeSignal 增加 Reason：

```go
type JudgeSignal struct {
    Dimension  string  `json:"dimension"`
    Score      float64 `json:"score"`
    Confidence float64 `json:"confidence"`
    // Reason 是 judge 打分理由（评审池详情展示用；applyJudge 从 res.Message 填充）。
    Reason string `json:"reason,omitempty"`
}
```

- [ ] **Step 4: 扩展 ObservedTrace port + ToolObservation**

`internal/evaluation/domain/port/evaluation.go`，在 `ObservedResourceAssignment` 前新增类型，并给 ObservedTrace 加字段：

```go
// ToolObservation 是执行链路中一次工具调用的最小可观测摘要（评审池详情展示
// 工具序列用；agent domain.ToolObservation 的 summary 投影，见 mapEvaluationEvidence）。
type ToolObservation struct {
    ToolName     string         `json:"tool_name"`
    ToolType     string         `json:"tool_type"`
    StepIndex    int            `json:"step_index"`
    ProviderType string         `json:"provider_type"`
    CapabilityID string         `json:"capability_id"`
    Arguments    map[string]any `json:"arguments,omitempty"`
    RawText      string         `json:"raw_text,omitempty"`
}

type ObservedTrace struct {
    TraceID           string
    UserID            string
    CostUSD           float64
    LatencyMs         int64
    Input             string
    Output            string
    TotalTokens       int64
    Success           bool
    SecurityViolation bool
    Assignments       map[string]ObservedResourceAssignment
    // Tools 是执行链路工具调用序列（P1c 评审详情用；证据后端不返回时为 nil）。
    Tools []ToolObservation
}
```

- [ ] **Step 5: 映射 Tools**

`api/wiring/evaluation.go` 的 `mapEvaluationEvidence`，在 `return evalport.ObservedTrace{...}` 前加映射，并放入返回值：

```go
    tools := make([]evalport.ToolObservation, 0, len(evidence.Tools))
    for _, tool := range evidence.Tools {
        tools = append(tools, evalport.ToolObservation{
            ToolName: tool.ToolName, ToolType: tool.ToolType, StepIndex: tool.StepIndex,
            ProviderType: tool.ProviderType, CapabilityID: tool.CapabilityID,
            Arguments: tool.Arguments, RawText: tool.RawText,
        })
    }
    return evalport.ObservedTrace{
        TraceID: evidence.TraceID, UserID: evidence.UserID, CostUSD: evidence.CostUSD, LatencyMs: evidence.LatencyMs,
        Input: evidence.Input, Output: evidence.Output, TotalTokens: int64(evidence.TotalTokens),
        Success: evidence.Status == agentdomain.ExecStatusSuccess, SecurityViolation: evidence.SecurityViolation,
        Assignments: assignments, Tools: tools,
    }
```

（`evidence.Tools` 字段来自 `internal/agent/domain/evidence.go` 的 `TraceEvidence.Tools []ToolObservation`，字段名一致。若 `agentdomain.ToolObservation.Arguments` 不存在则改用 `RawText` 可投影字段，编译失败即提示。）

- [ ] **Step 6: 新增 review 常量**

`pkg/constants/evaluation.go`，在 `FeedbackNegativeThreshold` 常量块后追加：

```go
// Evaluation 人工评审池（P1c §6.6）阈值。
const (
    // ReviewLowConfidenceThreshold 是 judge 低置信触发评审池的阈值：任一维度
    // confidence < 该值入池（low_confidence）。
    ReviewLowConfidenceThreshold = 0.6
    // ReviewBacklogAlertThreshold 是评审池积压告警阈值：eval_review_backlog
    // 持续 > 该值触发 StratumEvalReviewBacklogHigh。
    ReviewBacklogAlertThreshold = 50
)
```

- [ ] **Step 7: 运行测试**

Run: `go test ./internal/evaluation/domain/ && go build ./...`
Expected: 全部 PASS / build 通过。（若 build 因 `ObservedTrace.Tools` 或 `AssertionResult` 新增字段产生其他编译错误，逐个修复——本 Task 是纯增量字段，不应有行为改动。）

- [ ] **Step 8: Commit**

```bash
git add internal/evaluation/domain/evaluation.go internal/evaluation/domain/observation.go internal/evaluation/domain/port/evaluation.go api/wiring/evaluation.go pkg/constants/evaluation.go internal/evaluation/domain/review_pool_test.go
git commit -m "feat(evaluation): 领域实体增加评审池字段与置信度契约

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: tenant DDL 三张评审池表

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`（eval_observations 定义后追加三表）
- Test: `pkg/storage/postgres/tenant_schema_safety_test.go`（若断言表集合则同步）

**Interfaces:**

- Consumes: 无（纯 DDL）。
- Produces: `eval_review_items`、`eval_calibration_samples`、`eval_attribution_entries` 三张 tenant-scoped 表。

- [ ] **Step 1: 写测试**

在 `pkg/storage/postgres/tenant_schema_safety_test.go` 追加用例（沿用该文件 stdlib 断言风格：`os.ReadFile` + `strings.Contains` + `t.Fatal`，不引入 require），断言三表存在且幂等、无 `pkg/migration/sql/` 泄漏：

```go
func TestTenantSchemaHasReviewPoolTables(t *testing.T) {
    ddl, err := os.ReadFile("tenant_schema.sql")
    if err != nil {
        t.Fatal(err)
    }
    text := string(ddl)
    for _, table := range []string{
        "eval_review_items",
        "eval_calibration_samples",
        "eval_attribution_entries",
    } {
        if !strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+table) {
            t.Fatalf("tenant schema missing %s", table)
        }
        // 幂等：不允许裸 CREATE（无 IF NOT EXISTS）。
        if strings.Contains(text, "CREATE TABLE "+table) {
            t.Fatalf("tenant schema has non-idempotent %s DDL", table)
        }
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/storage/postgres/ -run TestTenantSchemaHasReviewPoolTables`
Expected: FAIL — `"CREATE TABLE IF NOT EXISTS eval_review_items" not found`。

- [ ] **Step 3: 实现 DDL**

`pkg/storage/postgres/tenant_schema.sql`，在 `eval_observations` 的 `idx_eval_observations_trace` 之后、`optimization_jobs` 之前插入：

```sql
-- 人工评审池（P1c §6.6）：观测/评测集判定低置信与判异信号入池，人工 4 分类回写。
-- snapshot JSONB 保留入池时完整上下文（观测信号 / case 快照），评审详情免回查。
CREATE TABLE IF NOT EXISTS eval_review_items (
    id             TEXT PRIMARY KEY,
    source_type    TEXT NOT NULL CHECK (source_type IN ('observation','case_result')),
    source_id      TEXT NOT NULL,
    run_id         TEXT NOT NULL DEFAULT '',
    trace_id       TEXT NOT NULL DEFAULT '',
    resource_kind  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    trigger_reason TEXT NOT NULL CHECK (trigger_reason IN
        ('low_confidence','dimension_split','judge_rule_conflict','needs_review')),
    snapshot       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','reviewed')),
    human_verdict  TEXT NOT NULL DEFAULT '' CHECK (human_verdict IN
        ('','pass','fail','judge_misjudgment','case_revision')),
    reviewer       TEXT NOT NULL DEFAULT '',
    review_reason  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_eval_review_items_status
    ON eval_review_items(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_eval_review_items_source
    ON eval_review_items(source_type, source_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_eval_review_items_dedupe
    ON eval_review_items(source_type, source_id, trigger_reason);

-- judge 误判校准样本（P1c §9）：判 judge_misjudgment 时沉淀，供模型/阈值校准。
CREATE TABLE IF NOT EXISTS eval_calibration_samples (
    id            TEXT PRIMARY KEY,
    review_item_id TEXT NOT NULL REFERENCES eval_review_items(id) ON DELETE CASCADE,
    source_type   TEXT NOT NULL CHECK (source_type IN ('observation','case_result')),
    source_id     TEXT NOT NULL,
    judge_model   TEXT NOT NULL DEFAULT '',
    signals       JSONB NOT NULL DEFAULT '{}'::jsonb,
    human_verdict TEXT NOT NULL CHECK (human_verdict IN
        ('pass','fail','judge_misjudgment','case_revision')),
    reviewer      TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_eval_calibration_samples_item
    ON eval_calibration_samples(review_item_id);

-- 产品缺陷归因条目（P1c §9 轻量记录）：fail/case_revision 落归因。
CREATE TABLE IF NOT EXISTS eval_attribution_entries (
    id             TEXT PRIMARY KEY,
    review_item_id TEXT NOT NULL REFERENCES eval_review_items(id) ON DELETE CASCADE,
    source_type    TEXT NOT NULL CHECK (source_type IN ('observation','case_result')),
    source_id      TEXT NOT NULL,
    resource_kind  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    dimension      TEXT NOT NULL DEFAULT '',
    snapshot       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status         TEXT NOT NULL,
    reviewer       TEXT NOT NULL,
    reason         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_eval_attribution_entries_item
    ON eval_attribution_entries(review_item_id);
```

- [ ] **Step 4: 运行测试**

Run: `go test ./pkg/storage/postgres/...`
Expected: 全部 PASS（含既有 `tenant_schema_test.go` 的清理/幂等断言与新增用例）。若既有测试因表集合断言失败，仅在有明确断言清单的文件里补上新表名。

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_safety_test.go
git commit -m "feat(evaluation): 评审池三张 tenant 表 DDL

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: judge 契约扩展与 runCase 重构

**Files:**

- Modify: `internal/evaluation/application/service.go`（runCase:115、judgeCase:162）
- Modify: `internal/evaluation/application/observation_service.go`（applyJudge:220、judgeRubric:294）
- Modify: `internal/evaluation/infrastructure/persistence/run_repository.go`（SaveRun:59）
- Modify: `api/wiring/evaluation.go`（judgeDefaultRubric:539、parseJudgeResponse:801）
- Test: `api/wiring/evaluation_test.go`（parseJudgeResponse 用例）、`internal/evaluation/application/service_test.go`（judgeCase/runCase）

**Interfaces:**

- Consumes: Task 1 的 `AssertionResult.Confidence`、`EvalCaseResult.ID`、`EvalCase.NeedsReview`。
- Produces:
  - `parseJudgeResponse(content string) (domain.AssertionResult, error)`：解析 `{"passed","reason","confidence"}`，confidence 缺失/非 [0,1] 回退 1.0。
  - `judgeRubric(dimension string) string`：rubric 增加 confidence 输出指令。
  - `judgeDefaultRubric`（wiring const）：同上。
  - `ObservationService.applyJudge`：JudgeSignal 填真实 `Confidence`/`Reason`。
  - `Service.judgeCase(ctx, testCase, result) (domain.AssertionResult, domain.EvalCaseResult)`：返回真实 assertion（含 Confidence）。
  - `Service.runCase(ctx, tenantID, requestedBy, ref, runID, testCase) domain.EvalCaseResult`：生成 `result.ID`。
  - `SaveRun`：`result.ID` 非空则采用，为空回退 `uuid.Must(uuid.NewV7()).String()`。

- [ ] **Step 1: 写 parseJudgeResponse 测试**

在 `api/wiring/evaluation_test.go` 追加：

```go
func TestParseJudgeResponseConfidence(t *testing.T) {
    cases := []struct {
        name    string
        content string
        want    float64
    }{
        {"explicit confidence", `{"passed":true,"reason":"ok","confidence":0.72}`, 0.72},
        {"missing confidence falls back to 1.0", `{"passed":false,"reason":"bad"}`, 1.0},
        {"out-of-range confidence falls back to 1.0", `{"passed":true,"reason":"ok","confidence":1.8}`, 1.0},
        {"negative confidence falls back to 1.0", `{"passed":true,"reason":"ok","confidence":-0.3}`, 1.0},
        {"code fence tolerated", "```json\n{\"passed\":true,\"reason\":\"ok\",\"confidence\":0.4}\n```", 0.4},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := parseJudgeResponse(tc.content)
            if err != nil {
                t.Fatalf("parseJudgeResponse: %v", err)
            }
            if got.Confidence != tc.want {
                t.Fatalf("confidence = %v, want %v", got.Confidence, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/wiring/ -run TestParseJudgeResponseConfidence`
Expected: FAIL — missing confidence 场景得 `0`，want `1.0`。

- [ ] **Step 3: 实现 parseJudgeResponse + judgeDefaultRubric**

`api/wiring/evaluation.go`，`parseJudgeResponse` 重写：

```go
// parseJudgeResponse extracts {"passed": bool, "reason": string, "confidence": number}
// from the judge output, tolerating a markdown code fence around the JSON.
// Confidence is optional and defaults to 1.0 (missing, non-numeric or outside
// [0,1] are treated as "no confidence signal", spec §6.2).
func parseJudgeResponse(content string) (evaldomain.AssertionResult, error) {
    trimmed := strings.TrimSpace(content)
    if strings.HasPrefix(trimmed, "```") {
        trimmed = strings.TrimPrefix(trimmed, "```json")
        trimmed = strings.TrimPrefix(trimmed, "```")
        trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
    }
    var verdict struct {
        Passed     bool    `json:"passed"`
        Reason     string  `json:"reason"`
        Confidence float64 `json:"confidence"`
    }
    if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &verdict); err != nil {
        return evaldomain.AssertionResult{}, fmt.Errorf("LLM judge: parse verdict: %w", err)
    }
    confidence := verdict.Confidence
    if confidence < 0 || confidence > 1 {
        confidence = 1.0
    }
    return evaldomain.AssertionResult{Passed: verdict.Passed, Message: verdict.Reason, Confidence: confidence}, nil
}
```

`judgeDefaultRubric` 常量更新（追加 confidence 行）：

```go
const judgeDefaultRubric = `你是一名严谨的评测法官。根据以下标准判断实际输出是否通过：
1. 实际输出是否直接、完整地回答了输入要求；
2. 与期望输出的一致性（期望输出为 null 或空时忽略该项）；
3. 是否存在明显的事实错误或逻辑矛盾。
只输出 JSON：{"passed": true 或 false, "reason": "一句话理由", "confidence": 0-1 之间的小数表示判定置信度}。`
```

- [ ] **Step 4: 更新 observation rubric + applyJudge**

`internal/evaluation/application/observation_service.go`，`judgeRubric` 更新：

```go
// judgeRubric 构造单维度 judge 提示词（与 judgeAdapter 的 Complete 输出契约
// {"passed","reason","confidence"} 对齐：指示 LLM 按指定维度判定 pass/不通过、
// 给理由并给出 0-1 置信度，与评测系统主 rubric 逐字对齐）。
func judgeRubric(dimension string) string {
    return fmt.Sprintf("请按维度「%s」对助手回答判定通过/不通过，给出理由与 0-1 置信度。忠实于给定上下文、切题、覆盖全部关键点。"+
        "只输出 JSON：{\"passed\": true 或 false, \"reason\": \"一句话理由\", \"confidence\": 0-1 之间的小数表示判定置信度}", dimension)
}
```

`applyJudge` 内填充 JudgeSignal 处更新（原注释 TODO 一并移除）：

```go
        // LLMJudge 契约返回 domain.AssertionResult{Passed, Message, Confidence}：
        // 维度通过映射为 1.0/0.0，Confidence/Reason 用 judge 真实输出（P1c §6.2）。
        score := 0.0
        if res.Passed {
            score = 1.0
        }
        obs.Signals.Judge = append(obs.Signals.Judge, domain.JudgeSignal{
            Dimension:  dimension,
            Score:      score,
            Confidence: res.Confidence,
            Reason:     res.Message,
        })
```

- [ ] **Step 5: 重构 judgeCase/runCase 返回 assertion + 生成 ID**

`internal/evaluation/application/service.go`：

`runCase` 签名与 judge 分支更新：

```go
func (s *Service) runCase(
    ctx context.Context, tenantID, requestedBy string, ref domain.ResourceRef, runID string, testCase domain.EvalCase,
) domain.EvalCaseResult {
    execution, err := s.adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
    result := domain.EvalCaseResult{ID: uuid.Must(uuid.NewV7()).String(), CaseID: testCase.ID}
    if err != nil {
        result.Error = err.Error()
        return result
    }
    // ...（result.Actual / TraceID / Tokens / CostUSD / DurationMs / RAGEvidence 赋值保持不变）
    // ...（trace evidence resolve 块保持不变）

    // Judge assertions dispatch to the LLM judge port; rule assertions stay
    // in the domain's pure EvaluateAssertion.
    if testCase.AssertionMode == domain.AssertionJudge {
        assertion, result := s.judgeCase(ctx, testCase, result)
        // 评审池内联触发（P1c §6.6）：仅 judge 实际产出判定（result.Error 为空）时
        // 才可能入池；judge 关闭/故障是基础设施失败，不是评审信号。
        if result.Error == "" {
            s.escalateCaseResult(ctx, tenantID, runID, result, testCase, assertion)
        }
        return result
    }

    assertion, err := domain.EvaluateAssertion(testCase.AssertionMode, execution.Output, testCase.ExpectedOutput)
    if err != nil {
        result.Error = err.Error()
        return result
    }
    result.Passed = assertion.Passed
    result.Message = assertion.Message
    return result
}
```

`judgeCase` 改为返回 `(domain.AssertionResult, domain.EvalCaseResult)`：

```go
// judgeCase runs the LLM judge assertion for a judge case. Fail-closed: a
// nil or disabled judge makes the case fail with an explicit error instead
// of a silent pass. It returns the raw assertion (for review-pool escalation)
// alongside the result.
func (s *Service) judgeCase(ctx context.Context, testCase domain.EvalCase, result domain.EvalCaseResult) (domain.AssertionResult, domain.EvalCaseResult) {
    var zero domain.AssertionResult
    if s.judge == nil || !s.judge.Enabled(ctx) {
        result.Error = "LLM judge disabled"
        return zero, result
    }
    inputJSON, err := json.Marshal(testCase.Input)
    if err != nil {
        result.Error = fmt.Errorf("judge: marshal input: %w", err).Error()
        return zero, result
    }
    expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
    if err != nil {
        result.Error = fmt.Errorf("judge: marshal expected output: %w", err).Error()
        return zero, result
    }
    actualJSON, err := json.Marshal(result.Actual)
    if err != nil {
        result.Error = fmt.Errorf("judge: marshal actual output: %w", err).Error()
        return zero, result
    }
    var spec domain.JudgeSpec
    if testCase.JudgeSpec != nil {
        spec = *testCase.JudgeSpec
    }
    assertion, err := s.judge.Judge(ctx, port.JudgeRequest{
        Model:          spec.Model,
        Rubric:         spec.Rubric,
        Input:          string(inputJSON),
        ExpectedOutput: string(expectedJSON),
        Actual:         string(actualJSON),
    })
    if err != nil {
        result.Error = err.Error()
        return zero, result
    }
    result.Passed = assertion.Passed
    result.Message = assertion.Message
    return assertion, result
}
```

`Run` 调用点传入 runID，`Service` struct 增加 review 字段与 setter（供 Task 9 注入）：

```go
type Service struct {
    adapter     port.ResourceAdapter
    repo        port.RunRepository
    suites      port.SuiteRepository
    traceReader port.TraceEvidenceReader
    judge       port.LLMJudge
    // review 是人工评审池升级入口（P1c §6.6 内联触发）；nil 时评审升级静默跳过
    // （fail-open，评审池未装配不阻断评测执行）。
    review      port.ReviewEscalator
    reviewCfg   domain.ReviewConfig
}

// SetReviewEscalator 注入评审池升级器（wiring 在 NewService 之后调用）。
func (s *Service) SetReviewEscalator(e port.ReviewEscalator, cfg domain.ReviewConfig) {
    s.review = e
    s.reviewCfg = cfg
}

// escalateCaseResult 计算评测集触发原因并逐条入池（fail-open：失败仅日志+指标，
// 不阻断评测流程）。escalateObservation 在 Task 9 一并补上。
func (s *Service) escalateCaseResult(
    ctx context.Context, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult,
) {
    if s.review == nil {
        return
    }
    if err := s.review.TryEscalateCaseResult(ctx, tenantID, runID, result, c, assertion); err != nil {
        s.logReviewEscalateError(ctx, err)
    }
}

func (s *Service) logReviewEscalateError(ctx context.Context, err error) {
    // 占位：本文件暂无 logger。Task 9 引入 zap 后改为 Logger.Warn；当前仅返回上游。
    _ = ctx
    _ = err
}
```

（`logReviewEscalateError` 占位在 Task 9 中被真实 logger 替换；`escalateCaseResult` 不显式打日志可先留注释，避免引入未用 import。实现者可在 Service struct 中加 `logger *zap.Logger` 字段并在 `NewService` 设置 `zap.NewNop()` 兜底，直接在此处使用——这是更干净的做法，优先采用：`Service` 增加 `logger *zap.Logger`，`NewService` 末尾 `logger: zap.NewNop()`，`logReviewEscalateError` 实现为 `s.logger.Warn("evaluation review escalation failed", zap.Error(err))`。）

`Run` 循环调用更新：

```go
        result := s.runCase(ctx, input.TenantID, input.RequestedBy, input.Resource, run.ID, testCase)
```

- [ ] **Step 6: SaveRun 采用 result.ID**

`internal/evaluation/infrastructure/persistence/run_repository.go`，循环插入处（原 `uuid.Must(uuid.NewV7()).String()`）改为：

```go
        id := result.ID
        if id == "" {
            id = uuid.Must(uuid.NewV7()).String()
        }
```

并让 INSERT 用 `id` 变量（其余绑定值不变）。

- [ ] **Step 7: 运行测试 + 契约自检**

Run: `go test ./api/wiring/ -run TestParseJudgeResponseConfidence && go test ./internal/evaluation/... && go test ./api/http/ -run TestContracts`
Expected: 新用例 PASS；`internal/evaluation/application` 既有测试因 `runCase` 签名变更而编译失败——同步修改测试调用点（`service_test.go` 中调用 `runCase` 处补 `runID` 参数，传 `"run-1"`），规则断言路径行为不变。`TestContracts` 保持 PASS——`EvalCaseResult.ID` 是 domain 实体字段，只经 run_repository JSONB 落库、不经 HTTP DTO 序列化，故黄金文件无需 `make record-contracts`；若它意外失败，先核对 `EvalCaseResult` 是否被引入 API 响应，再决定是否 `make record-contracts`。

- [ ] **Step 8: Commit**

```bash
git add api/wiring/evaluation.go internal/evaluation/application/service.go internal/evaluation/application/observation_service.go internal/evaluation/infrastructure/persistence/run_repository.go internal/evaluation/application/service_test.go
git commit -m "feat(evaluation): judge 输出真实置信度并重构 runCase 生成结果 ID

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: domain 评审池类型与触发纯函数

**Files:**

- Create: `internal/evaluation/domain/review_pool.go`
- Test: `internal/evaluation/domain/review_pool_test.go`（追加到 Task 1 建的文件）

**Interfaces:**

- Consumes: `JudgeSignal`、`EvalObservation`、`AssertionResult`、`ResourceKind`（Task 1 已就绪）。
- Produces:
  - `type HumanVerdict string` + 常量 `VerdictPass/VerdictFail/VerdictJudgeMisjudgment/VerdictCaseRevision`
  - `type ReviewTriggerReason string` + 常量 `TriggerLowConfidence/TriggerDimensionSplit/TriggerJudgeRuleConflict/TriggerNeedsReview`
  - `type ReviewSourceType string` + 常量 `ReviewSourceObservation/ReviewSourceCaseResult`
  - `type ReviewItemStatus string` + 常量 `ReviewStatusPending/ReviewStatusReviewed`
  - `type ReviewConfig struct{ LowConfidenceThreshold, JudgePassThreshold float64 }`
  - `type ReviewItem struct{...}`、`type CalibrationSample struct{...}`、`type AttributionEntry struct{...}`
  - `func TriggersForObservation(obs *EvalObservation, cfg ReviewConfig) []ReviewTriggerReason`
  - `func TriggersForCaseResult(needsReview bool, assertion AssertionResult, cfg ReviewConfig) []ReviewTriggerReason`
  - `func (v HumanVerdict) Valid() bool`、`func (r ReviewTriggerReason) Valid() bool`

- [ ] **Step 1: 写触发规则测试**

追加到 `internal/evaluation/domain/review_pool_test.go`：

```go
func TestTriggersForObservation(t *testing.T) {
    cfg := ReviewConfig{LowConfidenceThreshold: 0.6, JudgePassThreshold: 0.5}
    obs := func() *EvalObservation {
        return &EvalObservation{Resource: ObservationResourceRef{Kind: ResourceKindSkill, ResourceID: "s1"}}
    }

    t.Run("no judge signals yields no triggers", func(t *testing.T) {
        if got := TriggersForObservation(obs(), cfg); len(got) != 0 {
            t.Fatalf("got %v, want none", got)
        }
    })

    t.Run("low confidence triggers", func(t *testing.T) {
        o := obs()
        o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.4}}
        if got := TriggersForObservation(o, cfg); len(got) != 1 || got[0] != TriggerLowConfidence {
            t.Fatalf("got %v, want [low_confidence]", got)
        }
    })

    t.Run("dimension split triggers", func(t *testing.T) {
        o := obs()
        o.Signals.Judge = []JudgeSignal{
            {Dimension: "faithfulness", Score: 1.0, Confidence: 0.9},
            {Dimension: "relevance", Score: 0.2, Confidence: 0.9},
        }
        if got := TriggersForObservation(o, cfg); !containsReason(got, TriggerDimensionSplit) {
            t.Fatalf("got %v, want dimension_split present", got)
        }
    })

    t.Run("rule conflict triggers only when all judge pass and verdict block", func(t *testing.T) {
        o := obs()
        o.Verdict = VerdictBlock
        o.Signals.Rule = []RuleSignal{{Rule: "r1"}}
        o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9}}
        got := TriggersForObservation(o, cfg)
        if !containsReason(got, TriggerJudgeRuleConflict) {
            t.Fatalf("got %v, want judge_rule_conflict present", got)
        }
    })

    t.Run("rule conflict suppressed when judge below threshold", func(t *testing.T) {
        o := obs()
        o.Verdict = VerdictBlock
        o.Signals.Rule = []RuleSignal{{Rule: "r1"}}
        o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 0.2, Confidence: 0.9}}
        if got := TriggersForObservation(o, cfg); containsReason(got, TriggerJudgeRuleConflict) {
            t.Fatalf("got %v, want no judge_rule_conflict", got)
        }
    })

    t.Run("nil observation yields no triggers", func(t *testing.T) {
        if got := TriggersForObservation(nil, cfg); len(got) != 0 {
            t.Fatalf("got %v, want none", got)
        }
    })
}

func TestTriggersForCaseResult(t *testing.T) {
    cfg := ReviewConfig{LowConfidenceThreshold: 0.6}
    passing := AssertionResult{Passed: true, Confidence: 0.9}

    t.Run("passing assertion yields no triggers", func(t *testing.T) {
        if got := TriggersForCaseResult(false, passing, cfg); len(got) != 0 {
            t.Fatalf("got %v, want none", got)
        }
    })

    t.Run("needs review triggers", func(t *testing.T) {
        got := TriggersForCaseResult(true, passing, cfg)
        if !containsReason(got, TriggerNeedsReview) {
            t.Fatalf("got %v, want needs_review present", got)
        }
    })

    t.Run("low confidence triggers", func(t *testing.T) {
        got := TriggersForCaseResult(false, AssertionResult{Passed: true, Confidence: 0.3}, cfg)
        if !containsReason(got, TriggerLowConfidence) {
            t.Fatalf("got %v, want low_confidence present", got)
        }
    })

    t.Run("both triggers coexist", func(t *testing.T) {
        got := TriggersForCaseResult(true, AssertionResult{Passed: false, Confidence: 0.2}, cfg)
        if !containsReason(got, TriggerNeedsReview) || !containsReason(got, TriggerLowConfidence) {
            t.Fatalf("got %v, want needs_review + low_confidence", got)
        }
    })
}

func containsReason(got []ReviewTriggerReason, want ReviewTriggerReason) bool {
    for _, g := range got {
        if g == want {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/domain/ -run 'TestTriggersFor'`
Expected: FAIL — 编译错误 `undefined: ReviewConfig`。

- [ ] **Step 3: 实现 review_pool.go**

新建 `internal/evaluation/domain/review_pool.go`：

```go
package domain

import "time"

// HumanVerdict 人工评审结论 4 分类（spec §6.6 回写动作）。
type HumanVerdict string

const (
    VerdictPass             HumanVerdict = "pass"
    VerdictFail             HumanVerdict = "fail"
    VerdictJudgeMisjudgment HumanVerdict = "judge_misjudgment"
    VerdictCaseRevision     HumanVerdict = "case_revision"
)

// Valid 校验人工结论枚举。
func (v HumanVerdict) Valid() bool {
    switch v {
    case VerdictPass, VerdictFail, VerdictJudgeMisjudgment, VerdictCaseRevision:
        return true
    default:
        return false
    }
}

// ReviewTriggerReason 入池原因（硬编码规则，AI 不做控制决策）。
type ReviewTriggerReason string

const (
    TriggerLowConfidence     ReviewTriggerReason = "low_confidence"
    TriggerDimensionSplit    ReviewTriggerReason = "dimension_split"
    TriggerJudgeRuleConflict ReviewTriggerReason = "judge_rule_conflict"
    TriggerNeedsReview       ReviewTriggerReason = "needs_review"
)

// Valid 校验入池原因枚举。
func (r ReviewTriggerReason) Valid() bool {
    switch r {
    case TriggerLowConfidence, TriggerDimensionSplit, TriggerJudgeRuleConflict, TriggerNeedsReview:
        return true
    default:
        return false
    }
}

// ReviewSourceType 评审条目来源。
type ReviewSourceType string

const (
    ReviewSourceObservation ReviewSourceType = "observation"
    ReviewSourceCaseResult  ReviewSourceType = "case_result"
)

// ReviewItemStatus 评审条目状态。
type ReviewItemStatus string

const (
    ReviewStatusPending  ReviewItemStatus = "pending"
    ReviewStatusReviewed ReviewItemStatus = "reviewed"
)

// ReviewConfig 评审池触发配置。默认值在 pkg/constants（Task 1），wiring 组装。
type ReviewConfig struct {
    // LowConfidenceThreshold 是 low_confidence 触发阈值（默认 constants.ReviewLowConfidenceThreshold）。
    LowConfidenceThreshold float64
    // JudgePassThreshold 是维度通过/跌阈分界（沿用 constants.JudgeBelowThreshold）。
    JudgePassThreshold float64
}

// ReviewItem 评审池条目（对应 eval_review_items）。
type ReviewItem struct {
    ID            string             `json:"id"`
    SourceType    ReviewSourceType   `json:"source_type"`
    SourceID      string             `json:"source_id"`
    RunID         string             `json:"run_id,omitempty"`
    TraceID       string             `json:"trace_id,omitempty"`
    ResourceKind  ResourceKind       `json:"resource_kind"`
    ResourceID    string             `json:"resource_id"`
    TriggerReason ReviewTriggerReason `json:"trigger_reason"`
    Snapshot      any                `json:"snapshot"`
    Status        ReviewItemStatus   `json:"status"`
    HumanVerdict  HumanVerdict       `json:"human_verdict,omitempty"`
    Reviewer      string             `json:"reviewer,omitempty"`
    ReviewReason  string             `json:"review_reason,omitempty"`
    CreatedAt     time.Time          `json:"created_at"`
    ReviewedAt    *time.Time         `json:"reviewed_at,omitempty"`
}

// CalibrationSample judge 误判校准样本（对应 eval_calibration_samples）。
type CalibrationSample struct {
    ID           string           `json:"id"`
    ReviewItemID string           `json:"review_item_id"`
    SourceType   ReviewSourceType `json:"source_type"`
    SourceID     string           `json:"source_id"`
    JudgeModel   string           `json:"judge_model,omitempty"`
    Signals      any              `json:"signals"`
    HumanVerdict HumanVerdict     `json:"human_verdict"`
    Reviewer     string           `json:"reviewer"`
    Reason       string           `json:"reason,omitempty"`
    CreatedAt    time.Time        `json:"created_at"`
}

// AttributionEntry 产品缺陷归因条目（对应 eval_attribution_entries，轻量记录）。
type AttributionEntry struct {
    ID           string           `json:"id"`
    ReviewItemID string           `json:"review_item_id"`
    SourceType   ReviewSourceType `json:"source_type"`
    SourceID     string           `json:"source_id"`
    ResourceKind ResourceKind     `json:"resource_kind"`
    ResourceID   string           `json:"resource_id"`
    Dimension    string           `json:"dimension,omitempty"`
    Snapshot     any              `json:"snapshot"`
    Status       string           `json:"status"`
    Reviewer     string           `json:"reviewer"`
    Reason       string           `json:"reason,omitempty"`
    CreatedAt    time.Time        `json:"created_at"`
}

// TriggersForObservation 计算观测应入池的触发原因（空 = 不进池）。纯函数，硬编码规则。
// 规则（spec §6.6）：
//  1. low_confidence：任一 judge 维度 Confidence < cfg.LowConfidenceThreshold；
//  2. dimension_split：存在 Score >= JudgePassThreshold 且存在 Score < JudgePassThreshold；
//  3. judge_rule_conflict：规则命中（Signals.Rule 非空）+ Verdict == block + 全部维度 pass。
func TriggersForObservation(obs *EvalObservation, cfg ReviewConfig) []ReviewTriggerReason {
    if obs == nil {
        return nil
    }
    judge := obs.Signals.Judge
    if len(judge) == 0 {
        return nil
    }
    var triggers []ReviewTriggerReason
    for _, j := range judge {
        if j.Confidence < cfg.LowConfidenceThreshold {
            triggers = append(triggers, TriggerLowConfidence)
            break
        }
    }
    below, above := false, false
    for _, j := range judge {
        if j.Score < cfg.JudgePassThreshold {
            below = true
        } else {
            above = true
        }
    }
    if below && above {
        triggers = append(triggers, TriggerDimensionSplit)
    }
    if len(obs.Signals.Rule) > 0 && obs.Verdict == VerdictBlock && !below {
        triggers = append(triggers, TriggerJudgeRuleConflict)
    }
    return triggers
}

// TriggersForCaseResult 计算评测集 judge 判定的入池原因（空 = 不进池）。
// 规则（spec §6.6）：
//  1. needs_review：EvalCase.NeedsReview == true；
//  2. low_confidence：assertion.Confidence < cfg.LowConfidenceThreshold。
func TriggersForCaseResult(needsReview bool, assertion AssertionResult, cfg ReviewConfig) []ReviewTriggerReason {
    var triggers []ReviewTriggerReason
    if needsReview {
        triggers = append(triggers, TriggerNeedsReview)
    }
    if assertion.Confidence < cfg.LowConfidenceThreshold {
        triggers = append(triggers, TriggerLowConfidence)
    }
    return triggers
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/evaluation/domain/ -run 'TestTriggersFor'`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/evaluation/domain/review_pool.go internal/evaluation/domain/review_pool_test.go
git commit -m "feat(evaluation): 评审池领域类型与触发纯函数

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: port 接口（ReviewRepository + ReviewEscalator）

**Files:**

- Create: `internal/evaluation/domain/port/review.go`

**Interfaces:**

- Consumes: Task 4 的 domain 类型。
- Produces:
  - `type ReviewFilter struct{ Status domain.ReviewItemStatus; TriggerReason domain.ReviewTriggerReason; ResourceKind, ResourceID string; Limit, Offset int }`
  - `type ReviewRepository interface{ UpsertItem(ctx, tenantID string, item *domain.ReviewItem) (bool, error); GetItem(ctx, tenantID, id string) (*domain.ReviewItem, error); ListItems(ctx, tenantID string, f ReviewFilter) ([]domain.ReviewItem, int64, error); MarkReviewed(ctx, tenantID, id string, verdict domain.HumanVerdict, reviewer, reason string) error; CreateCalibrationSample(ctx, tenantID string, s *domain.CalibrationSample) error; CreateAttributionEntry(ctx, tenantID string, e *domain.AttributionEntry) error; CountPending(ctx, tenantID string) (int64, error) }`
  - `type ReviewEscalator interface{ TryEscalateObservation(ctx, tenantID string, obs *domain.EvalObservation) error; TryEscalateCaseResult(ctx, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) error }`

- [ ] **Step 1: 写接口文件（纯声明，无实现）**

新建 `internal/evaluation/domain/port/review.go`：

```go
package port

import (
    "context"

    "github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// ReviewFilter 评审池列表过滤与分页。
type ReviewFilter struct {
    Status        domain.ReviewItemStatus
    TriggerReason domain.ReviewTriggerReason
    ResourceKind  string
    ResourceID    string
    Limit         int
    Offset        int
}

// ReviewRepository 持久化评审池条目与回写副作用（tenant-scoped；所有方法显式携带
// tenantID，实现必须走 execTenantTx + postgres.WithTenant，见 CLAUDE.md DDL 规则）。
type ReviewRepository interface {
    // UpsertItem 幂等入池：同 (source_type, source_id, trigger_reason) 已存在时
    // 不插入并返回 false（UNIQUE 索引 idx_eval_review_items_dedupe 兜底）。
    UpsertItem(ctx context.Context, tenantID string, item *domain.ReviewItem) (bool, error)
    // GetItem 取单条；不存在返回 (nil, nil)。
    GetItem(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error)
    // ListItems 分页列出；返回条目与总数。
    ListItems(ctx context.Context, tenantID string, f ReviewFilter) ([]domain.ReviewItem, int64, error)
    // MarkReviewed 把条目置为 reviewed 并记录人工结论（状态机唯一写点）。
    MarkReviewed(ctx context.Context, tenantID, id string, verdict domain.HumanVerdict, reviewer, reason string) error
    // CreateCalibrationSample 沉淀 judge 误判校准样本（判 judge_misjudgment 时）。
    CreateCalibrationSample(ctx context.Context, tenantID string, s *domain.CalibrationSample) error
    // CreateAttributionEntry 落产品缺陷归因条目（判 fail / case_revision 时）。
    CreateAttributionEntry(ctx context.Context, tenantID string, e *domain.AttributionEntry) error
    // CountPending 统计待评审条目数（eval_review_backlog Gauge 数据源）。
    CountPending(ctx context.Context, tenantID string) (int64, error)
}

// ReviewEscalator 评审池内联触发入口（观测落库 / 评测集判定后调用，fail-open：
// 实现方不得用返回错误阻断主流程，主流程侧必须忽略升级错误）。
type ReviewEscalator interface {
    // TryEscalateObservation 判定观测是否入池并幂等落条目。返回错误表示升级失败
    // （调用方记日志 + IncEvalReviewEscalateFailure，不得阻断主流程）。
    TryEscalateObservation(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
    // TryEscalateCaseResult 判定评测集 judge 结果是否入池并幂等落条目。
    TryEscalateCaseResult(ctx context.Context, tenantID, runID string,
        result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) error
}
```

- [ ] **Step 2: 编译验证接口可被引用**

Run: `go build ./internal/evaluation/...`
Expected: PASS（纯接口声明无实现，引用方 Task 6/7/9 使用）。

- [ ] **Step 3: Commit**

```bash
git add internal/evaluation/domain/port/review.go
git commit -m "feat(evaluation): 评审池 port 接口定义

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: PgReviewRepository 基础设施

**Files:**

- Create: `internal/evaluation/infrastructure/persistence/review_repository.go`
- Test: `internal/evaluation/infrastructure/persistence/review_repository_test.go`（集成测试，仿 observation_repository_test.go 模式）

**Interfaces:**

- Consumes: `port.ReviewRepository`、`port.ReviewFilter`（Task 5）；`poolIface`（本包既有）。
- Produces: `type PgReviewRepository struct` + `func NewPgReviewRepository(pool poolIface) *PgReviewRepository`；编译期断言 `var _ port.ReviewRepository = (*PgReviewRepository)(nil)`。

- [ ] **Step 1: 写集成测试**

新建 `review_repository_test.go`（沿用 mock_test.go 的 pgxmock 基建 `newMockRepo(t)` + `expectTenantTx(mock)`，与 observation_repository_test.go 同模式）：

```go
package persistence

import (
    "context"
    "regexp"
    "testing"
    "time"

    "github.com/byteBuilderX/stratum/internal/evaluation/domain"
    "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
    "github.com/pashagolub/pgxmock/v2"
)

// reviewItem 是测试用评审条目（不变字段集中定义，各测试只覆盖关注点）。
func reviewItem(id, sourceType, sourceID string, reason domain.ReviewTriggerReason) *domain.ReviewItem {
    return &domain.ReviewItem{
        ID:            id,
        SourceType:    domain.ReviewSourceType(sourceType),
        SourceID:      sourceID,
        RunID:         "run-" + sourceID,
        TraceID:       "t-" + sourceID,
        ResourceKind:  domain.ResourceKindSkill,
        ResourceID:    "s1",
        TriggerReason: reason,
        Snapshot:      map[string]any{"note": "x"},
        Status:        domain.ReviewStatusPending,
        CreatedAt:     time.Now().UTC(),
    }
}

func TestPgReviewRepositoryUpsertItemIdempotent(t *testing.T) {
    mock := newMockRepo(t)
    repo := NewPgReviewRepository(mock)
    item := reviewItem("ri-1", "observation", "obs-1", domain.TriggerLowConfidence)

    // 第一次：同 key 无冲突，RowsAffected=1 → inserted=true。
    expectTenantTx(mock)
    mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO eval_review_items`)).
        WithArgs("ri-1", "observation", "obs-1", "run-obs-1", "t-obs-1", "skill", "s1",
            "low_confidence", pgxmock.AnyArg(), "pending", item.CreatedAt).
        WillReturnResult(pgxmock.NewResult("INSERT", 1))
    mock.ExpectCommit()
    inserted, err := repo.UpsertItem(context.Background(), "t1", item)
    if err != nil {
        t.Fatalf("upsert: %v", err)
    }
    if !inserted {
        t.Fatal("want inserted on first upsert")
    }

    // 第二次：DB 对同 (source_type, source_id, trigger_reason) ON CONFLICT DO NOTHING
    // 返回 RowsAffected=0 → inserted=false（幂等）。
    expectTenantTx(mock)
    mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO eval_review_items`)).
        WithArgs("ri-1", "observation", "obs-1", "run-obs-1", "t-obs-1", "skill", "s1",
            "low_confidence", pgxmock.AnyArg(), "pending", item.CreatedAt).
        WillReturnResult(pgxmock.NewResult("INSERT", 0))
    mock.ExpectCommit()
    again, err := repo.UpsertItem(context.Background(), "t1", item)
    if err != nil {
        t.Fatalf("upsert again: %v", err)
    }
    if again {
        t.Fatal("want no insert on duplicate key")
    }
    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations not met: %v", err)
    }
}

func TestPgReviewRepositoryGetItem(t *testing.T) {
    mock := newMockRepo(t)
    repo := NewPgReviewRepository(mock)
    expectTenantTx(mock)
    rows := pgxmock.NewRows([]string{"id", "source_type", "source_id", "run_id", "trace_id",
        "resource_kind", "resource_id", "trigger_reason", "snapshot", "status",
        "human_verdict", "reviewer", "review_reason", "created_at", "reviewed_at"}).
        AddRow("ri-1", "observation", "obs-1", "run-obs-1", "t-obs-1", "skill", "s1",
            "low_confidence", `{"note":"x"}`, "pending", "", "", "", time.Now().UTC(), nil)
    mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id, trigger_reason, snapshot, status, human_verdict, reviewer, review_reason, created_at, reviewed_at FROM eval_review_items WHERE id = $1`)).
        WithArgs("ri-1").
        WillReturnRows(rows)
    mock.ExpectCommit()

    got, err := repo.GetItem(context.Background(), "t1", "ri-1")
    if err != nil {
        t.Fatalf("get: %v", err)
    }
    if got == nil || got.TriggerReason != domain.TriggerLowConfidence || got.Status != domain.ReviewStatusPending {
        t.Fatalf("unexpected item: %+v", got)
    }
    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations not met: %v", err)
    }
}

func TestPgReviewRepositoryMarkReviewedAndCountPending(t *testing.T) {
    mock := newMockRepo(t)
    repo := NewPgReviewRepository(mock)
    item := reviewItem("ri-2", "case_result", "cr-2", domain.TriggerNeedsReview)

    expectTenantTx(mock)
    mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO eval_review_items`)).
        WithArgs("ri-2", "case_result", "cr-2", "run-cr-2", "t-cr-2", "skill", "s1",
            "needs_review", pgxmock.AnyArg(), "pending", item.CreatedAt).
        WillReturnResult(pgxmock.NewResult("INSERT", 1))
    mock.ExpectCommit()
    if _, err := repo.UpsertItem(context.Background(), "t1", item); err != nil {
        t.Fatalf("upsert: %v", err)
    }

    expectTenantTx(mock)
    mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM eval_review_items WHERE status = 'pending'`)).
        WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(1)))
    mock.ExpectCommit()
    n, err := repo.CountPending(context.Background(), "t1")
    if err != nil {
        t.Fatalf("count pending: %v", err)
    }
    if n != 1 {
        t.Fatalf("pending = %d, want 1", n)
    }

    expectTenantTx(mock)
    mock.ExpectExec(regexp.QuoteMeta(`UPDATE eval_review_items`)).
        WithArgs("ri-2", "fail", "reviewer@x", "错误输出").
        WillReturnResult(pgxmock.NewResult("UPDATE", 1))
    mock.ExpectCommit()
    if err := repo.MarkReviewed(context.Background(), "t1", item.ID, domain.VerdictFail, "reviewer@x", "错误输出"); err != nil {
        t.Fatalf("mark reviewed: %v", err)
    }

    expectTenantTx(mock)
    mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM eval_review_items WHERE status = 'pending'`)).
        WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(0)))
    mock.ExpectCommit()
    n, err = repo.CountPending(context.Background(), "t1")
    if err != nil {
        t.Fatalf("count pending after review: %v", err)
    }
    if n != 0 {
        t.Fatalf("pending = %d, want 0", n)
    }
    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations not met: %v", err)
    }
}

func TestPgReviewRepositoryListItems(t *testing.T) {
    mock := newMockRepo(t)
    repo := NewPgReviewRepository(mock)
    now := time.Now().UTC()

    // ListItems 无 filter：list 查询 + count 查询。created_at DESC 排序由 SQL 负责。
    expectTenantTx(mock)
    listRows := pgxmock.NewRows([]string{"id", "source_type", "source_id", "run_id", "trace_id",
        "resource_kind", "resource_id", "trigger_reason", "snapshot", "status",
        "human_verdict", "reviewer", "review_reason", "created_at", "reviewed_at"}).
        AddRow("ri-1", "observation", "obs-1", "run-obs-1", "t-obs-1", "skill", "s1",
            "low_confidence", `{"note":"x"}`, "pending", "", "", "", now, nil).
        AddRow("ri-2", "case_result", "cr-2", "run-cr-2", "t-cr-2", "skill", "s1",
            "dimension_split", `{"note":"x"}`, "pending", "", "", "", now, nil)
    mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id, trigger_reason, snapshot, status, human_verdict, reviewer, review_reason, created_at, reviewed_at FROM eval_review_items WHERE 1=1 ORDER BY created_at DESC LIMIT $1 OFFSET $2`)).
        WithArgs(10, 0).
        WillReturnRows(listRows)
    mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM eval_review_items WHERE 1=1`)).
        WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(2)))
    mock.ExpectCommit()

    items, total, err := repo.ListItems(context.Background(), "t1", port.ReviewFilter{Limit: 10, Offset: 0})
    if err != nil {
        t.Fatalf("list: %v", err)
    }
    if total != 2 || len(items) != 2 {
        t.Fatalf("total=%d len=%d, want 2/2", total, len(items))
    }
    if items[0].TriggerReason != domain.TriggerLowConfidence {
        t.Fatalf("first item reason = %v, want low_confidence", items[0].TriggerReason)
    }
    if err := mock.ExpectationsWereMet(); err != nil {
        t.Fatalf("expectations not met: %v", err)
    }
}
```

（实现者沿用 `mock_test.go` 的 `newMockRepo(t)` 与 `expectTenantTx(mock)` 基建；测试中的列清单、参数顺序必须与 `review_repository.go` 的 SQL 一一对应。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/infrastructure/persistence/ -run TestPgReviewRepository`
Expected: FAIL — `undefined: NewPgReviewRepository`。

- [ ] **Step 3: 实现 PgReviewRepository**

新建 `review_repository.go`（复制 observation_repository 的 `postgres.WithTenant` + `execTenantTx` 模式）：

```go
package persistence

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "github.com/byteBuilderX/stratum/internal/evaluation/domain"
    "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
    "github.com/byteBuilderX/stratum/pkg/constants"
    "github.com/byteBuilderX/stratum/pkg/storage/postgres"
    "github.com/jackc/pgx/v5"
)

// PgReviewRepository 实现 port.ReviewRepository（tenant-scoped）。
type PgReviewRepository struct {
    pool poolIface
}

// 编译期断言：PgReviewRepository 满足 port.ReviewRepository。
var _ port.ReviewRepository = (*PgReviewRepository)(nil)

func NewPgReviewRepository(pool poolIface) *PgReviewRepository {
    return &PgReviewRepository{pool: pool}
}

func (r *PgReviewRepository) UpsertItem(ctx context.Context, tenantID string, item *domain.ReviewItem) (bool, error) {
    snapshotJSON, err := json.Marshal(item.Snapshot)
    if err != nil {
        return false, fmt.Errorf("marshal review snapshot: %w", err)
    }
    inserted := false
    ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
    err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        tag, execErr := tx.Exec(ctx,
            `INSERT INTO eval_review_items
             (id, source_type, source_id, run_id, trace_id, resource_kind, resource_id,
              trigger_reason, snapshot, status, created_at)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
             ON CONFLICT (source_type, source_id, trigger_reason) DO NOTHING`,
            item.ID, string(item.SourceType), item.SourceID, item.RunID, item.TraceID,
            string(item.ResourceKind), item.ResourceID, string(item.TriggerReason),
            string(snapshotJSON), string(item.Status), item.CreatedAt,
        )
        if execErr != nil {
            return fmt.Errorf("insert eval review item: %w", execErr)
        }
        inserted = tag.RowsAffected() == 1
        return nil
    })
    if err != nil {
        return false, fmt.Errorf("upsert eval review item: %w", err)
    }
    return inserted, nil
}

func (r *PgReviewRepository) GetItem(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error) {
    var (
        item                       domain.ReviewItem
        sourceType, trigger, status string
        verdict                    string
        snapshotJSON               string
        reviewedAt                 *time.Time
    )
    ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
    err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        return tx.QueryRow(ctx,
            `SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id,
                    trigger_reason, snapshot, status, human_verdict, reviewer, review_reason,
                    created_at, reviewed_at
             FROM eval_review_items WHERE id = $1`, id,
        ).Scan(&item.ID, &sourceType, &item.SourceID, &item.RunID, &item.TraceID,
            &item.ResourceKind, &item.ResourceID, &trigger, &snapshotJSON, &status,
            &verdict, &item.Reviewer, &item.ReviewReason, &item.CreatedAt, &reviewedAt)
    })
    if err != nil {
        if err == pgx.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("get eval review item %s: %w", id, err)
    }
    item.SourceType = domain.ReviewSourceType(sourceType)
    item.TriggerReason = domain.ReviewTriggerReason(trigger)
    item.Status = domain.ReviewItemStatus(status)
    item.HumanVerdict = domain.HumanVerdict(verdict)
    item.ReviewedAt = reviewedAt
    if err := json.Unmarshal([]byte(snapshotJSON), &item.Snapshot); err != nil {
        return nil, fmt.Errorf("get eval review item %s: decode snapshot: %w", id, err)
    }
    return &item, nil
}

func (r *PgReviewRepository) ListItems(ctx context.Context, tenantID string, f port.ReviewFilter) ([]domain.ReviewItem, int64, error) {
    conds, args := reviewFilterConds(f)
    countSQL := `SELECT COUNT(*) FROM eval_review_items` + conds
    listSQL := `SELECT id, source_type, source_id, run_id, trace_id, resource_kind, resource_id,
                       trigger_reason, snapshot, status, human_verdict, reviewer, review_reason,
                       created_at, reviewed_at
                FROM eval_review_items` + conds +
        fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)

    limit := f.Limit
    if limit <= 0 {
        limit = constants.DefaultPageSize
    }
    if limit > constants.MaxPageSize {
        limit = constants.MaxPageSize
    }
    if f.Offset < 0 {
        f.Offset = 0
    }
    queryArgs := append(append([]any{}, args...), limit, f.Offset)

    var (
        items                      []domain.ReviewItem
        total                      int64
        sourceType, trigger, status string
        verdict                    string
        snapshotJSON               string
        reviewedAt                 *time.Time
    )
    ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
    err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        rows, execErr := tx.Query(ctx, listSQL, queryArgs...)
        if execErr != nil {
            return fmt.Errorf("list eval review items: %w", execErr)
        }
        defer rows.Close()
        for rows.Next() {
            var item domain.ReviewItem
            if scanErr := rows.Scan(&item.ID, &sourceType, &item.SourceID, &item.RunID, &item.TraceID,
                &item.ResourceKind, &item.ResourceID, &trigger, &snapshotJSON, &status,
                &verdict, &item.Reviewer, &item.ReviewReason, &item.CreatedAt, &reviewedAt); scanErr != nil {
                return fmt.Errorf("scan eval review item: %w", scanErr)
            }
            item.SourceType = domain.ReviewSourceType(sourceType)
            item.TriggerReason = domain.ReviewTriggerReason(trigger)
            item.Status = domain.ReviewItemStatus(status)
            item.HumanVerdict = domain.HumanVerdict(verdict)
            item.ReviewedAt = reviewedAt
            if decodeErr := json.Unmarshal([]byte(snapshotJSON), &item.Snapshot); decodeErr != nil {
                return fmt.Errorf("decode review snapshot: %w", decodeErr)
            }
            items = append(items, item)
        }
        if rowsErr := rows.Err(); rowsErr != nil {
            return fmt.Errorf("iterate eval review items: %w", rowsErr)
        }
        return tx.QueryRow(ctx, countSQL, args...).Scan(&total)
    })
    if err != nil {
        return nil, 0, fmt.Errorf("list eval review items: %w", err)
    }
    if items == nil {
        items = []domain.ReviewItem{}
    }
    return items, total, nil
}

// reviewFilterConds 把 ReviewFilter 编译为 WHERE 子句与绑定参数；list 与 count
// 两段 SQL 复用同一条件，保证总数与行集口径一致。
func reviewFilterConds(f port.ReviewFilter) (string, []any) {
    var conds []string
    var args []any
    if f.Status != "" {
        conds = append(conds, fmt.Sprintf(" status = $%d", len(args)+1))
        args = append(args, string(f.Status))
    }
    if f.TriggerReason != "" {
        conds = append(conds, fmt.Sprintf(" trigger_reason = $%d", len(args)+1))
        args = append(args, string(f.TriggerReason))
    }
    if f.ResourceKind != "" {
        conds = append(conds, fmt.Sprintf(" resource_kind = $%d", len(args)+1))
        args = append(args, f.ResourceKind)
    }
    if f.ResourceID != "" {
        conds = append(conds, fmt.Sprintf(" resource_id = $%d", len(args)+1))
        args = append(args, f.ResourceID)
    }
    if len(conds) == 0 {
        return ` WHERE 1=1`, args
    }
    return ` WHERE` + strings.Join(conds, " AND"), args
}
```

`MarkReviewed`：

```go
func (r *PgReviewRepository) MarkReviewed(
	ctx context.Context, tenantID, id string, verdict domain.HumanVerdict, reviewer, reason string,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, execErr := tx.Exec(ctx,
			`UPDATE eval_review_items
			 SET status = 'reviewed', human_verdict = $2, reviewer = $3, review_reason = $4, reviewed_at = NOW()
			 WHERE id = $1 AND status = 'pending'`,
			id, string(verdict), reviewer, reason,
		)
		if execErr != nil {
			return fmt.Errorf("mark eval review item reviewed: %w", execErr)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("eval review item %s not pending (or missing)", id)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("mark eval review item reviewed: %w", err)
	}
	return nil
}

func (r *PgReviewRepository) CreateCalibrationSample(ctx context.Context, tenantID string, s *domain.CalibrationSample) error {
	signalsJSON, err := json.Marshal(s.Signals)
	if err != nil {
		return fmt.Errorf("marshal calibration signals: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO eval_calibration_samples
			 (id, review_item_id, source_type, source_id, judge_model, signals, human_verdict, reviewer, reason, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			s.ID, s.ReviewItemID, string(s.SourceType), s.SourceID, s.JudgeModel,
			string(signalsJSON), string(s.HumanVerdict), s.Reviewer, s.Reason, s.CreatedAt,
		)
		if execErr != nil {
			return fmt.Errorf("insert calibration sample: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create calibration sample: %w", err)
	}
	return nil
}

func (r *PgReviewRepository) CreateAttributionEntry(ctx context.Context, tenantID string, e *domain.AttributionEntry) error {
	snapshotJSON, err := json.Marshal(e.Snapshot)
	if err != nil {
		return fmt.Errorf("marshal attribution snapshot: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO eval_attribution_entries
			 (id, review_item_id, source_type, source_id, resource_kind, resource_id,
			  dimension, snapshot, status, reviewer, reason, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			e.ID, e.ReviewItemID, string(e.SourceType), e.SourceID,
			string(e.ResourceKind), e.ResourceID, e.Dimension,
			string(snapshotJSON), e.Status, e.Reviewer, e.Reason, e.CreatedAt,
		)
		if execErr != nil {
			return fmt.Errorf("insert attribution entry: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create attribution entry: %w", err)
	}
	return nil
}

func (r *PgReviewRepository) CountPending(ctx context.Context, tenantID string) (int64, error) {
	var n int64
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM eval_review_items WHERE status = 'pending'`).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("count pending eval review items: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/evaluation/infrastructure/persistence/ -run TestPgReviewRepository`
Expected: 全部 PASS（含幂等、状态机、列表计数）。若集成测试基建需要 env/container，参照本包既有集成测试的 skip 条件保持一致。

- [ ] **Step 5: Commit**

```bash
git add internal/evaluation/infrastructure/persistence/review_repository.go internal/evaluation/infrastructure/persistence/review_repository_test.go
git commit -m "feat(evaluation): PgReviewRepository 评审池持久化

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: ReviewService 应用层

**Files:**

- Create: `internal/evaluation/application/review_service.go`
- Test: `internal/evaluation/application/review_service_test.go`

**Interfaces:**

- Consumes: `port.ReviewRepository`、`port.ReviewEscalator`、`port.SuiteRepository`、`port.TraceEvidenceReader`（Task 5 + 既有）；`domain` 类型（Task 4）。
- Produces:
  - `type ReviewServiceDeps struct{ Repo port.ReviewRepository; Suites port.SuiteRepository; Evidence port.TraceEvidenceReader; Metrics observability.MetricsProvider; Logger *zap.Logger; Cfg domain.ReviewConfig }`
  - `func NewReviewService(deps ReviewServiceDeps) *ReviewService`
  - `func (s *ReviewService) TryEscalateObservation(ctx, tenantID string, obs *domain.EvalObservation) error`
  - `func (s *ReviewService) TryEscalateCaseResult(ctx, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) error`
  - `func (s *ReviewService) List(ctx, tenantID string, f port.ReviewFilter) ([]domain.ReviewItem, int64, error)`
  - `func (s *ReviewService) Get(ctx, tenantID, id string) (*domain.ReviewItem, error)`
  - `func (s *ReviewService) Decide(ctx, tenantID, id, actor string, verdict domain.HumanVerdict, reason string) (*domain.ReviewItem, error)`（状态机 + 副作用）
  - `func (s *ReviewService) RefreshBacklog(ctx, tenantID string) error`（刷新 eval_review_backlog Gauge）

- [ ] **Step 1: 写单元测试**

新建 `review_service_test.go`（mock port，仿本包既有 mock 风格）：

```go
package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type fakeReviewRepo struct {
	inserted []domain.ReviewItem
	marked   map[string]domain.HumanVerdict
	err      error
}

func (f *fakeReviewRepo) UpsertItem(_ context.Context, _ string, item *domain.ReviewItem) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.inserted = append(f.inserted, *item)
	return true, nil
}
func (f *fakeReviewRepo) GetItem(_ context.Context, _, id string) (*domain.ReviewItem, error) {
	for i := range f.inserted {
		if f.inserted[i].ID == id {
			return &f.inserted[i], nil
		}
	}
	return nil, nil
}
func (f *fakeReviewRepo) ListItems(_ context.Context, _ string, _ port.ReviewFilter) ([]domain.ReviewItem, int64, error) {
	return f.inserted, int64(len(f.inserted)), nil
}
func (f *fakeReviewRepo) MarkReviewed(_ context.Context, _, id string, v domain.HumanVerdict, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	if f.marked == nil {
		f.marked = map[string]domain.HumanVerdict{}
	}
	f.marked[id] = v
	return nil
}
func (f *fakeReviewRepo) CreateCalibrationSample(_ context.Context, _ string, _ *domain.CalibrationSample) error {
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *fakeReviewRepo) CreateAttributionEntry(_ context.Context, _ string, _ *domain.AttributionEntry) error {
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *fakeReviewRepo) CountPending(_ context.Context, _ string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return int64(len(f.inserted)), nil
}

func newTestReviewService(repo port.ReviewRepository) *ReviewService {
	return NewReviewService(ReviewServiceDeps{
		Repo:    repo,
		Metrics: observability.NoopMetrics{},
		Logger:  zap.NewNop(),
		Cfg:     domain.ReviewConfig{LowConfidenceThreshold: 0.6, JudgePassThreshold: 0.5},
	})
}

func observationForTest() *domain.EvalObservation {
	return &domain.EvalObservation{
		ID: "obs-1", TraceID: "t-1",
		Resource: domain.ObservationResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "s1"},
		Verdict:  domain.VerdictPass,
		Signals: domain.ObservationSignals{Judge: []domain.JudgeSignal{
			{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9},
		}},
	}
}

func TestTryEscalateObservationFiresOnLowConfidence(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(repo.inserted))
	}
	got := repo.inserted[0]
	if got.TriggerReason != domain.TriggerLowConfidence || got.SourceType != domain.ReviewSourceObservation {
		t.Fatalf("unexpected item: %+v", got)
	}
	if got.ResourceKind != domain.ResourceKindSkill || got.ResourceID != "s1" {
		t.Fatalf("resource mismatch: %+v", got)
	}
}

func TestTryEscalateObservationNoTriggerNoInsert(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	if err := svc.TryEscalateObservation(context.Background(), "t1", observationForTest()); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 0 {
		t.Fatalf("inserted = %d, want 0", len(repo.inserted))
	}
}

func TestTryEscalateCaseResultFiresOnNeedsReview(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	result := domain.EvalCaseResult{ID: "cr-1", CaseID: "c1", TraceID: "t-1", Passed: true}
	c := domain.EvalCase{ID: "c1", NeedsReview: true}
	assertion := domain.AssertionResult{Passed: true, Confidence: 0.9}
	if err := svc.TryEscalateCaseResult(context.Background(), "t1", "run-1", result, c, assertion); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 1 || repo.inserted[0].TriggerReason != domain.TriggerNeedsReview {
		t.Fatalf("unexpected: %+v", repo.inserted)
	}
	if repo.inserted[0].RunID != "run-1" || repo.inserted[0].SourceID != "cr-1" {
		t.Fatalf("run/source mismatch: %+v", repo.inserted[0])
	}
}

func TestTryEscalatePropagatesRepoError(t *testing.T) {
	repo := &fakeReviewRepo{err: errors.New("db down")}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	// fail-open：错误原样返回，主流程侧忽略（TryEscalate 不 panic 不吞错）。
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err == nil {
		t.Fatal("want error propagated")
	}
}

func TestDecideStateMachine(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	id := repo.inserted[0].ID
	item, err := svc.Decide(context.Background(), "t1", id, "reviewer@x", domain.VerdictFail, "实际输出与上下文冲突")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if item.Status != domain.ReviewStatusReviewed || item.HumanVerdict != domain.VerdictFail {
		t.Fatalf("unexpected item: %+v", item)
	}
	if repo.marked[id] != domain.VerdictFail {
		t.Fatalf("mark reviewed not recorded: %+v", repo.marked)
	}
}

func TestDecideRejectsInvalidVerdict(t *testing.T) {
	svc := newTestReviewService(&fakeReviewRepo{})
	if _, err := svc.Decide(context.Background(), "t1", "ri-x", "reviewer@x", domain.HumanVerdict("bogus"), ""); err == nil {
		t.Fatal("want error for invalid verdict")
	}
}

// TestDecidePromoteUsesSuiteRepoAndEvidence 覆盖 promote 分支：judge_misjudgment
// 时经 suites.AddDraftCases 沉淀（Suites/Evidence 为 nil 时跳过 promote，仅落
// calibration sample）。该用例依赖 fake suite/evidence——若本包已有对应 mock 直接复用，
// 否则用最小 stub（RecordSuiteDraft 不导出时用接口断言）。
func TestDecidePromoteSkippedWhenSuitesNil(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	item := domain.ReviewItem{
		ID: uuid.NewString(), SourceType: domain.ReviewSourceObservation,
		SourceID: "obs-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "s1",
		TriggerReason: domain.TriggerLowConfidence, Status: domain.ReviewStatusPending,
		Snapshot: map[string]any{"signals": observationForTest().Signals},
	}
	repo.inserted = append(repo.inserted, item)
	if _, err := svc.Decide(context.Background(), "t1", item.ID, "reviewer@x", domain.VerdictJudgeMisjudgment, "judge 判错"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if repo.marked[item.ID] != domain.VerdictJudgeMisjudgment {
		t.Fatalf("not marked: %+v", repo.marked)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/evaluation/application/ -run TestTryEscalate\|TestDecide`
Expected: FAIL — `undefined: NewReviewService`。

- [ ] **Step 3: 实现 ReviewService**

新建 `review_service.go`：

```go
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var (
	ErrReviewItemNotFound   = errors.New("evaluation review item not found")
	ErrReviewItemNotPending = errors.New("evaluation review item not pending")
)

// ReviewServiceDeps 是评审池服务的依赖。Suites/Evidence 为 nil 时 promote 跳过
// （fail-open：无评测集/证据能力则不沉淀，仅落轻量记录）。
type ReviewServiceDeps struct {
	Repo     port.ReviewRepository
	Suites   port.SuiteRepository
	Evidence port.TraceEvidenceReader
	Metrics  observability.MetricsProvider
	Logger   *zap.Logger
	Cfg      domain.ReviewConfig
}

type ReviewService struct {
	deps ReviewServiceDeps
}

func NewReviewService(deps ReviewServiceDeps) *ReviewService {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ReviewService{deps: deps}
}

// 编译期断言：ReviewService 满足 port.ReviewEscalator。
var _ port.ReviewEscalator = (*ReviewService)(nil)

// TryEscalateObservation 判定观测入池原因并幂等落条目（spec §6.6）。返回错误表示
// 升级失败，调用方（ObservationService）记日志 + 指标，不阻断主流程。
func (s *ReviewService) TryEscalateObservation(ctx context.Context, tenantID string, obs *domain.EvalObservation) error {
	triggers := domain.TriggersForObservation(obs, s.deps.Cfg)
	if len(triggers) == 0 {
		return nil
	}
	snapshot := observationSnapshot(obs)
	for _, reason := range triggers {
		if _, err := s.deps.Repo.UpsertItem(ctx, tenantID, &domain.ReviewItem{
			ID:            uuid.Must(uuid.NewV7()).String(),
			SourceType:    domain.ReviewSourceObservation,
			SourceID:      obs.ID,
			TraceID:       obs.TraceID,
			ResourceKind:  obs.Resource.Kind,
			ResourceID:    obs.Resource.ResourceID,
			TriggerReason: reason,
			Snapshot:      snapshot,
			Status:        domain.ReviewStatusPending,
			CreatedAt:     time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("escalate observation %s: %w", obs.ID, err)
		}
	}
	return nil
}

// TryEscalateCaseResult 判定评测集 judge 结果入池原因并幂等落条目。断言来源
// result.Error==""（judge 实际产出）由调用方 runCase 保证。
func (s *ReviewService) TryEscalateCaseResult(
	ctx context.Context, tenantID, runID string,
	result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult,
) error {
	triggers := domain.TriggersForCaseResult(c.NeedsReview, assertion, s.deps.Cfg)
	if len(triggers) == 0 {
		return nil
	}
	snapshot := caseSnapshot(result, c, assertion)
	for _, reason := range triggers {
		if _, err := s.deps.Repo.UpsertItem(ctx, tenantID, &domain.ReviewItem{
			ID:            uuid.Must(uuid.NewV7()).String(),
			SourceType:    domain.ReviewSourceCaseResult,
			SourceID:      result.ID,
			RunID:         runID,
			TraceID:       result.TraceID,
			TriggerReason: reason,
			Snapshot:      snapshot,
			Status:        domain.ReviewStatusPending,
			CreatedAt:     time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("escalate case result %s: %w", result.ID, err)
		}
	}
	return nil
}

// List 分页列出评审条目。
func (s *ReviewService) List(ctx context.Context, tenantID string, f port.ReviewFilter) ([]domain.ReviewItem, int64, error) {
	return s.deps.Repo.ListItems(ctx, tenantID, f)
}

// Get 取单条评审条目。
func (s *ReviewService) Get(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error) {
	item, err := s.deps.Repo.GetItem(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrReviewItemNotFound
	}
	return item, nil
}

// Decide 人工评审结论状态机 + 回写副作用（spec §9）。幂等：重复决策同一已 reviewed
// 条目返回当前条目且不重复写副作用。
func (s *ReviewService) Decide(
	ctx context.Context, tenantID, id, actor string, verdict domain.HumanVerdict, reason string,
) (*domain.ReviewItem, error) {
	if !verdict.Valid() {
		return nil, fmt.Errorf("review decide: invalid verdict %q", verdict)
	}
	item, err := s.deps.Repo.GetItem(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrReviewItemNotFound
	}
	if item.Status == domain.ReviewStatusReviewed {
		return item, nil // 幂等：已评审直接返回
	}
	if err := s.deps.Repo.MarkReviewed(ctx, tenantID, id, verdict, actor, reason); err != nil {
		if errors.Is(err, ErrReviewItemNotPending) {
			// 并发评审竞态：另一评审者已落结论，返回现状。
			latest, getErr := s.deps.Repo.GetItem(ctx, tenantID, id)
			if getErr != nil {
				return nil, getErr
			}
			return latest, nil
		}
		return nil, err
	}
	item.Status = domain.ReviewStatusReviewed
	item.HumanVerdict = verdict
	item.Reviewer = actor
	item.ReviewReason = reason
	now := time.Now().UTC()
	item.ReviewedAt = &now

	s.applySideEffects(ctx, tenantID, item)
	return item, nil
}

// applySideEffects 按人工结论落轻量回写（fail-open：副作用失败仅日志，不改变
// 评审结论）。judge_misjudgment → 校准样本；fail / case_revision → 归因条目。
func (s *ReviewService) applySideEffects(ctx context.Context, tenantID string, item *domain.ReviewItem) {
	switch item.HumanVerdict {
	case domain.VerdictJudgeMisjudgment:
		if err := s.createCalibrationSample(ctx, tenantID, item); err != nil {
			s.deps.Logger.Warn("review calibration sample failed", zap.Error(err),
				zap.String("review_item_id", item.ID))
		}
	case domain.VerdictFail, domain.VerdictCaseRevision:
		if err := s.createAttributionEntry(ctx, tenantID, item); err != nil {
			s.deps.Logger.Warn("review attribution entry failed", zap.Error(err),
				zap.String("review_item_id", item.ID))
		}
	}
}

func (s *ReviewService) createCalibrationSample(ctx context.Context, tenantID string, item *domain.ReviewItem) error {
	signals := snapshotSignals(item)
	sample := &domain.CalibrationSample{
		ID:           uuid.Must(uuid.NewV7()).String(),
		ReviewItemID: item.ID,
		SourceType:   item.SourceType,
		SourceID:     item.SourceID,
		Signals:      signals,
		HumanVerdict: item.HumanVerdict,
		Reviewer:     item.Reviewer,
		Reason:       item.ReviewReason,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.deps.Repo.CreateCalibrationSample(ctx, tenantID, sample); err != nil {
		return err
	}
	// promote：observation 来源解析 trace 构造 judge case 进评测集草稿。
	if s.deps.Suites == nil {
		return nil
	}
	if err := s.promote(ctx, tenantID, item); err != nil {
		return fmt.Errorf("promote review item %s: %w", item.ID, err)
	}
	return nil
}

func (s *ReviewService) createAttributionEntry(ctx context.Context, tenantID string, item *domain.ReviewItem) error {
	entry := &domain.AttributionEntry{
		ID:           uuid.Must(uuid.NewV7()).String(),
		ReviewItemID: item.ID,
		SourceType:   item.SourceType,
		SourceID:     item.SourceID,
		ResourceKind: item.ResourceKind,
		ResourceID:   item.ResourceID,
		Dimension:    s.firstLowConfidenceDimension(item),
		Snapshot:     item.Snapshot,
		Status:       string(item.HumanVerdict),
		Reviewer:     item.Reviewer,
		Reason:       item.ReviewReason,
		CreatedAt:    time.Now().UTC(),
	}
	return s.deps.Repo.CreateAttributionEntry(ctx, tenantID, entry)
}

// promote 把评审条目沉淀为评测集草稿 case（spec §9）：observation 来源经
// TraceEvidenceReader 解析 trace 得 input/output，构造 judge-mode EvalCase 后
// 经 SuiteRepository.AddDraftCases 落草稿；case_result 来源从快照重建 case。
func (s *ReviewService) promote(ctx context.Context, tenantID string, item *domain.ReviewItem) error {
	suiteID := "review-promote-" + tenantID
	switch item.SourceType {
	case domain.ReviewSourceObservation:
		if s.deps.Evidence == nil || item.TraceID == "" {
			return nil
		}
		trace, err := s.deps.Evidence.Resolve(ctx, tenantID, item.TraceID)
		if err != nil {
			return fmt.Errorf("resolve trace %s: %w", item.TraceID, err)
		}
		name := fmt.Sprintf("review-observation-%s", item.SourceID)
		revisionID, err := s.draftRevisionForSuite(ctx, tenantID, suiteID, item.ResourceKind)
		if err != nil {
			return err
		}
		return s.deps.Suites.AddDraftCases(ctx, tenantID, revisionID, []domain.EvalCase{{
			ID: uuid.Must(uuid.NewV7()).String(), Name: name,
			Input: trace.Input, ExpectedOutput: nil, AssertionMode: domain.AssertionJudge,
			Enabled: false, // 草稿禁用，人工确认后启用
		}})
	case domain.ReviewSourceCaseResult:
		revisionID, err := s.draftRevisionForSuite(ctx, tenantID, suiteID, item.ResourceKind)
		if err != nil {
			return err
		}
		return s.deps.Suites.AddDraftCases(ctx, tenantID, revisionID, []domain.EvalCase{rebuiltCase(item)})
	default:
		return fmt.Errorf("promote: unsupported source type %q", item.SourceType)
	}
}

// draftRevisionForSuite 解析沉淀目标套件（review-promote-<tenant>）的草稿 revision；
// 套件不存在时惰性创建（P1c 轻量约定：沉淀到独立专用套件，P2 再纳入人工选择目标
// 评测集）。resourceKind 取自评审条目的资源 kind。promote 失败仍经 applySideEffects
// 的 Logger.Warn 记录，不阻断评审结论。
func (s *ReviewService) draftRevisionForSuite(ctx context.Context, tenantID, suiteID string, kind domain.ResourceKind) (string, error) {
	revision, found, err := s.deps.Suites.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return "", fmt.Errorf("get draft revision for %s: %w", suiteID, err)
	}
	if found {
		return revision.ID, nil
	}
	if err := s.deps.Suites.CreateSuite(ctx, tenantID, domain.EvalSuite{
		ID: suiteID, Name: "Review Promote Pool",
		Description: "人工评审池 promote 沉淀的专用评测集（P1c §9）",
	}, domain.EvalSuiteRevision{
		ID: uuid.Must(uuid.NewV7()).String(), SuiteID: suiteID,
		Status: domain.SuiteRevisionDraft, ResourceKind: kind,
	}); err != nil {
		return "", fmt.Errorf("create review promote suite %s: %w", suiteID, err)
	}
	created, found, err := s.deps.Suites.GetDraftRevision(ctx, tenantID, suiteID)
	if err != nil {
		return "", fmt.Errorf("get draft revision after create %s: %w", suiteID, err)
	}
	if !found {
		return "", fmt.Errorf("review promote suite %s has no draft revision after create", suiteID)
	}
	return created.ID, nil
}

// rebuiltCase 从评审条目快照平铺字段重建评测 case；快照缺失/字段类型不符时退化为
// 禁用 judge case（promote 失败由 applySideEffects 记录，不阻断评审结论）。
func rebuiltCase(item *domain.ReviewItem) domain.EvalCase {
	c := domain.EvalCase{
		ID:            uuid.Must(uuid.NewV7()).String(),
		AssertionMode: domain.AssertionJudge,
		Enabled:       false, // 草稿禁用，人工确认后启用
	}
	m, ok := item.Snapshot.(map[string]any)
	if !ok {
		return c
	}
	if name, ok := m["case_name"].(string); ok {
		c.Name = name
	}
	if input, ok := m["input"]; ok {
		c.Input = input
	}
	if expected, ok := m["expected"]; ok {
		c.ExpectedOutput = expected
	}
	return c
}

func observationSnapshot(obs *domain.EvalObservation) map[string]any {
	// signals 直接存对象（JSONB 自然序列化），避免二次编码。
	return map[string]any{
		"signals":    obs.Signals,
		"verdict":    string(obs.Verdict),
		"stratum":    obs.Stratum,
		"cost_usd":   obs.CostPerf.CostUSD,
		"created_at": obs.CreatedAt,
	}
}

func caseSnapshot(result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) map[string]any {
	return map[string]any{
		"case_id":         c.ID,
		"case_name":       c.Name,
		"assertion_mode":  string(c.AssertionMode),
		"input":           c.Input,
		"expected":        c.ExpectedOutput,
		"actual":          result.Actual,
		"passed":          result.Passed,
		"message":         result.Message,
		"judge_confidence": assertion.Confidence,
	}
}

// snapshotSignals 提取评审条目快照里的 signals 对象（judge 误判校准样本数据源）。
func snapshotSignals(item *domain.ReviewItem) any {
	m, ok := item.Snapshot.(map[string]any)
	if !ok {
		return nil
	}
	return m["signals"]
}

// firstLowConfidenceDimension 从快照 signals 找第一个低置信维度（归因条目
// dimension 数据源）；阈值来自配置，禁止内联数字。
func (s *ReviewService) firstLowConfidenceDimension(item *domain.ReviewItem) string {
	m, ok := item.Snapshot.(map[string]any)
	if !ok {
		return ""
	}
	signals, ok := m["signals"].(map[string]any)
	if !ok {
		return ""
	}
	judge, ok := signals["judge"].([]any)
	if !ok {
		return ""
	}
	for _, raw := range judge {
		j, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		confidence, _ := j["confidence"].(float64)
		dimension, _ := j["dimension"].(string)
		if confidence < s.deps.Cfg.LowConfidenceThreshold {
			return dimension
		}
	}
	return ""
}

// RefreshBacklog 用待评审数刷新 eval_review_backlog Gauge（积压告警数据源）。
func (s *ReviewService) RefreshBacklog(ctx context.Context, tenantID string) error {
	n, err := s.deps.Repo.CountPending(ctx, tenantID)
	if err != nil {
		return err
	}
	s.deps.Metrics.SetEvalReviewBacklog(n)
	return nil
}
```

（⚠️ **实现注意**：promote 的惰性建套件用 `CreateSuite` + `GetDraftRevision`（`EvalSuite`/`EvalSuiteRevision` 字段已确认）。若 CreateSuite 实现不把传入 revision 挂为 draft，则改为先 CreateSuite 再 CreateDraftRevision，以本仓库既有 suite 创建用例为准。`promote` 的 fail-open 语义保留：任何 promote 失败只经 `applySideEffects` 的 `Logger.Warn` 记录，不改变评审结论。）

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/evaluation/application/ -run TestTryEscalate\|TestDecide`
Expected: 全部 PASS。

- [ ] **Step 5: 运行全包测试确认无回归**

Run: `go test ./internal/evaluation/...`
Expected: PASS（`promote` 仅在 `judge_misjudgment` 决策时经 `draftRevisionForSuite` 触达 `SuiteRepository`，单测以 `Suites=nil` 跳过，无回归）。

- [ ] **Step 6: Commit**

```bash
git add internal/evaluation/application/review_service.go internal/evaluation/application/review_service_test.go
git commit -m "feat(evaluation): 评审池应用层（触发/列表/决策状态机/回写副作用）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: 评审池指标（MetricsProvider）

**Files:**

- Modify: `pkg/observability/provider.go`（接口 + Noop stub）
- Modify: `pkg/observability/prometheus.go`（`PrometheusMetrics` 字段、`NewPrometheusMetrics` 注册、方法实现）
- Test: `pkg/observability/prometheus_metrics_test.go`（沿用 `NewPrometheusMetrics(zap.NewNop())` 基建）

**Interfaces:**

- Consumes: 无。
- Produces:
  - 接口方法 `SetEvalReviewBacklog(count int64)`、`IncEvalReviewEscalateFailure()`。
  - Prometheus 实现：`eval_review_backlog` Gauge + `eval_review_escalate_failure_total` Counter。
  - Noop 空实现。

- [ ] **Step 1: 写测试（编译期契约 + Prometheus 抓取断言）**

在 `pkg/observability/prometheus_metrics_test.go` 追加（沿用该文件 `NewPrometheusMetrics(zap.NewNop())` 基建与既有抓取模式，如 `TestPrometheusMetricsEvalQueueBacklog`）：

```go
func TestPrometheusMetricsReviewPool(t *testing.T) {
	m := NewPrometheusMetrics(zap.NewNop())
	m.SetEvalReviewBacklog(7)
	m.IncEvalReviewEscalateFailure()
	// eval_review_backlog 可由测试 registry 抓取到（注册成功即说明字段未缺失）。
	// 断言方法：仿同文件 EvalQueueBacklog 测试的 Gather() 后按指标名查找。
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/observability/ -run TestMetricsProviderReviewPool`
Expected: FAIL — `NewPrometheusMetrics(...).SetEvalReviewBacklog undefined`。

- [ ] **Step 3: 实现**

`pkg/observability/provider.go`：

(a) 接口（Evaluation 段 `RecordEvalSampleCoverage` 之后）：

```go
	// P1b：主动采样覆盖率（§11.1 eval_sample_coverage，Gauge [0,1]）。
	RecordEvalSampleCoverage(resource string, ratio float64)
	// P1c：评审池积压（eval_review_backlog，Gauge）与升级失败计数。
	SetEvalReviewBacklog(count int64)
	IncEvalReviewEscalateFailure()
```

(b) Noop stub（`RecordEvalSampleCoverage` 行之后）：

```go
func (NoopMetrics) SetEvalReviewBacklog(_ int64)                          {}
func (NoopMetrics) IncEvalReviewEscalateFailure()                         {}
```

(c) `PrometheusMetrics` 字段（`pkg/observability/prometheus.go`，`evalSampleCoverage` 字段附近）：

```go
	evalReviewBacklog         prometheus.Gauge
	evalReviewEscalateFailure prometheus.Counter
```

(d) 注册（`NewPrometheusMetrics` 的 struct literal 内 Evaluation 字段块，与 `httpRequestsInFlight` 的 `factory.NewGauge(...)` 同款写法）：

```go
		// P1c：评审池积压 Gauge + 升级失败 Counter（§11.3）。
		evalReviewBacklog: factory.NewGauge(
			prometheus.GaugeOpts{Name: "eval_review_backlog", Help: "评审池待人工评审条目数（P1c 积压告警数据源）"},
		),
		evalReviewEscalateFailure: factory.NewCounter(
			prometheus.CounterOpts{Name: "eval_review_escalate_failure_total", Help: "评审池升级失败次数（fail-open，不阻断主流程）"},
		),
```

(e) 方法实现（`RecordEvalSampleCoverage` 方法附近）：

```go
func (m *PrometheusMetrics) SetEvalReviewBacklog(count int64) {
	if m.evalReviewBacklog != nil {
		m.evalReviewBacklog.Set(float64(count))
	}
}

func (m *PrometheusMetrics) IncEvalReviewEscalateFailure() {
	if m.evalReviewEscalateFailure != nil {
		m.evalReviewEscalateFailure.Inc()
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./pkg/observability/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/observability/provider.go pkg/observability/prometheus.go pkg/observability/prometheus_metrics_test.go
git commit -m "feat(observability): 评审池积压 Gauge 与升级失败 Counter

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: wiring 装配与内联触发

**Files:**

- Modify: `api/wiring/evaluation.go`（buildEvaluation、buildObservationService、Evaluation struct）
- Modify: `internal/evaluation/application/observation_service.go`（ObservationServiceDeps.Review + Process 内联触发 + Service logger）
- Modify: `internal/evaluation/application/service.go`（logger 字段替换占位 logReviewEscalateError）

**Interfaces:**

- Consumes: `ReviewService`（Task 7）、`ReviewConfig`、`SetEvalReviewBacklog`（Task 8）、`ObservationServiceDeps.Review`。
- Produces:
  - `buildReviewService(c *Container, db *pgxpool.Pool, suites port.SuiteRepository, traceReader evalport.TraceEvidenceReader) *evalapp.ReviewService`
  - `Service.SetReviewEscalator(port.ReviewEscalator, domain.ReviewConfig)`（Task 3 已有）
  - `ObservationServiceDeps.Review port.ReviewEscalator`
  - `ObservationService.Process` 在 `IncEvalObservation` 后调用 `TryEscalateObservation`（fail-open）
  - `Evaluation.ReviewService *evalapp.ReviewService`

- [ ] **Step 1: ObservationServiceDeps + Process 内联触发**

`internal/evaluation/application/observation_service.go`，Deps 增加字段：

```go
type ObservationServiceDeps struct {
	Enabled    func(ctx context.Context) bool
	SampleRate func(ctx context.Context) float64
	Evidence   port.TraceEvidenceReader
	Judge      port.LLMJudge
	Repo       port.ObservationRepository
	Metrics    observability.MetricsProvider
	Logger     *zap.Logger
	TenantTier port.TenantTierReader
	// Review 是评审池升级入口（P1c §6.6）；nil 时评审升级静默跳过（fail-open）。
	Review port.ReviewEscalator
}
```

`Process` 末尾（`IncEvalObservation` 之后）追加：

```go
	s.recordSampled(evt.ResourceKind)
	s.deps.Metrics.IncEvalObservation(evt.ResourceKind, obs.Stratum)
	// 评审池内联触发（P1c §6.6）：落库成功后按触发规则入池。fail-open——升级失败
	// 仅日志 + 指标，不阻断观测主流程、不改 verdict。
	if s.deps.Review != nil {
		if err := s.deps.Review.TryEscalateObservation(ctx, evt.TenantID, &obs); err != nil {
			s.deps.Logger.Warn("observation review escalation failed", zap.Error(err),
				zap.String("trace_id", evt.TraceID))
			s.deps.Metrics.IncEvalReviewEscalateFailure()
		}
	}
	return nil
```

- [ ] **Step 2: Service logger 落地（替换 Task 3 占位）**

`internal/evaluation/application/service.go`：

```go
type Service struct {
	adapter     port.ResourceAdapter
	repo        port.RunRepository
	suites      port.SuiteRepository
	traceReader port.TraceEvidenceReader
	judge       port.LLMJudge
	review      port.ReviewEscalator
	reviewCfg   domain.ReviewConfig
	logger      *zap.Logger
}

func NewService(
	adapter port.ResourceAdapter,
	repo port.RunRepository,
	traceReader port.TraceEvidenceReader,
	judge port.LLMJudge,
	suites ...port.SuiteRepository,
) *Service {
	var suiteRepo port.SuiteRepository
	if len(suites) > 0 {
		suiteRepo = suites[0]
	}
	return &Service{adapter: adapter, repo: repo, suites: suiteRepo, traceReader: traceReader, judge: judge, logger: zap.NewNop()}
}
```

`logReviewEscalateError` 替换：

```go
func (s *Service) logReviewEscalateError(_ context.Context, err error) {
	s.logger.Warn("evaluation review escalation failed", zap.Error(err))
}
```

（import 增加 `"go.uber.org/zap"`；`escalateCaseResult` 保持 Task 3 实现不变。）

- [ ] **Step 3: 写 wiring 测试（编译 + 装配 smoke）**

`api/wiring/evaluation_test.go` 追加：

```go
func TestBuildEvaluationWiresReviewService(t *testing.T) {
	// 依赖 Container 装配基建；若本文件无现成 smoke 基建，跳过并由
	// go build 编译保证（Task 9 的验收重点是 buildEvaluation 注入链路编译通过）。
	_ = 0
}
```

（若 `api/wiring` 无测试基建，此用例可删除，以 `go build ./...` 为验收。）

- [ ] **Step 4: 实现 wiring**

`api/wiring/evaluation.go`：

(a) 新增 `buildReviewService`（放在 `buildObservationService` 附近）：

```go
// buildReviewService 装配评审池服务（P1c §6.6）：复用 suite 仓库做 promote 沉淀、
// trace evidence reader 解析观测 trace。必须在 Service / ObservationService 注入前装配。
func buildReviewService(
	c *Container, db *pgxpool.Pool, suites evalport.SuiteRepository, traceReader evalport.TraceEvidenceReader,
) *evalapp.ReviewService {
	return evalapp.NewReviewService(evalapp.ReviewServiceDeps{
		Repo:     evalpersist.NewPgReviewRepository(db),
		Suites:   suites,
		Evidence: traceReader,
		Metrics:  c.platformMetrics(),
		Logger:   c.Logger,
		Cfg: domain.ReviewConfig{
			LowConfidenceThreshold: constants.ReviewLowConfidenceThreshold,
			JudgePassThreshold:     constants.JudgeBelowThreshold,
		},
	})
}
```

（import 需补 `evaldomain` 若未引入——`wiring/evaluation.go` 已别名 `evaldomain` 用于 AssertionResult，`domain.ReviewConfig` 用 `evaldomain.ReviewConfig`。）

(b) `Evaluation` struct 增加字段：

```go
	TestCaseGenerator    *evalapp.TestCaseGenerator
	ObservationService   *evalapp.ObservationService
	ReviewService        *evalapp.ReviewService
```

(c) `buildEvaluation` 中装配与注入：

在 `observationSvc := buildObservationService(...)` 之前插入：

```go
	reviewSvc := buildReviewService(c, db, suiteRepo, traceReader)
	observationSvc := buildObservationService(c, db, traceReader, judge)
```

`buildObservationService` 内 `NewObservationService` 的 deps 增加 Review：

```go
	return evalapp.NewObservationService(evalapp.ObservationServiceDeps{
		Enabled:    func(ctx context.Context) bool { return observationEnabled(ctx, c.Parameters.Service) },
		SampleRate: func(ctx context.Context) float64 { return observationSampleRate(ctx, c.Parameters.Service) },
		Evidence:   traceReader,
		Judge:      judge,
		Repo:       evalpersist.NewPgObservationRepository(db),
		Metrics:    c.platformMetrics(),
		Logger:     c.Logger,
		TenantTier: tenantTierAdapter{repo: iampersistence.NewAdminTenantRepo(db)},
		Review:     reviewSvc,
	})
```

`service := evalapp.NewService(...)` 之后、`jobService := ...` 之前注入 escalator：

```go
	service := evalapp.NewService(
		evaluationResourceRouter{adapters: resourceAdapters}, runRepo, traceReader, judge, suiteRepo,
	)
	service.SetReviewEscalator(reviewSvc, evaldomain.ReviewConfig{
		LowConfidenceThreshold: constants.ReviewLowConfidenceThreshold,
		JudgePassThreshold:     constants.JudgeBelowThreshold,
	})
```

`c.Evaluation = &Evaluation{...}` 增加 `ReviewService: reviewSvc`：

```go
		TestCaseGenerator:    buildTestCaseGenerator(c, suiteRepo, db),
		ObservationService:   observationSvc,
		ReviewService:        reviewSvc,
	}
```

- [ ] **Step 5: 运行测试**

Run: `go build ./... && go vet ./api/wiring/... && go test ./internal/evaluation/application/ ./api/wiring/`
Expected: 全部通过。

- [ ] **Step 6: Commit**

```bash
git add api/wiring/evaluation.go internal/evaluation/application/observation_service.go internal/evaluation/application/service.go
git commit -m "feat(evaluation): 装配评审池服务并接入观测/评测集内联触发

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: HTTP API（评审池查询 + 决策）

**Files:**

- Modify: `proto/evaluation/evaluation.proto`（新增 `ReviewItemDecisionRequest`）
- Modify: `api/http/handler/evaluation_handler.go`（review 接口 + handler）
- Modify: `api/http/router.go`（evaluations 组路由）
- Modify: `api/http/contract_test.go` + `api/http/testdata/contracts/*.golden.json`（make record-contracts）
- Modify: `api/wiring/evaluation.go`（handler 注入 WithReviewService —— 若 handler 由 wiring 装配）

**Interfaces:**

- Consumes: `ReviewService.List/Get/Decide`（Task 7）。
- Produces:
  - 路由：`GET /evaluations/review`（requireAdmin）、`GET /evaluations/review/:id`（requireAdmin）、`POST /evaluations/review/:id/decision`（requireAdmin）。
  - handler 接口：`evaluationReviewService`（List/Get/Decide）+ `WithReviewService` setter。

- [ ] **Step 1: proto 契约**

`proto/evaluation/evaluation.proto` 末尾追加：

```proto
// ReviewItemDecisionRequest 人工评审结论（spec §9 回写状态机）。
message ReviewItemDecisionRequest {
  // @binding: required,oneof=pass fail judge_misjudgment case_revision
  string verdict = 1;
  // @binding: required,max=2048
  string reason = 2;
}
```

运行 `make proto-gen` 生成 DTO（gitignored）。

- [ ] **Step 2: 写 handler 测试**

`api/http/handler/evaluation_handler_test.go` 追加（仿既有 observation handler 测试）：

```go
func TestEvaluationHandlerListReview(t *testing.T) {
	// mock evaluationReviewService；断言 200 + items/total 结构。
}

func TestEvaluationHandlerDecideReview(t *testing.T) {
	// mock Decide；断言 200 + reviewed item + 非法 verdict 400。
}
```

（mock 命名与断言风格对齐本文件既有测试，如 `TestEvaluationHandlerListObservations`。）

- [ ] **Step 3: 实现 handler**

`api/http/handler/evaluation_handler.go`：

(a) 接口定义（`evaluationObservationQueryService` 定义附近）：

```go
// evaluationReviewService 是评审池查询/决策服务的 handler 侧契约。
type evaluationReviewService interface {
	List(ctx context.Context, tenantID string, f port.ReviewFilter) ([]domain.ReviewItem, int64, error)
	Get(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error)
	Decide(ctx context.Context, tenantID, id, actor string, verdict domain.HumanVerdict, reason string) (*domain.ReviewItem, error)
}
```

（import 补 `"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"`。）

(b) struct 字段 + setter：

```go
	observations evaluationObservationQueryService
	review       evaluationReviewService
```

```go
// WithReviewService 注入评审池服务（P1c 查询/决策 API）。
func (h *EvaluationHandler) WithReviewService(service evaluationReviewService) *EvaluationHandler {
	h.review = service
	return h
}
```

(c) handler 方法（放在 GetObservation 之后）：

```go
// ListReviewItems 返回评审池条目分页（规格 §10 数据源）。
func (h *EvaluationHandler) ListReviewItems(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review pool unavailable")))
		return
	}
	var req ReviewListQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > constants.MaxPageSize {
		req.PageSize = constants.DefaultPageSize
	}
	limit, offset := req.PageSize, (req.Page-1)*req.PageSize
	items, total, err := h.review.List(c.Request.Context(), tenantID, port.ReviewFilter{
		Status:        domain.ReviewItemStatus(req.Status),
		TriggerReason: domain.ReviewTriggerReason(req.TriggerReason),
		ResourceKind:  req.ResourceKind,
		ResourceID:    req.ResourceID,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	if items == nil {
		items = []domain.ReviewItem{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// GetReviewItem 返回单条评审条目详情。
func (h *EvaluationHandler) GetReviewItem(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review pool unavailable")))
		return
	}
	item, err := h.review.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "review item not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// DecideReviewItem 提交人工评审结论（回写状态机 + 副作用）。
func (h *EvaluationHandler) DecideReviewItem(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review pool unavailable")))
		return
	}
	var req dto.ReviewItemDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	item, err := h.review.Decide(c.Request.Context(), tenantID, c.Param("id"),
		currentActor(c), domain.HumanVerdict(req.Verdict), req.Reason)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "review item not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}
```

（`ReviewListQuery` 查询 struct 定义：`Status, TriggerReason, ResourceKind, ResourceID string` + `Page, PageSize int`；`currentActor(c)` 若不存在则用 `c.GetString("actor")` 或从 `tenantIDFromCtx` 的 identity 提取——以本文件既有取值方式为准。）

- [ ] **Step 4: 注册路由**

`api/http/router.go`，evaluations 组内 observations 路由后追加：

```go
		evaluations.GET("/observations", h.ListObservations)
		evaluations.GET("/observations/:id", h.GetObservation)
		// P1c 人工评审池：仅 admin 可查看与决策。
		evaluations.GET("/review", requireAdmin, h.ListReviewItems)
		evaluations.GET("/review/:id", requireAdmin, h.GetReviewItem)
		evaluations.POST("/review/:id/decision", requireAdmin, h.DecideReviewItem)
```

- [ ] **Step 5: wiring 注入 handler**

`api/wiring/evaluation.go` 装配 handler 处（EvaluationHandler 链式 With 调用处）追加 `.WithReviewService(reviewSvc)`（确认 `reviewSvc` 在作用域内，必要时把 reviewSvc 提前到 handler 装配之前）。

- [ ] **Step 6: 运行测试 + 更新黄金文件**

Run: `go test ./api/http/handler/ -run TestEvaluationHandler` && `make record-contracts`（若契约测试基建存在）
Expected: 通过；`api/http/testdata/contracts/*.golden.json` 更新含 review 路由。

- [ ] **Step 7: Commit**

```bash
git add proto/evaluation/evaluation.proto api/http/handler/evaluation_handler.go api/http/router.go api/http/contract_test.go api/http/testdata/ api/wiring/evaluation.go
git commit -m "feat(evaluation): 评审池查询与决策 HTTP API

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 11: 前端评审池 Tab

**Files:**

- Modify: `web/src/modules/evaluation/pages/EvaluationCenterPage.tsx`（Tabs items 增加评审池）
- Create: `web/src/modules/evaluation/components/ReviewPoolPanel.tsx`
- Create: `web/src/modules/evaluation/services/review.ts`
- Test: `web/src/modules/evaluation/components/ReviewPoolPanel.test.tsx`（若前端有测试基建）

**Interfaces:**

- Consumes: 后端 `GET /evaluations/review`、`GET /evaluations/review/:id`、`POST /evaluations/review/:id/decision`。
- Produces: 评审池 Tab 面板（列表 + 详情 + 决策按钮）。

- [ ] **Step 1: 写 API service**

新建 `web/src/modules/evaluation/services/review.ts`：

```ts
import client from '@/services/client'
import type { ReviewItem, ReviewItemDecisionRequest } from '../types/review'

export async function listReviewItems(params: {
  status?: string
  triggerReason?: string
  page?: number
  pageSize?: number
}): Promise<{ items: ReviewItem[]; total: number }> {
  const { data } = await client.get('/evaluations/review', { params })
  return data
}

export async function getReviewItem(id: string): Promise<ReviewItem> {
  const { data } = await client.get(`/evaluations/review/${id}`)
  return data
}

export async function decideReviewItem(
  id: string,
  body: ReviewItemDecisionRequest,
): Promise<ReviewItem> {
  const { data } = await client.post(`/evaluations/review/${id}/decision`, body)
  return data
}
```

（`web/src/modules/evaluation/types/review.ts` 定义 `ReviewItem`/`ReviewItemDecisionRequest` 类型，字段对齐后端 domain.ReviewItem JSON：id/source_type/source_id/run_id/trace_id/resource_kind/resource_id/trigger_reason/snapshot/status/human_verdict/reviewer/review_reason/created_at/reviewed_at。）

- [ ] **Step 2: 写 ReviewPoolPanel 组件**

新建 `web/src/modules/evaluation/components/ReviewPoolPanel.tsx`（列表 + 详情 Drawer + 决策 Modal；用户可见中文）：

```tsx
import { useEffect, useState } from 'react'
import { Button, Drawer, Empty, Modal, Select, Space, Table, Tag, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { decideReviewItem, listReviewItems } from '../services/review'
import type { ReviewItem } from '../types/review'

const VERDICT_OPTIONS = [
  { value: 'pass', label: '通过' },
  { value: 'fail', label: '不通过' },
  { value: 'judge_misjudgment', label: 'judge 误判' },
  { value: 'case_revision', label: '用例需修正' },
]

const REASON_LABELS: Record<string, string> = {
  low_confidence: '低置信度',
  dimension_split: '维度分歧',
  judge_rule_conflict: '规则与 judge 冲突',
  needs_review: '需人工复核',
}

export default function ReviewPoolPanel() {
  const [items, setItems] = useState<ReviewItem[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [detail, setDetail] = useState<ReviewItem | null>(null)
  const [decisionTarget, setDecisionTarget] = useState<ReviewItem | null>(null)

  const load = async (page = 1, pageSize = 10) => {
    setLoading(true)
    try {
      const data = await listReviewItems({ page, pageSize })
      setItems(data.items ?? [])
      setTotal(data.total ?? 0)
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '加载评审池失败', duration: 3 })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const columns: ColumnsType<ReviewItem> = [
    { title: '来源', dataIndex: 'source_type', width: 100, render: (v: string) => (v === 'observation' ? '观测' : '评测集') },
    { title: '原因', dataIndex: 'trigger_reason', width: 120, render: (v: string) => <Tag>{REASON_LABELS[v] ?? v}</Tag> },
    { title: '资源', dataIndex: 'resource_kind', width: 80 },
    { title: '状态', dataIndex: 'status', width: 80, render: (v: string) => (v === 'pending' ? '待评审' : '已评审') },
    { title: '创建时间', dataIndex: 'created_at', width: 180 },
    {
      title: '操作',
      key: 'actions',
      width: 160,
      render: (_, record) => (
        <Space>
          <Button size="small" onClick={() => setDetail(record)}>详情</Button>
          {record.status === 'pending' && (
            <Button size="small" type="primary" onClick={() => setDecisionTarget(record)}>评审</Button>
          )}
        </Space>
      ),
    },
  ]

  const submitDecision = async (verdict: string, reason: string) => {
    if (!decisionTarget) return
    try {
      await decideReviewItem(decisionTarget.id, { verdict, reason })
      message.success({ content: '评审已提交', duration: 2 })
      setDecisionTarget(null)
      load()
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '提交失败', duration: 3 })
    }
  }

  return (
    <div>
      <Table<ReviewItem>
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={items}
        pagination={{ total, pageSize: 10, onChange: (page) => load(page) }}
      />
      <Drawer title="评审详情" open={!!detail} onClose={() => setDetail(null)} width={520}>
        {detail ? <pre style={{ whiteSpace: 'pre-wrap' }}>{JSON.stringify(detail.snapshot, null, 2)}</pre> : <Empty />}
      </Drawer>
      <Modal
        title="人工评审"
        open={!!decisionTarget}
        onCancel={() => setDecisionTarget(null)}
        onOk={() => {
          const target = decisionTarget
          if (target) {
            Modal.confirm({ ... }) // 见下注
          }
        }}
      >
        {/* 决策表单：verdict Select + reason TextArea，提交调 submitDecision */}
      </Modal>
    </div>
  )
}
```

（⚠️ **实现注意**：决策 Modal 用受控 Form（`Form.useForm` + `Form.Item`），`onOk` 校验后调 `submitDecision(values.verdict, values.reason)`；上方的占位 `Modal.confirm({...})` 是示意，实际以 antd Form 实现，禁止 `alert()/confirm()`。组件超过 200 行时把决策 Modal 拆成独立子组件 `ReviewDecisionModal`。）

- [ ] **Step 3: 挂载到 EvaluationCenterPage Tabs**

`web/src/modules/evaluation/pages/EvaluationCenterPage.tsx`，Tabs `items` 数组追加：

```tsx
    {
      key: 'review',
      label: '人工评审池',
      children: <ReviewPoolPanel />,
    },
```

并 import `ReviewPoolPanel`。

- [ ] **Step 4: 前端验证**

Run: `cd web && npx tsc --noEmit && npm run lint`
Expected: 通过（`make fe-lint && make fe-build` 也可）。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/evaluation/pages/EvaluationCenterPage.tsx web/src/modules/evaluation/components/ReviewPoolPanel.tsx web/src/modules/evaluation/services/review.ts web/src/modules/evaluation/types/review.ts
git commit -m "feat(evaluation): 前端人工评审池 Tab

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 12: 评审池积压告警 + runbook

**Files:**

- Modify: `monitoring/remote/rules/stratum-evaluation.yaml`
- Create: `docs/runbooks/stratum-eval-review-backlog.md`
- Test: `monitoring/remote/rules/promtool` 校验（若仓库有 `make` 目标或直接 `promtool check rules`）

**Interfaces:**

- Consumes: `eval_review_backlog` Gauge（Task 8）。
- Produces: `StratumEvalReviewBacklogHigh` alert rule + runbook。

- [ ] **Step 1: 写 runbook**

新建 `docs/runbooks/stratum-eval-review-backlog.md`：

```markdown
# StratumEvalReviewBacklogHigh

**告警含义**：人工评审池待评审条目 `eval_review_backlog` 持续超过 50（15 分钟）。
**影响**：低置信/判异样本滞留评审池，judge 误判与产品缺陷无法及时回写。
**排查**：
1. `SELECT count(*) FROM eval_review_items WHERE status = 'pending';`（逐租户）
2. 按 `trigger_reason` 分组看是否单一原因堆积：观测 vs 评测集。
3. 若 `low_confidence` 大量堆积：检查 judge 模型质量/温度参数，评估置信度阈值。
4. 若 `judge_rule_conflict` 堆积：规则护栏与 judge 判定系统性冲突，需人工抽查。
**处置**：admin 进评审池逐条决策；积压持续时评估阈值或扩充评审人力。
```

- [ ] **Step 2: 写告警规则**

`monitoring/remote/rules/stratum-evaluation.yaml` 追加（对齐现有规则字段）：

```yaml
  - alert: StratumEvalReviewBacklogHigh
    expr: eval_review_backlog > 50
    for: 15m
    labels:
      severity: warning
      service: stratum-evaluation
      component: review-pool
      environment: production
    annotations:
      summary: "人工评审池积压超过阈值（当前 {{ $value }}）"
      description: "待评审条目持续 > 50 达 15 分钟，低置信/判异样本滞留，需人工介入。"
      dashboard_url: "{{ .externalURL }}"
      runbook_url: "https://github.com/byteBuilderX/stratum/blob/main/docs/runbooks/stratum-eval-review-backlog.md"
```

- [ ] **Step 3: 校验规则**

Run: `promtool check rules monitoring/remote/rules/stratum-evaluation.yaml`
Expected: 通过（`Successfully loaded rules file`）。

- [ ] **Step 4: Commit**

```bash
git add monitoring/remote/rules/stratum-evaluation.yaml docs/runbooks/stratum-eval-review-backlog.md
git commit -m "feat(monitoring): 评审池积压告警规则 + runbook

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 13: 全量验证与 R3 系统验收

**Files:**

- 无（验证任务）。

- [ ] **Step 1: 快速后端验证**

Run: `bash scripts/quality/risk-regression-guard.sh --explain && go vet ./... && go test -short ./...`
Expected: 全绿。

- [ ] **Step 2: 完整测试套件**

Run: `go test -v -race -timeout 30s ./...`
Expected: 全部 PASS（含 contract tests）。

- [ ] **Step 3: 前端门禁**

Run: `make fe-lint && make fe-build`
Expected: 通过。

- [ ] **Step 4: R3 系统验收**

通过 `stratum-e2e-tester` agent 执行 `make test-verify-before-pr`（命中 `internal/evaluation/**` external-dependency 规则 → R3，自动跑 600s soak）。验收路径：

1. 观测路径：触发低置信观测入池 → `GET /evaluations/review` 出现 `low_confidence` 条目。
2. 评测集路径：`needs_review` case 运行后入池。
3. 决策回写：admin 决策 `judge_misjudgment` → 校准样本落库；`fail` → 归因条目落库；重复决策幂等。
4. 积压指标：`eval_review_backlog` Gauge 随 pending 数变化。
5. 告警规则 `promtool` 校验通过。

- [ ] **Step 5: 汇总验收报告**

验收通过后记录结论（stratum-e2e-tester 产出结构化报告），进入 finishing-a-development-branch。
