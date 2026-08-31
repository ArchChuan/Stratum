# Evaluation P3c 评测输出升级 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把评测输出从「单 verdict + 顶层聚合」升级为 spec §6.2 多维指标（judge 三维度打分 + run.metrics by_dimension/cost/latency/version 双版本锚点）+ §6.3 评测归因三层（同集版本对比 / case 聚类 / trace 下钻）的可展示数据。

**Architecture:** 分层增量扩展，兼容旧数据。judge 契约 `AssertionResult` 增加 `Dimensions []DimensionScore`（三维度 score/passed/reason/confidence，fail-open 解析）；`EvalCaseResult` 增加 `Dimensions` + `FailureReason`（`dimension:<名> | assert:<mode> | execution`）；`aggregateRunMetrics` 在保留现有顶层键前提下新增 spec §6.2 嵌套结构；eval_case_results 以 `ADD COLUMN IF NOT EXISTS` 升级历史租户。归因三层全部由 run 详情数据支撑（同集对比 = 同资源两次 run 的 metrics 对比），不新增后端端点。run 详情响应是手写 Go struct 直接序列化，**不触碰 proto / contract golden**。

**Tech Stack:** Go 1.25（domain/application/infrastructure + api/wiring）、PostgreSQL JSONB、React 18 + antd 5 + zod。

## Global Constraints

- 维度名 `faithfulness` / `relevance` / `completeness` 为 judge 三维度（spec §6.2）；safety/format/tool_pass/step_reasoning/behavior（§3.1 规则/process/行为维度）与 by_category（§6.2 case 分类）**不在 P3c 范围**——规则断言无多维分数、领域无 case 分类字段，计划中明确降级，不得静默实现。
- `result.failure_reason` 格式（spec §6.2）：judge 失败 → `"dimension:<维度名>"`；规则断言失败 → `"assert:<exact|contains|regex>"`；执行失败 → `"execution"`；通过 case 为空。
- 双版本锚点（spec §6.2/§3.5）：`metrics.version = { suite_revision_id, platform_seq, resource_version }`。platform_seq 由 `SetPlatformVersion` 注入（复用 wiring 的 `observationPlatformVersion`，api/wiring/evaluation.go:1268）；读取器 nil/失败/无已发布版本时 fail-open 记 0（unknown 语义与 ObservationService `resolvePlatformVersion` 一致，见 observation_service.go:244-252）。
- 多维分数 fail-open 解析：单维度非法（name 空 / score 越界）丢弃该维度，不使整次 judge 判定失败；完全无合法维度返回 nil，聚合层自动跳过（兼容旧 judge 单 verdict 输出）。
- 落库：eval_case_results 新列必须 `ADD COLUMN IF NOT EXISTS` + 安全默认（`JSONB NOT NULL DEFAULT '{}'::jsonb`、`TEXT NOT NULL DEFAULT ''`），仅改 `pkg/storage/postgres/tenant_schema.sql`，禁止改 `pkg/migration/sql/`。
- pgx v5 JSONB：dimensions 写入必须 `json.Marshal` 后传 `string(b)`，禁止直接传 struct。
- metrics 向后兼容：现有顶层键（pass_rate/total_tokens/total_cost_usd/avg_latency_ms/p95_latency_ms/avg_*_at_5/rag_case_count）**全部保留**，只新增嵌套结构。
- dimensions.reason 与现有 message 同等级（judge 文本，不额外脱敏）；actual_output 仍走 `sanitizeValue`（run_repository.go:122）。
- 行为数字禁止内联；新增函数满足圈复杂度 ≤10、认知复杂度 ≤15、行数 ≤120、嵌套 ≤4。
- 前端：错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 3 })`；成功 `message.success({ content: '操作成功', duration: 2 })`；组件 ≤200 行（超出提取）；antd CJK 按钮匹配用 regex `/创\s*建/` 风格；用户可见字符串中文；vitest 必须从 `web/` 目录运行。
- 安全：不得记录 password/token/API key/PII/原始上游响应体；bearer credential 不得进入 URL/Web Storage/通用请求日志/下游错误正文。
- Commit 格式 `[type](scope): description`，结尾 `Co-Authored-By: Claude <noreply@anthropic.com>`；禁止在 `main` 分支直接提交。

---

### Task 1: judge 多维打分（domain 类型 + prompt + 解析）

**Files:**

- Modify: `internal/evaluation/domain/evaluation.go`（AssertionResult 区块，25-31 行）
- Modify: `api/wiring/evaluation.go`（judgeDefaultRubric 541-545、parseJudgeResponse 852-879）
- Test: `api/wiring/evaluation_test.go`（TestParseJudgeResponseConfidence 23 行附近）

**Interfaces:**

- Produces: `domain.DimensionScore{Name string, Score float64, Passed bool, Reason string, Confidence float64}`（json tags: name/score/passed/reason/confidence）；`domain.AssertionResult.Dimensions []DimensionScore`（json tag `dimensions,omitempty`）。Task 2 依赖此类型做 case 落链，Task 3 依赖此类型做聚合。

- [ ] **Step 1: 在 domain/evaluation.go 定义 DimensionScore 并扩展 AssertionResult**

在 `AssertionResult` 定义（25-31 行）前加入类型，并给 `AssertionResult` 加字段：

```go
// DimensionScore 是 judge 对一个语义维度（faithfulness/relevance/completeness）
// 的评分。Score 归一化到 [0,1]；Confidence 缺失/越界由解析层回退 1.0（与
// AssertionResult.Confidence 同语义，spec §6.2）。规则断言不产生该结构。
type DimensionScore struct {
	Name       string  `json:"name"`
	Score      float64 `json:"score"`
	Passed     bool    `json:"passed"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type AssertionResult struct {
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
	// Confidence 是 judge 判定置信度（0-1）。规则断言不产生该值；judge 解析
	// 缺失/无效时由 parseJudgeResponse 回退 1.0（spec §6.2，本 domain 不静默改值）。
	Confidence float64 `json:"confidence,omitempty"`
	// Dimensions 是 judge 返回的语义维度分数（spec §6.2）。旧 judge 不返回
	// 维度时为空；聚合层对空维度自动跳过，不阻断判定。
	Dimensions []DimensionScore `json:"dimensions,omitempty"`
}
```

- [ ] **Step 2: 扩展 judgeDefaultRubric 要求三维度输出**

把 `judgeDefaultRubric`（api/wiring/evaluation.go:541-545）整体替换为：

```go
// judgeDefaultRubric is the built-in rubric used for LLM judge verdicts. It
// asks for a binary verdict with a short justification, a 0-1 confidence,
// and per-dimension scores (faithfulness / relevance / completeness, spec
// §6.2). Missing or invalid dimensions are tolerated by parseJudgeResponse
// (fail-open), so older judges that return only passed/reason keep working.
const judgeDefaultRubric = `你是一名严谨的评测法官。根据以下标准判断实际输出是否通过：
1. 实际输出是否直接、完整地回答了输入要求；
2. 与期望输出的一致性（期望输出为 null 或空时忽略该项）；
3. 是否存在明显的事实错误或逻辑矛盾。
只输出 JSON：{"passed": true 或 false, "reason": "一句话理由", "confidence": 0-1 之间的小数表示判定置信度,
"dimensions": [{"name": "faithfulness", "score": 0-1, "passed": true 或 false, "reason": "一句话理由", "confidence": 0-1},
{"name": "relevance", "score": 0-1, "passed": true 或 false, "reason": "一句话理由", "confidence": 0-1},
{"name": "completeness", "score": 0-1, "passed": true 或 false, "reason": "一句话理由", "confidence": 0-1}]}。`
```

- [ ] **Step 3: 先写失败测试 TestParseJudgeResponseDimensions**

在 `api/wiring/evaluation_test.go`（紧接 TestParseJudgeResponseConfidence）追加：

```go
func TestParseJudgeResponseDimensions(t *testing.T) {
	content := `{"passed":false,"reason":"事实错误","confidence":0.6,
		"dimensions":[
			{"name":"faithfulness","score":0.4,"passed":false,"reason":"与实际不符","confidence":0.7},
			{"name":"relevance","score":0.9,"passed":true,"reason":"","confidence":0.9},
			{"name":"completeness","score":0.8,"passed":true}
		]}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if got.Passed || got.Message != "事实错误" {
		t.Fatalf("verdict mismatch: %+v", got)
	}
	if got.Confidence != 0.6 {
		t.Fatalf("confidence = %v, want 0.6", got.Confidence)
	}
	if len(got.Dimensions) != 3 {
		t.Fatalf("dimensions = %d, want 3", len(got.Dimensions))
	}
	faith := got.Dimensions[0]
	if faith.Name != "faithfulness" || faith.Score != 0.4 || faith.Passed || faith.Confidence != 0.7 {
		t.Fatalf("faithfulness mismatch: %+v", faith)
	}
	if got.Dimensions[2].Confidence != 1.0 { // 缺失回退 1.0
		t.Fatalf("completeness confidence = %v, want 1.0", got.Dimensions[2].Confidence)
	}
}

func TestParseJudgeResponseInvalidDimensionsDropped(t *testing.T) {
	content := `{"passed":true,"reason":"通过","dimensions":[
		{"name":"","score":0.5,"passed":true},          // name 空 → 丢弃
		{"name":"relevance","score":2.5,"passed":true}, // score 越界 → 丢弃
		{"name":"completeness","score":-0.1,"passed":true},
		{"name":"faithfulness","score":0.7,"passed":true}
	]}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if len(got.Dimensions) != 1 || got.Dimensions[0].Name != "faithfulness" {
		t.Fatalf("dimensions = %+v, want only faithfulness", got.Dimensions)
	}
}

func TestParseJudgeResponseNoDimensionsTolerated(t *testing.T) {
	content := `{"passed":false,"reason":"不及格","confidence":0.3}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if got.Passed || len(got.Dimensions) != 0 {
		t.Fatalf("old-style verdict must stay single: %+v", got)
	}
}
```

- [ ] **Step 4: 运行测试确认失败（dimensions 字段还不存在）**

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./api/wiring/ -run 'TestParseJudgeResponse' -count=1`
Expected: 编译失败 / TestParseJudgeResponseDimensions FAIL（`got.Dimensions` 不存在）。

- [ ] **Step 5: 实现 parseJudgeResponse 解析 dimensions**

把 `parseJudgeResponse`（852-879）的 verdict struct 扩展并加两个 helper，整体替换函数体（保留现有 confidence 语义，抽共用 helper）：

```go
// parseJudgeResponse extracts {"passed","reason","confidence","dimensions"}
// from the judge output, tolerating a markdown code fence around the JSON.
// Confidence and per-dimension scores are optional: missing, non-numeric or
// out-of-[0,1] values default to 1.0 / are dropped (fail-open, spec §6.2).
func parseJudgeResponse(content string) (evaldomain.AssertionResult, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var verdict struct {
		Passed     bool              `json:"passed"`
		Reason     string            `json:"reason"`
		Confidence json.RawMessage   `json:"confidence"`
		Dimensions []json.RawMessage `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &verdict); err != nil {
		return evaldomain.AssertionResult{}, fmt.Errorf("LLM judge: parse verdict: %w", err)
	}
	confidence := 1.0
	if score, ok := parseScore01(verdict.Confidence); ok {
		confidence = score
	}
	return evaldomain.AssertionResult{
		Passed:     verdict.Passed,
		Message:    verdict.Reason,
		Confidence: confidence,
		Dimensions: parseJudgeDimensions(verdict.Dimensions),
	}, nil
}

// parseJudgeDimensions 解析 judge 返回的多维分数。单维度非法（name 空、score
// 越界、JSON 无法解析）时丢弃该维度而非使整次判定失败（fail-open）；完全无
// 合法维度返回 nil，聚合层自动跳过。confidence 缺失/越界回退 1.0。
func parseJudgeDimensions(raw []json.RawMessage) []evaldomain.DimensionScore {
	out := make([]evaldomain.DimensionScore, 0, len(raw))
	for _, item := range raw {
		var d struct {
			Name       string          `json:"name"`
			Score      json.RawMessage `json:"score"`
			Passed     bool            `json:"passed"`
			Reason     string          `json:"reason"`
			Confidence json.RawMessage `json:"confidence"`
		}
		if err := json.Unmarshal(item, &d); err != nil || strings.TrimSpace(d.Name) == "" {
			continue
		}
		score, ok := parseScore01(d.Score)
		if !ok {
			continue
		}
		confidence := 1.0
		if c, ok := parseScore01(d.Confidence); ok {
			confidence = c
		}
		out = append(out, evaldomain.DimensionScore{
			Name: d.Name, Score: score, Passed: d.Passed, Reason: d.Reason, Confidence: confidence,
		})
	}
	return out
}

// parseScore01 解析 [0,1] 内的 number；缺失/null/非数字/越界返回 ok=false。
func parseScore01(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil || v < 0 || v > 1 {
		return 0, false
	}
	return v, true
}
```

注意：现有 `parseJudgeResponse` 内联的 confidence 判定（871-877 行）被 `parseScore01` 替换，`TestParseJudgeResponseConfidence` 语义保持一致（缺失/越界 → 1.0）。

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./api/wiring/ -run 'TestParseJudgeResponse' -count=1 && go build ./...`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add internal/evaluation/domain/evaluation.go api/wiring/evaluation.go api/wiring/evaluation_test.go
git commit -m "feat(evaluation): judge 多维打分（dimensions 契约 + fail-open 解析）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: case 级多维分数与失败归因落链（service 层）

**Files:**

- Modify: `internal/evaluation/domain/evaluation.go`（EvalCaseResult，141-159 行）
- Modify: `internal/evaluation/application/service.go`（judgeCase 215-254、runCase 161-209）
- Test: `internal/evaluation/application/service_test.go`（fakeLLMJudge 242 行附近）

**Interfaces:**

- Consumes: `domain.DimensionScore`、`domain.AssertionResult.Dimensions`（Task 1）
- Produces: `domain.EvalCaseResult.Dimensions []DimensionScore`（json tag `dimensions,omitempty`）、`domain.EvalCaseResult.FailureReason string`（json tag `failure_reason,omitempty`）。Task 3 聚合、Task 4 落库依赖。

- [ ] **Step 1: 扩展 EvalCaseResult**

在 `internal/evaluation/domain/evaluation.go` 的 EvalCaseResult（141-159 行）追加字段：

```go
	// RAGEvidence carries structured retrieval metrics for knowledge
	// evaluations; nil for other resource kinds. It replaces brittle parsing
	// of the serialized Actual payload.
	RAGEvidence *RAGEvidenceInfo `json:"rag_evidence,omitempty"`
	// Dimensions 是 judge case 的语义维度分数（spec §6.2），由 judge 判定拷贝；
	// 规则断言 case 为空。
	Dimensions []DimensionScore `json:"dimensions,omitempty"`
	// FailureReason 是 case 失败的主要归因（spec §6.2）：judge 失败 →
	// "dimension:<名>"；规则断言失败 → "assert:<mode>"；执行失败 → "execution"。
	// 通过 case 为空。
	FailureReason string `json:"failure_reason,omitempty"`
}
```

- [ ] **Step 2: 先写失败测试**

在 `internal/evaluation/application/service_test.go` 末尾追加。复用现有 helper（service_test.go:108-151）：`fakeAdapter{outputs: map[string]any{caseID: output}, errCase: string}`、`fakeRunRepo{}`、`fakeLLMJudge{enabled, result, err}`、`errFakeExecution`（151 行），以及现有 judge 测试模式 `NewService(adapter, repo, nil, judge)` + 完整 `RunInput`（参照 TestServiceJudgeAssertionDispatchesToJudge，257-292 行）：

```go
func TestRunJudgeCasePopulatesDimensionsAndFailureReason(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"judge-1": "回答不准确"}}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true, result: domain.AssertionResult{
		Passed: false, Message: "faithfulness 不足", Confidence: 0.6,
		Dimensions: []domain.DimensionScore{
			{Name: "faithfulness", Score: 0.3, Passed: false, Confidence: 0.7},
			{Name: "relevance", Score: 0.9, Passed: true, Confidence: 0.8},
		},
	}}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "judge-1", Input: "问题", AssertionMode: domain.AssertionJudge, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Passed {
		t.Fatal("judge failed verdict must fail the run")
	}
	got := run.Results[0]
	if len(got.Dimensions) != 2 || got.Dimensions[0].Name != "faithfulness" {
		t.Fatalf("dimensions = %+v", got.Dimensions)
	}
	if got.FailureReason != "dimension:faithfulness" {
		t.Fatalf("failure_reason = %q, want dimension:faithfulness", got.FailureReason)
	}
}

func TestRunRuleAssertionSetsAssertFailureReason(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "你好"}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil) // 规则断言不走 judge

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "问", ExpectedOutput: "找不到的关键词", AssertionMode: domain.AssertionContains, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if got.Passed {
		t.Fatal("contains mismatch must fail")
	}
	if got.FailureReason != "assert:contains" {
		t.Fatalf("failure_reason = %q, want assert:contains", got.FailureReason)
	}
	if len(got.Dimensions) != 0 {
		t.Fatalf("rule assertions must not carry dimensions: %+v", got.Dimensions)
	}
}

func TestRunExecutionErrorSetsExecutionFailureReason(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "ok"}, errCase: "case-1"}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "问", ExpectedOutput: "答", AssertionMode: domain.AssertionContains, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if got.Error == "" {
		t.Fatal("execution error must surface")
	}
	if got.FailureReason != "execution" {
		t.Fatalf("failure_reason = %q, want execution", got.FailureReason)
	}
}
```

注意：`fakeAdapter.errCase` 按 `c.ID` 精确匹配触发执行错误（ExecuteRevision 129 行），因此执行错误用例的 case ID 必须为 `"case-1"`。该文件已 import `errors`（235 行用于 fakeFailingTraceEvidenceReader），本测试不新增对 errors 的使用。

- [ ] **Step 3: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./internal/evaluation/application/ -run 'TestRunJudgeCasePopulates|TestRunRuleAssertionSets|TestRunExecutionErrorSets' -count=1`
Expected: FAIL（`got.Dimensions`/`FailureReason` 字段不存在）。

- [ ] **Step 4: 实现 service 层填充**

修改 `internal/evaluation/application/service.go`：

a) `runCase` 的执行错误分支（167-169 行）补 failure_reason：

```go
	execution, err := s.adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
	result := domain.EvalCaseResult{ID: uuid.Must(uuid.NewV7()).String(), CaseID: testCase.ID}
	if err != nil {
		result.Error = err.Error()
		result.FailureReason = "execution"
		return result
	}
```

b) `runCase` 的规则断言分支（206-207 行）：

```go
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	if !assertion.Passed {
		result.FailureReason = "assert:" + string(testCase.AssertionMode)
	}
	return result
```

c) `judgeCase`（251-253 行）补维度拷贝与失败归因：

```go
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	result.Dimensions = assertion.Dimensions
	result.FailureReason = judgeFailureReason(assertion)
	return assertion, result
```

d) 新增 helper（放在 judgeCase 之后）：

```go
// judgeFailureReason 从 judge 判定推导主要失败维度（spec §6.2）：优先取显式
// 判负的维度，否则取 score 最低维度；无维度信息时回退 "judge"（保持归因可见）。
func judgeFailureReason(assertion domain.AssertionResult) string {
	if assertion.Passed {
		return ""
	}
	for _, d := range assertion.Dimensions {
		if !d.Passed {
			return "dimension:" + d.Name
		}
	}
	if len(assertion.Dimensions) > 0 {
		worst := assertion.Dimensions[0]
		for _, d := range assertion.Dimensions[1:] {
			if d.Score < worst.Score {
				worst = d
			}
		}
		return "dimension:" + worst.Name
	}
	return "judge"
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./internal/evaluation/application/ -count=1`
Expected: PASS（含新增 3 测试 + 存量）。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add internal/evaluation/domain/evaluation.go internal/evaluation/application/service.go internal/evaluation/application/service_test.go
git commit -m "feat(evaluation): case 级多维分数与失败归因（dimension/assert/execution）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: run.metrics 多维聚合（by_dimension/cost/latency/version）

**Files:**

- Modify: `internal/evaluation/application/metrics.go`（aggregateRunMetrics 14-47、新增 helper）
- Modify: `internal/evaluation/application/service.go`（SetPlatformVersion + Run 154 行）
- Modify: `api/wiring/evaluation.go`（newEvaluationServiceWithReview 615-627 注入）
- Test: `internal/evaluation/application/metrics_test.go`（TestAggregateRunMetrics 37 行）

**Interfaces:**

- Consumes: `domain.EvalCaseResult.Dimensions`（Task 2）、`domain.EvalRun`
- Produces: `aggregateRunMetrics(run domain.EvalRun, version runVersionAnchor) map[string]any`——signature 变更，唯一调用点在 service.go:154。新增 `metrics.overall_pass_rate`、`metrics.cost{total_usd,avg_usd}`、`metrics.latency{p50_ms,p95_ms,max_ms}`、`metrics.by_dimension{<name>:{avg_score,pass_rate,samples}}`、`metrics.version{suite_revision_id,platform_seq,resource_version}`。

- [ ] **Step 1: 先写失败测试**

在 `internal/evaluation/application/metrics_test.go` 追加（读现有 TestAggregateRunMetrics 结构后，保留原用例并加新断言）：

```go
func TestAggregateRunMetricsMultidimensional(t *testing.T) {
	run := domain.EvalRun{
		ID: "run-1", SuiteRevisionID: "rev-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "s1", RevisionID: "res-v2"},
		Passed:   false, TotalCases: 2, PassedCases: 1,
		Results: []domain.EvalCaseResult{
			{Passed: false, DurationMs: 100, CostUSD: 0.01,
				Dimensions: []domain.DimensionScore{
					{Name: "faithfulness", Score: 0.4, Passed: false},
					{Name: "relevance", Score: 0.9, Passed: true},
				}},
			{Passed: true, DurationMs: 300, CostUSD: 0.03,
				Dimensions: []domain.DimensionScore{
					{Name: "faithfulness", Score: 0.8, Passed: true},
					{Name: "relevance", Score: 0.7, Passed: true},
					{Name: "completeness", Score: 0.6, Passed: true},
				}},
		},
	}
	metrics := aggregateRunMetrics(run, runVersionAnchor{SuiteRevisionID: "rev-1", PlatformSeq: 3, ResourceVersion: "res-v2"})

	if metrics["overall_pass_rate"] != 0.5 {
		t.Fatalf("overall_pass_rate = %v, want 0.5", metrics["overall_pass_rate"])
	}
	cost, ok := metrics["cost"].(map[string]any)
	if !ok || cost["total_usd"] != 0.04 || cost["avg_usd"] != 0.02 {
		t.Fatalf("cost = %+v, want {total_usd:0.04 avg_usd:0.02}", metrics["cost"])
	}
	lat, ok := metrics["latency"].(map[string]any)
	// nearest-rank percentile of [100,300]: p50=100 (rank ceil(0.5*2)=1),
	// p95=300 (rank ceil(0.95*2)=2), max=300.
	if !ok || lat["max_ms"] != 300.0 || lat["p50_ms"] != 100.0 || lat["p95_ms"] != 300.0 {
		t.Fatalf("latency = %+v, want p50=100 p95=max=300", metrics["latency"])
	}
	byDim, ok := metrics["by_dimension"].(map[string]any)
	if !ok {
		t.Fatalf("by_dimension missing: %+v", metrics)
	}
	faith := byDim["faithfulness"].(map[string]any)
	if faith["avg_score"] != 0.6 || faith["pass_rate"] != 0.5 || faith["samples"] != 2 {
		t.Fatalf("faithfulness = %+v, want {avg_score:0.6 pass_rate:0.5 samples:2}", faith)
	}
	comp := byDim["completeness"].(map[string]any)
	if comp["avg_score"] != 0.6 || comp["pass_rate"] != 1.0 || comp["samples"] != 1 {
		t.Fatalf("completeness = %+v, want {avg_score:0.6 pass_rate:1 samples:1}", comp)
	}
	ver, ok := metrics["version"].(map[string]any)
	if !ok || ver["suite_revision_id"] != "rev-1" || ver["platform_seq"] != int64(3) || ver["resource_version"] != "res-v2" {
		t.Fatalf("version = %+v, want {rev-1, 3, res-v2}", metrics["version"])
	}
}

func TestAggregateRunMetricsNoJudgeDimensions(t *testing.T) {
	run := domain.EvalRun{Passed: true, TotalCases: 1, PassedCases: 1,
		Results: []domain.EvalCaseResult{{Passed: true, DurationMs: 10}}}
	metrics := aggregateRunMetrics(run, runVersionAnchor{})
	if _, ok := metrics["by_dimension"].(map[string]any); ok && len(metrics["by_dimension"].(map[string]any)) != 0 {
		t.Fatalf("by_dimension should be empty for no-dimension runs: %+v", metrics["by_dimension"])
	}
	if _, ok := metrics["version"].(map[string]any); !ok {
		t.Fatalf("version must always be present: %+v", metrics)
	}
	if metrics["version"].(map[string]any)["platform_seq"] != int64(0) {
		t.Fatalf("platform_seq must be 0 (unknown) when anchor absent")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./internal/evaluation/application/ -run 'TestAggregateRunMetricsMultidimensional|TestAggregateRunMetricsNoJudgeDimensions' -count=1`
Expected: FAIL（签名 `aggregateRunMetrics(run)` 与 `runVersionAnchor` 不存在）。

- [ ] **Step 3: 实现 metrics.go 扩展**

整体替换 `aggregateRunMetrics`（metrics.go:14-47）并新增 helper：

```go
// runVersionAnchor 是 run 的双版本锚点（spec §6.2/§3.5）。PlatformSeq 在平台
// 版本读取器未装配 / 读取失败 / 无已发布版本时为 0（fail-open，unknown 语义
// 与 ObservationService resolvePlatformVersion 一致）。
type runVersionAnchor struct {
	SuiteRevisionID string
	PlatformSeq     int64
	ResourceVersion string
}

// aggregateRunMetrics derives run-level signals from the case results after
// the case loop, before persistence. Existing top-level keys are preserved
// for backward compatibility; spec §6.2 nested structures (overall_pass_rate,
// cost/latency/by_dimension/version) are added alongside.
func aggregateRunMetrics(run domain.EvalRun, version runVersionAnchor) map[string]any {
	metrics := map[string]any{
		"pass_rate":          0.0,
		"overall_pass_rate":  0.0,
		"total_cases":        run.TotalCases,
		"cost":               map[string]any{"total_usd": 0.0, "avg_usd": 0.0},
		"latency":            map[string]any{},
		"by_dimension":       map[string]any{},
		"version":            map[string]any{"suite_revision_id": version.SuiteRevisionID, "platform_seq": version.PlatformSeq, "resource_version": version.ResourceVersion},
	}
	if run.TotalCases > 0 {
		metrics["pass_rate"] = float64(run.PassedCases) / float64(run.TotalCases)
		metrics["overall_pass_rate"] = metrics["pass_rate"]
	}
	if len(run.Results) == 0 {
		return metrics
	}

	var totalTokens int
	var totalCostUSD float64
	latencies := make([]int64, 0, len(run.Results))
	for _, result := range run.Results {
		totalTokens += result.Tokens
		totalCostUSD += result.CostUSD
		if result.DurationMs > 0 {
			latencies = append(latencies, int64(result.DurationMs))
		}
	}
	metrics["total_tokens"] = totalTokens
	metrics["total_cost_usd"] = totalCostUSD
	metrics["avg_tokens_per_case"] = float64(totalTokens) / float64(len(run.Results))
	metrics["cost"] = map[string]any{
		"total_usd": totalCostUSD,
		"avg_usd":   totalCostUSD / float64(len(run.Results)),
	}
	if len(latencies) > 0 {
		metrics["avg_latency_ms"] = avgInt64(latencies)
		metrics["p95_latency_ms"] = percentileInt64(latencies, 0.95)
		maxLatency := latencies[0]
		for _, l := range latencies[1:] {
			if l > maxLatency {
				maxLatency = l
			}
		}
		metrics["latency"] = map[string]any{
			"p50_ms": percentileInt64(latencies, 0.50),
			"p95_ms": percentileInt64(latencies, 0.95),
			"max_ms": float64(maxLatency),
		}
	}
	metrics["by_dimension"] = aggregateByDimension(run.Results)
	for key, value := range aggregateRAGEvidence(run.Results) {
		metrics[key] = value
	}
	return metrics
}

// aggregateByDimension 按语义维度聚合 judge case 分数（spec §6.2 by_dimension）。
// 只统计带 Dimensions 的 case；未出现的维度不在结果中。samples 为该维度贡献的
// case 数，avg_score 为 Score 均值，pass_rate 为该维度 Passed 比例。
func aggregateByDimension(results []domain.EvalCaseResult) map[string]any {
	type acc struct {
		scoreSum float64
		passed   int
		samples  int
	}
	accum := make(map[string]*acc)
	for _, result := range results {
		for _, d := range result.Dimensions {
			a, ok := accum[d.Name]
			if !ok {
				a = &acc{}
				accum[d.Name] = a
			}
			a.scoreSum += d.Score
			a.samples++
			if d.Passed {
				a.passed++
			}
		}
	}
	out := make(map[string]any, len(accum))
	for name, a := range accum {
		out[name] = map[string]any{
			"avg_score": a.scoreSum / float64(a.samples),
			"pass_rate": float64(a.passed) / float64(a.samples),
			"samples":   a.samples,
		}
	}
	return out
}
```

- [ ] **Step 4: 更新 service.go 注入版本锚点**

`internal/evaluation/application/service.go`：
a) Service struct 加字段（在 `metrics` 字段后）：

```go
	// platformVersion 解析平台配置组当前生效版本序号（Phase 2 §4.3 版本锚点）。
	// nil 时 run.metrics.version.platform_seq 记 0（unknown，fail-open）。
	platformVersion func(ctx context.Context) (int64, bool, error)
```

b) 新增 setter（SetObservability 之后）：

```go
// SetPlatformVersion 注入平台版本读取器（wiring 在 NewService 后调用）；nil
// 表示未装配，run.metrics.version.platform_seq 记 0（unknown，fail-open）。
func (s *Service) SetPlatformVersion(fn func(ctx context.Context) (int64, bool, error)) {
	s.platformVersion = fn
}
```

c) `Run()` 的聚合调用（154 行）替换：

```go
	seq := int64(0)
	if s.platformVersion != nil {
		if seqVal, ok, err := s.platformVersion(ctx); err == nil && ok {
			seq = seqVal
		}
	}
	run.Metrics = aggregateRunMetrics(run, runVersionAnchor{
		SuiteRevisionID: run.SuiteRevisionID,
		PlatformSeq:     seq,
		ResourceVersion: run.Resource.RevisionID,
	})
```

- [ ] **Step 5: 更新 wiring 注入**

`api/wiring/evaluation.go` 的 `newEvaluationServiceWithReview`（SetObservability 之后，625 行）加：

```go
	service.SetPlatformVersion(func(ctx context.Context) (int64, bool, error) {
		return observationPlatformVersion(ctx, c.Parameters.Service)
	})
```

- [ ] **Step 6: 修复存量 metrics 测试签名并确认全绿**

现有 `TestAggregateRunMetrics`（metrics_test.go:37）调用 `aggregateRunMetrics(run)`，需改为 `aggregateRunMetrics(run, runVersionAnchor{})`。改动后：

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./internal/evaluation/application/ ./api/wiring/ -count=1`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add internal/evaluation/application/metrics.go internal/evaluation/application/service.go api/wiring/evaluation.go internal/evaluation/application/metrics_test.go
git commit -m "feat(evaluation): run.metrics 多维聚合（by_dimension/cost/latency/version 双版本锚点）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: eval_case_results 多维列 DDL + repository 读写

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`（eval_case_results，507-521 行）
- Modify: `internal/evaluation/infrastructure/persistence/run_repository.go`（SaveRun 58-65、GetRun 100-115）
- Test: `internal/evaluation/infrastructure/persistence/run_repository_test.go`（或既有 integration）

**Interfaces:**

- Consumes: `domain.EvalCaseResult.Dimensions` / `FailureReason`（Task 2）
- Produces: eval_case_results 新增 `dimensions JSONB NOT NULL DEFAULT '{}'`、`failure_reason TEXT NOT NULL DEFAULT ''`；SaveRun/GetRun 往返读写。

- [ ] **Step 1: tenant_schema.sql 加列**

在 `eval_case_results` CREATE TABLE 定义内（tokens/cost_usd/duration_ms 列后）加两列，并在表后（idx_eval_case_results_run 之后）加升级 ALTER（历史租户）：

```sql
    cost_usd        DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_ms     INT NOT NULL DEFAULT 0,
    dimensions      JSONB NOT NULL DEFAULT '{}'::jsonb,
    failure_reason  TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_eval_case_results_run ON eval_case_results(run_id);
-- P3c 评测输出升级（spec §6.2）：case 级多维分数与失败归因，升级历史租户。
ALTER TABLE eval_case_results ADD COLUMN IF NOT EXISTS dimensions JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE eval_case_results ADD COLUMN IF NOT EXISTS failure_reason TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: SaveRun 写入维度**

`run_repository.go` SaveRun 的 case result INSERT（58-65 行）替换：

```go
			dimensionsJSON, err := json.Marshal(result.Dimensions)
			if err != nil {
				return fmt.Errorf("evaluation run repository: marshal dimensions: %w", err)
			}
			if string(dimensionsJSON) == "null" {
				dimensionsJSON = []byte("[]")
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO eval_case_results
				 (id, run_id, case_id, passed, actual_output, message, error_message, trace_id,
				  tokens, cost_usd, duration_ms, dimensions, failure_reason)
				 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				id, run.ID, result.CaseID, result.Passed, string(actualJSON),
				result.Message, result.Error, result.TraceID, result.Tokens, result.CostUSD, result.DurationMs,
				string(dimensionsJSON), result.FailureReason,
			); err != nil {
				return fmt.Errorf("evaluation run repository: insert case result: %w", err)
			}
```

- [ ] **Step 3: GetRun 读回维度**

`run_repository.go` GetRun 的 case result 查询（100-115 行）替换：

```go
		rows, err := tx.Query(ctx,
			`SELECT case_id, passed, actual_output, message, error_message, trace_id, tokens, cost_usd,
			        duration_ms, dimensions, failure_reason
			 FROM eval_case_results WHERE run_id=$1 ORDER BY created_at, id`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var result domain.EvalCaseResult
			var actualJSON []byte
			var dimensionsJSON []byte
			if err := rows.Scan(&result.CaseID, &result.Passed, &actualJSON, &result.Message, &result.Error,
				&result.TraceID, &result.Tokens, &result.CostUSD, &result.DurationMs,
				&dimensionsJSON, &result.FailureReason); err != nil {
				return err
			}
			_ = json.Unmarshal(actualJSON, &result.Actual)
			if len(dimensionsJSON) > 0 {
				_ = json.Unmarshal(dimensionsJSON, &result.Dimensions)
			}
			run.Results = append(run.Results, result)
		}
```

- [ ] **Step 4: 测试 SaveRun/GetRun 维度往返**

在 `internal/evaluation/infrastructure/persistence/run_repository_test.go` 找现有 SaveRun/GetRun 测试（integration 或 mock），追加或扩展现有断言：SaveRun 带 `Dimensions` 与 `FailureReason` 的 result → GetRun 读回相等；failure_reason 默认 ''、dimensions 默认空。若现有测试为 mock SQL 校验，扩展匹配 SQL 语句与参数。

Run: `cd /home/yang/go-projects/stratum-p3c && go test ./internal/evaluation/infrastructure/persistence/ -run 'Test.*RunRepository|TestPgRunRepository' -count=1`
Expected: PASS（DB 依赖的 integration 测试若需 infra 才跑，则先跑 mock 测试；PR 前完整跑）。

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add pkg/storage/postgres/tenant_schema.sql internal/evaluation/infrastructure/persistence/run_repository.go internal/evaluation/infrastructure/persistence/run_repository_test.go
git commit -m "feat(evaluation): eval_case_results 多维列 DDL + repository 读写（dimensions/failure_reason）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 前端多维指标展示

**Files:**

- Modify: `web/src/modules/evaluation/model/evaluation.ts`（evaluationRunSchema 78-101）
- Create: `web/src/modules/evaluation/components/RunMetricPanel.tsx`
- Modify: `web/src/modules/evaluation/components/RunDrawer.tsx`（metrics Tab 89-98 换用 RunMetricPanel）
- Test: `web/src/modules/evaluation/model/evaluation.test.ts` + Create `web/src/modules/evaluation/components/RunMetricPanel.test.tsx`

**Interfaces:**

- Consumes: 后端 `metrics` JSONB（含 by_dimension/version/cost/latency + 旧顶层键）、`results[].dimensions/failure_reason`
- Produces: `RunMetricPanel({ metrics }: { metrics: Record<string, unknown> })`——按 spec §6.2 渲染 by_dimension 表格、version/cost/latency 区块，并兼容旧顶层键；`dimensionScoreSchema`。

- [ ] **Step 1: 前端 schema 扩展**

`web/src/modules/evaluation/model/evaluation.ts` 加（evaluationRunSchema 前）：

```ts
export const dimensionScoreSchema = z.object({
  name: z.string(),
  score: z.number(),
  passed: z.boolean(),
  reason: z.string().optional(),
  confidence: z.number().optional(),
});
export type DimensionScore = z.infer<typeof dimensionScoreSchema>;
```

`evaluationRunSchema.results[]` 内加（现有 schema 96-97 行已有 `duration_ms`/`rag_evidence`，禁止重复定义）：

```ts
      dimensions: z.array(dimensionScoreSchema).optional(),
      failure_reason: z.string().optional(),
```

- [ ] **Step 2: 先写失败测试**

`web/src/modules/evaluation/model/evaluation.test.ts` 追加：

```ts
import { dimensionScoreSchema, evaluationRunSchema } from './evaluation';

describe('dimensionScoreSchema', () => {
  it('parses a valid dimension', () => {
    const dim = dimensionScoreSchema.parse({ name: 'faithfulness', score: 0.6, passed: true, confidence: 0.9 });
    expect(dim).toEqual({ name: 'faithfulness', score: 0.6, passed: true, confidence: 0.9 });
  });
});

describe('evaluationRunSchema', () => {
  it('parses run results with dimensions and failure_reason', () => {
    const run = evaluationRunSchema.parse({
      id: 'r1', resource: { kind: 'skill', resource_id: 's1', revision_id: 'v1' },
      suite_revision_id: 'rev-1', passed: false, total_cases: 1, passed_cases: 0,
      metrics: { version: { suite_revision_id: 'rev-1', platform_seq: 3, resource_version: 'v1' } },
      results: [{ case_id: 'c1', passed: false, dimensions: [{ name: 'faithfulness', score: 0.3, passed: false }], failure_reason: 'dimension:faithfulness' }],
    });
    expect(run.results[0].failure_reason).toBe('dimension:faithfulness');
    expect(run.results[0].dimensions?.[0].score).toBe(0.3);
  });
});
```

新建 `web/src/modules/evaluation/components/RunMetricPanel.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { RunMetricPanel } from './RunMetricPanel';

describe('RunMetricPanel', () => {
  it('renders scalar, by_dimension, version, cost and latency sections', () => {
    const metrics = {
      overall_pass_rate: 0.5,
      by_dimension: {
        faithfulness: { avg_score: 0.6, pass_rate: 0.5, samples: 2 },
        relevance: { avg_score: 0.8, pass_rate: 1, samples: 2 },
      },
      cost: { total_usd: 0.04, avg_usd: 0.02 },
      latency: { p50_ms: 100, p95_ms: 300, max_ms: 300 },
      version: { suite_revision_id: 'rev-1', platform_seq: 3, resource_version: 'res-v2' },
    };
    render(<RunMetricPanel metrics={metrics} />);
    expect(screen.getByText('基础指标')).toBeInTheDocument();
    expect(screen.getByText('总体通过率')).toBeInTheDocument();
    expect(screen.getByText('语义维度')).toBeInTheDocument();
    expect(screen.getByText('faithfulness')).toBeInTheDocument();
    expect(screen.getByText('relevance')).toBeInTheDocument();
    expect(screen.getByText('版本锚点')).toBeInTheDocument();
    expect(screen.getByText('rev-1')).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation/model/evaluation.test.ts src/modules/evaluation/components/RunMetricPanel.test.tsx`
Expected: FAIL（dimensionScoreSchema / RunMetricPanel 不存在）。

- [ ] **Step 4: 实现 RunMetricPanel**

新建 `web/src/modules/evaluation/components/RunMetricPanel.tsx`：

```tsx
import { Descriptions, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

// metricLabels maps the legacy top-level scalar run metrics keys (eval_runs.metrics
// keys produced by the backend, kept for backward compatibility) to Chinese labels;
// unknown scalar keys fall back to the raw key.
const metricLabels: Record<string, string> = {
  pass_rate: '通过率',
  overall_pass_rate: '总体通过率',
  total_cases: '用例数',
  total_tokens: '总 tokens',
  total_cost_usd: '总成本 (USD)',
  avg_tokens_per_case: '平均 tokens/用例',
  avg_latency_ms: '平均延迟 (ms)',
  p95_latency_ms: 'P95 延迟 (ms)',
  avg_recall_at_5: '平均 Recall@5',
  avg_precision_at_5: '平均 Precision@5',
  avg_mrr: '平均 MRR',
  avg_ndcg_at_5: '平均 nDCG@5',
  rag_case_count: 'RAG 证据用例数',
};

function formatMetric(key: string, value: number): string {
  if (key === 'pass_rate' || key === 'overall_pass_rate') {
    return `${(value * 100).toFixed(1)}%`;
  }
  if (Number.isInteger(value)) {
    return String(value);
  }
  return value.toFixed(4);
}

type DimensionRow = {
  name: string;
  avg_score: number;
  pass_rate: number;
  samples: number;
};

const dimensionColumns: ColumnsType<DimensionRow> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '平均分', dataIndex: 'avg_score', key: 'avg_score', render: (v: number) => v.toFixed(3) },
  { title: '通过率', dataIndex: 'pass_rate', key: 'pass_rate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
  { title: '样本数', dataIndex: 'samples', key: 'samples' },
];

const versionLabels: Record<string, string> = {
  suite_revision_id: '套件版本', platform_seq: '平台参数序号', resource_version: '资源版本',
};

// RunMetricPanel renders the spec §6.2 multidimensional run metrics: legacy
// scalar top-level keys + overall_pass_rate as a base section, by_dimension as
// a table, and version/cost/latency as compact sections. Only top-level number
// values pass through formatMetric, so nested objects can never render as
// "[object Object]".
export const RunMetricPanel = ({ metrics }: { metrics: Record<string, unknown> }) => {
  const dimensions = metrics.by_dimension as Record<string, { avg_score: number; pass_rate: number; samples: number }> | undefined;
  const version = metrics.version as Record<string, unknown> | undefined;
  const cost = metrics.cost as Record<string, number> | undefined;
  const latency = metrics.latency as Record<string, number> | undefined;

  const scalars = Object.entries(metrics).filter(([, value]) => typeof value === 'number');
  const rows: DimensionRow[] = dimensions ? Object.entries(dimensions).map(([name, d]) => ({
    name, avg_score: d.avg_score, pass_rate: d.pass_rate, samples: d.samples,
  })) : [];

  return (
    <div data-testid="run-metric-panel">
      {scalars.length > 0 && <Typography.Title level={5}>基础指标</Typography.Title>}
      {scalars.length > 0 && <Descriptions bordered size="small" column={1}>
        {scalars.map(([key, value]) => (
          <Descriptions.Item key={key} label={metricLabels[key] ?? key}>{formatMetric(key, value)}</Descriptions.Item>
        ))}
      </Descriptions>}
      <Typography.Title level={5}>语义维度</Typography.Title>
      <Table<DimensionRow>
        rowKey="name"
        size="small"
        pagination={false}
        dataSource={rows}
        columns={dimensionColumns}
        locale={{ emptyText: '无 judge 维度数据' }}
      />
      {version && <Typography.Title level={5}>版本锚点</Typography.Title>}
      {version && <Descriptions bordered size="small" column={1}>
        {Object.entries(version).map(([key, value]) => (
          <Descriptions.Item key={key} label={versionLabels[key] ?? key}>{String(value ?? '')}</Descriptions.Item>
        ))}
      </Descriptions>}
      {(cost || latency) && <Typography.Title level={5}>成本与延迟</Typography.Title>}
      {cost && <Descriptions bordered size="small" column={1}>
        <Descriptions.Item label="总成本 (USD)">{cost.total_usd?.toFixed(4)}</Descriptions.Item>
        <Descriptions.Item label="平均成本 (USD)">{cost.avg_usd?.toFixed(4)}</Descriptions.Item>
      </Descriptions>}
      {latency && <Descriptions bordered size="small" column={1}>
        <Descriptions.Item label="P50 延迟 (ms)">{latency.p50_ms}</Descriptions.Item>
        <Descriptions.Item label="P95 延迟 (ms)">{latency.p95_ms}</Descriptions.Item>
        <Descriptions.Item label="最大延迟 (ms)">{latency.max_ms}</Descriptions.Item>
      </Descriptions>}
    </div>
  );
};
```

- [ ] **Step 5: RunDrawer 换用 RunMetricPanel**

`RunDrawer.tsx` 的 metrics Tab children（89-97 行）替换为：

```tsx
          {
            key: 'metrics',
            label: '指标',
            children: metrics === null
              ? <Skeleton active paragraph={{ rows: 4 }} />
              : <RunMetricPanel metrics={metrics} />,
          },
```

把 `metricLabels` / `formatMetric`（RunDrawer.tsx:12-38）迁移到 RunMetricPanel（功能等价迁移——旧顶层标量键必须继续展示，Global Constraints 要求保留现有顶层键），RunDrawer 删除这两个函数并加 import `RunMetricPanel`。注意保留「观测事实」Tab 不变。RunDrawer 行数须 ≤200。

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation && cd .. && make fe-lint`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add web/src/modules/evaluation/model/evaluation.ts web/src/modules/evaluation/components/RunMetricPanel.tsx web/src/modules/evaluation/components/RunDrawer.tsx web/src/modules/evaluation/model/evaluation.test.ts web/src/modules/evaluation/components/RunMetricPanel.test.tsx
git commit -m "feat(evaluation): 前端多维指标展示（by_dimension/version/cost/latency）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 归因面板（case 聚类 + 单 case 下钻）

**Files:**

- Create: `web/src/modules/evaluation/components/RunAttributionPanel.tsx`
- Modify: `web/src/modules/evaluation/components/RunDrawer.tsx`（加「归因」Tab）
- Test: Create `web/src/modules/evaluation/components/RunAttributionPanel.test.tsx`

**Interfaces:**

- Consumes: `EvaluationRun.results`（含 dimensions/failure_reason/trace_id/actual/trace_evidence）
- Produces: `RunAttributionPanel({ results }: { results: EvaluationRun['results'] })`——§6.3 case 聚类（按 failure_reason 归组，每组 count + 维度摘要）+ 单 case 展开（dimensions 得分表 + trace 下钻字段）。

- [ ] **Step 1: 先写失败测试**

新建 `web/src/modules/evaluation/components/RunAttributionPanel.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { EvaluationRun } from '../../model/evaluation';
import { RunAttributionPanel } from './RunAttributionPanel';

const results: EvaluationRun['results'] = [
  { case_id: 'c1', passed: false, failure_reason: 'dimension:faithfulness',
    trace_id: 't-1', actual: '回复不准确',
    dimensions: [{ name: 'faithfulness', score: 0.3, passed: false, confidence: 0.7 }] },
  { case_id: 'c2', passed: false, failure_reason: 'dimension:faithfulness',
    dimensions: [{ name: 'faithfulness', score: 0.4, passed: false, confidence: 0.6 }] },
  { case_id: 'c3', passed: true },
];

describe('RunAttributionPanel', () => {
  it('clusters failed cases by failure_reason and drills into one case', () => {
    render(<RunAttributionPanel results={results} />);
    expect(screen.getByText('dimension:faithfulness')).toBeInTheDocument();
    expect(screen.getByText('2 个失败用例')).toBeInTheDocument();
    fireEvent.click(screen.getByText('c1'));
    expect(screen.getByText('faithfulness')).toBeInTheDocument();
    expect(screen.getByText('t-1')).toBeInTheDocument();
    expect(screen.queryByText('c3')).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation/components/RunAttributionPanel.test.tsx`
Expected: FAIL（组件不存在）。

- [ ] **Step 3: 实现 RunAttributionPanel**

新建 `web/src/modules/evaluation/components/RunAttributionPanel.tsx`：

```tsx
import { Descriptions, Empty, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo, useState } from 'react';

import type { DimensionScore, EvaluationRun } from '../../model/evaluation';

type ClusterRow = { key: string; reason: string; count: number; failedCaseIds: string[] };

// RunAttributionPanel implements spec §6.3 case-clustering attribution: failed
// cases grouped by failure_reason, with per-cluster count and a single-case
// drill-down showing dimension scores, trace id and actual output.
export const RunAttributionPanel = ({ results }: { results: EvaluationRun['results'] }) => {
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const clusters = useMemo<ClusterRow[]>(() => {
    const map = new Map<string, { count: number; ids: string[] }>();
    for (const r of results) {
      if (r.passed || !r.failure_reason) {
        continue;
      }
      const entry = map.get(r.failure_reason) ?? { count: 0, ids: [] };
      entry.count += 1;
      entry.ids.push(r.case_id);
      map.set(r.failure_reason, entry);
    }
    return [...map.entries()].map(([reason, e]) => ({
      key: reason, reason, count: e.count, failedCaseIds: e.ids,
    }));
  }, [results]);

  const columns: ColumnsType<ClusterRow> = [
    { title: '失败归因', dataIndex: 'reason', key: 'reason' },
    { title: '失败用例', dataIndex: 'count', key: 'count', render: (v: number) => `${v} 个失败用例` },
  ];

  const selected = results.find((r) => r.case_id === selectedId && !r.passed);

  return (
    <div data-testid="run-attribution-panel">
      <Typography.Title level={5}>失败聚类</Typography.Title>
      <Table<ClusterRow>
        rowKey="key"
        size="small"
        pagination={false}
        dataSource={clusters}
        columns={columns}
        expandable={{
          expandedRowRender: (row) => (
            <div>
              {row.failedCaseIds.map((id) => (
                <a key={id} role="button" onClick={() => setSelectedId(id)} style={{ marginRight: 12 }}>{id}</a>
              ))}
            </div>
          ),
        }}
        locale={{ emptyText: '没有失败用例' }}
      />
      {selected && <CaseDrillDown result={selected} />}
    </div>
  );
};

const CaseDrillDown = ({ result }: { result: NonNullable<EvaluationRun['results'][number]> }) => (
  <div data-testid="case-drill-down">
    <Typography.Title level={5}>用例 {result.case_id}</Typography.Title>
    {result.dimensions && result.dimensions.length > 0 && (
      <Table<DimensionScore>
        rowKey="name"
        size="small"
        pagination={false}
        dataSource={result.dimensions}
        columns={dimensionScoreColumns}
      />
    )}
    <Descriptions bordered size="small" column={1}>
      <Descriptions.Item label="Trace">{result.trace_id || '无'}</Descriptions.Item>
      <Descriptions.Item label="实际输出">{typeof result.actual === 'string' ? result.actual : JSON.stringify(result.actual)}</Descriptions.Item>
      <Descriptions.Item label="失败归因">{result.failure_reason}</Descriptions.Item>
    </Descriptions>
  </div>
);

const dimensionScoreColumns: ColumnsType<DimensionScore> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '得分', dataIndex: 'score', key: 'score', render: (v: number) => v.toFixed(3) },
  { title: '通过', dataIndex: 'passed', key: 'passed', render: (v: boolean) => (v ? '是' : '否') },
  { title: '置信度', dataIndex: 'confidence', key: 'confidence', render: (v: number) => (v == null ? '-' : v.toFixed(2)) },
];
```

- [ ] **Step 4: RunDrawer 加「归因」Tab**

`RunDrawer.tsx` 的 Tabs items 追加（在 metrics Tab 之后）：

```tsx
          {
            key: 'attribution',
            label: '归因',
            children: <RunAttributionPanel results={runResults} />,
          },
```

其中 `runResults` 为 run 详情返回的 results（RunDrawer 当前只 state 了 metrics；需要再加 `results` state，在 getRun 回调一并 set）。RunDrawer 整体 ≤200 行，超出则把「观测事实」Tab 提取。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation && cd .. && make fe-lint`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add web/src/modules/evaluation/components/RunAttributionPanel.tsx web/src/modules/evaluation/components/RunAttributionPanel.test.tsx web/src/modules/evaluation/components/RunDrawer.tsx
git commit -m "feat(evaluation): 归因面板（case 聚类 + 单 case 维度/trace 下钻）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 同集版本对比视图（§6.3 归因第一层）

**Files:**

- Create: `web/src/modules/evaluation/components/CompareRunsPanel.tsx`
- Modify: `web/src/modules/evaluation/components/RunDrawer.tsx`（加「版本对比」Tab）
- Modify: `web/src/modules/evaluation/api/evaluation.api.ts`（如需暴露 listRuns）
- Test: Create `web/src/modules/evaluation/components/CompareRunsPanel.test.tsx`

**Interfaces:**

- Consumes: 当前 run（RunSummary + getRun detail）、同资源 run 列表（runs prop）、`evaluationApi.getRun`
- Produces: `CompareRunsPanel({ currentId, runs, getRun })`——同资源选择另一 run 作对比基线，拉两个详情，输出 by_dimension 逐维度差异表（avg_score/pass_rate 差）与版本信息。

- [ ] **Step 1: 复用现有 listRuns API**

`listRuns(filters?: EvaluationCenterFilters)` 已存在于 `evaluation.api.ts:54`，返回 `runPageSchema`；`EvaluationCenterPage.tsx:88` 的 `center.runs.items` 就是 `RunSummary[]`。**不要改 api 文件**，`CompareRunsPanel` 的 `runs` prop 直接用 `center.runs.items`（同资源 run 列表已由 Center 过滤）。

- [ ] **Step 2: 先写失败测试**

新建 `web/src/modules/evaluation/components/CompareRunsPanel.test.tsx`：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { RunSummary } from '../../model/evaluation';
import { CompareRunsPanel } from './CompareRunsPanel';

const runs: RunSummary[] = [
  { id: 'r-v1', resource_id: 's1', revision_id: 'v1', status: 'succeeded', resource_kind: 'skill', passed: true, total_cases: 2, passed_cases: 2, created_at: '2026-08-30T00:00:00Z' },
  { id: 'r-v2', resource_id: 's1', revision_id: 'v2', status: 'succeeded', resource_kind: 'skill', passed: false, total_cases: 2, passed_cases: 1, created_at: '2026-08-31T00:00:00Z' },
];

const getRun = vi.fn().mockImplementation(async (id: string) => ({
  id,
  resource: { kind: 'skill', resource_id: 's1', revision_id: id === 'r-v2' ? 'v2' : 'v1' },
  suite_revision_id: 'rev-1', passed: true, total_cases: 2, passed_cases: 1, created_at: '',
  metrics: {
    by_dimension: {
      faithfulness: { avg_score: id === 'r-v2' ? 0.6 : 0.8, pass_rate: id === 'r-v2' ? 0.5 : 1, samples: 2 },
    },
  },
  results: [],
}));

describe('CompareRunsPanel', () => {
  it('compares by_dimension between two runs of the same resource', async () => {
    render(<CompareRunsPanel currentId="r-v2" runs={runs} getRun={getRun} />);
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '对比目标' }));
    fireEvent.click(await screen.findByText('v1'));
    await waitFor(() => expect(screen.getByText('faithfulness')).toBeInTheDocument());
    expect(screen.getByText('-0.200')).toBeInTheDocument(); // 0.6 - 0.8
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation/components/CompareRunsPanel.test.tsx`
Expected: FAIL（组件不存在）。

- [ ] **Step 4: 实现 CompareRunsPanel**

新建 `web/src/modules/evaluation/components/CompareRunsPanel.tsx`：

```tsx
import { Descriptions, Empty, Select, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useState } from 'react';

import type { EvaluationRun, RunSummary } from '../../model/evaluation';

type CompareRow = { name: string; baseScore: number; targetScore: number; delta: number; basePassRate: number; targetPassRate: number };

const columns: ColumnsType<CompareRow> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '基线平均分', dataIndex: 'baseScore', key: 'baseScore', render: (v: number) => v.toFixed(3) },
  { title: '对比平均分', dataIndex: 'targetScore', key: 'targetScore', render: (v: number) => v.toFixed(3) },
  { title: '差异', dataIndex: 'delta', key: 'delta', render: (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(3)}` },
  { title: '基线通过率', dataIndex: 'basePassRate', key: 'basePassRate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
  { title: '对比通过率', dataIndex: 'targetPassRate', key: 'targetPassRate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
];

type GetRun = (runId: string) => Promise<EvaluationRun>;

// CompareRunsPanel implements spec §6.3 attribution layer 1: same-suite /
// same-resource version comparison. The current run (target) is compared
// against a selected base run; per-dimension metric deltas surface which
// dimension regressed between the two versions.
export const CompareRunsPanel = ({ currentId, runs, getRun }: {
  currentId: string; runs: RunSummary[]; getRun: GetRun;
}) => {
  const [baseId, setBaseId] = useState<string | undefined>();
  const [base, setBase] = useState<EvaluationRun | null>(null);
  const [target, setTarget] = useState<EvaluationRun | null>(null);

  const candidates = useMemo(() => runs.filter((r) => r.id !== currentId), [runs, currentId]);

  const load = useCallback(async (id: string) => {
    const detail = await getRun(id);
    if (id === currentId) {
      setTarget(detail);
    } else {
      setBase(detail);
    }
  }, [currentId, getRun]);

  useEffect(() => {
    void load(currentId);
  }, [currentId, load]);

  useEffect(() => {
    if (baseId) {
      void load(baseId);
    }
  }, [baseId, load]);

  if (candidates.length === 0) {
    return <Empty description="该资源只有一次运行，无法对比" />;
  }

  const baseDim = base?.metrics?.by_dimension as Record<string, { avg_score: number; pass_rate: number }> | undefined;
  const targetDim = target?.metrics?.by_dimension as Record<string, { avg_score: number; pass_rate: number }> | undefined;
  const names = new Set([...(baseDim ? Object.keys(baseDim) : []), ...(targetDim ? Object.keys(targetDim) : [])]);
  const rows: CompareRow[] = [...names].map((name) => {
    const baseScore = baseDim?.[name]?.avg_score ?? 0;
    const targetScore = targetDim?.[name]?.avg_score ?? 0;
    return {
      name,
      baseScore,
      targetScore,
      delta: targetScore - baseScore,
      basePassRate: baseDim?.[name]?.pass_rate ?? 0,
      targetPassRate: targetDim?.[name]?.pass_rate ?? 0,
    };
  });

  return (
    <div data-testid="compare-runs-panel">
      <Select
        aria-label="对比目标"
        style={{ width: 240, marginBottom: 16 }}
        placeholder="选择基线运行"
        value={baseId}
        onChange={setBaseId}
        options={candidates.map((r) => ({ value: r.id, label: `${r.revision_id}（${new Date(r.created_at).toLocaleDateString('zh-CN')}）` }))}
      />
      {base && target && <Table<CompareRow> rowKey="name" size="small" pagination={false} dataSource={rows} columns={columns} />}
      {base && <Descriptions bordered size="small" column={1} style={{ marginTop: 16 }}>
        <Descriptions.Item label="基线套件版本">{base.suite_revision_id}</Descriptions.Item>
        <Descriptions.Item label="对比套件版本">{target?.suite_revision_id}</Descriptions.Item>
      </Descriptions>}
    </div>
  );
};
```

- [ ] **Step 5: RunDrawer 加「版本对比」Tab**

`RunDrawer.tsx` 加 props：`runs: RunSummary[]`（同资源 runs，由 EvaluationCenterPage 传入 `center.runs.items`）。Tabs items 追加：

```tsx
          {
            key: 'compare',
            label: '版本对比',
            children: <CompareRunsPanel currentId={run.id} runs={runs} getRun={evaluationApi.getRun} />,
          },
```

EvaluationCenterPage.tsx 的 `<RunDrawer .../>`（110 行）补 `runs={center.runs.items}`。RunDrawer 若超 200 行，提取「观测事实」Tab 为独立组件。

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /home/yang/go-projects/stratum-p3c/web && npx vitest run src/modules/evaluation && cd .. && make fe-lint && make fe-build`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
cd /home/yang/go-projects/stratum-p3c
git add web/src/modules/evaluation/components/CompareRunsPanel.tsx web/src/modules/evaluation/components/CompareRunsPanel.test.tsx web/src/modules/evaluation/components/RunDrawer.tsx web/src/modules/evaluation/pages/EvaluationCenterPage.tsx web/src/modules/evaluation/api/evaluation.api.ts
git commit -m "feat(evaluation): 同集版本对比视图（§6.3 逐维度差异表）

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage（§6.2/6.3）：**

- §6.2 `result.dimensions`（多维分数 + confidence）→ Task 1（judge 解析）+ Task 2（case 落链）
- §6.2 `result.failure_reason`（dimension/assert 两级；span 留 trace 下钻）→ Task 2
- §6.2 `run.metrics.overall_pass_rate / by_dimension / cost / latency / version` → Task 3
- §6.2 双版本锚点 `version{suite_revision_id, platform_seq, resource_version}` → Task 3（wiring 注入 platform_seq）
- §6.3 同集版本对比 → Task 7；case 聚类 → Task 6；trace 下钻 → Task 6（单 case trace_id/TraceEvidence 展示）
- 明确降级：by_category（无 case 分类字段）、safety/format/tool_pass/step_reasoning/behavior 维度（规则断言无多维分数、无行为信号）——Global Constraints 已声明，不静默实现。

**Placeholder scan：** 全部步骤含完整代码；测试中 `fakeLLMJudge`/`fakeAdapter` 等 mock 标注「若文件已有等价 helper 复用」，实现时以读文件为准，不得凭空新造命名冲突。

**Type consistency：**

- `DimensionScore` 字段（name/score/passed/reason/confidence）在 Task 1 定义，Task 2/3 沿用一致
- `EvalCaseResult.Dimensions/FailureReason` json tag 在 Task 2 定义，Task 4 落库、Task 5/6 前端 zod 沿用
- `aggregateRunMetrics(run, runVersionAnchor{...})` 签名 Task 3 定，唯一调用点 service.go 同步更新
- 前端 `dimensionScoreSchema`（Task 5）与后端 `DimensionScore` json 字段一一对应
- `RunMetricPanel`/`RunAttributionPanel`/`CompareRunsPanel` 各自 props 在 Task 5/6/7 定义并跨 Task 一致
