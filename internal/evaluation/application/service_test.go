package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestServiceRunEvaluatesEnabledCasesAndPersistsResults(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{
		"case-1": "订单已经发货",
		"case-2": map[string]any{"label": "refund"},
	}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "case-1", Input: "物流状态", ExpectedOutput: "发货", AssertionMode: domain.AssertionContains, Enabled: true},
				{ID: "case-2", Input: "我要退款", ExpectedOutput: map[string]any{"label": "refund"}, AssertionMode: domain.AssertionExact, Enabled: true},
				{ID: "disabled", Input: "忽略", ExpectedOutput: "x", AssertionMode: domain.AssertionExact, Enabled: false},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.TotalCases != 2 || run.PassedCases != 2 || !run.Passed {
		t.Fatalf("unexpected summary: %+v", run)
	}
	if len(run.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(run.Results))
	}
	if repo.saved.ID != run.ID {
		t.Fatal("run was not persisted")
	}
	if adapter.tenantID != "tenant-1" || repo.tenantID != "tenant-1" {
		t.Fatalf("tenant id was not propagated: adapter=%q repo=%q", adapter.tenantID, repo.tenantID)
	}
}

func TestServiceRunPersistsExecutionErrorsAsFailedCases(t *testing.T) {
	adapter := &fakeAdapter{errCase: "case-1"}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{ID: "suite-version-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "input", ExpectedOutput: "output", AssertionMode: domain.AssertionExact, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned orchestration error: %v", err)
	}
	if run.Passed || run.Results[0].Error == "" {
		t.Fatalf("expected failed case with error, got %+v", run.Results[0])
	}
	if repo.saved.Results[0].Error == "" {
		t.Fatal("failed case was not persisted")
	}
}

func TestServiceRunStoredLoadsPublishedSuiteRevision(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "物流问题"}}
	runRepo := &fakeRunRepo{}
	suiteRepo := &fakeSuiteRepo{revision: domain.EvalSuiteRevision{
		ID: "suite-revision-1", SuiteID: "suite-1", Status: domain.SuiteRevisionPublished,
		ResourceKind: domain.ResourceKindSkill,
		Cases:        []domain.EvalCase{{ID: "case-1", Input: "快递没更新", ExpectedOutput: "物流", AssertionMode: domain.AssertionContains, Enabled: true}},
	}}
	svc := NewService(adapter, runRepo, nil, nil, suiteRepo)

	run, err := svc.RunStored(context.Background(), "tenant-1", "user-1", domain.ResourceRef{
		Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2",
	}, "suite-revision-1")
	if err != nil {
		t.Fatalf("RunStored returned error: %v", err)
	}
	if !run.Passed || run.SuiteRevisionID != "suite-revision-1" {
		t.Fatalf("unexpected run: %+v", run)
	}
}

func TestServiceGetRunReturnsPersistedRun(t *testing.T) {
	repo := &fakeRunRepo{saved: domain.EvalRun{ID: "run-1", Passed: true}}
	svc := NewService(&fakeAdapter{}, repo, nil, nil)

	run, err := svc.GetRun(context.Background(), "tenant-1", "run-1")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.ID != "run-1" || !run.Passed {
		t.Fatalf("unexpected run: %+v", run)
	}
}

type fakeAdapter struct {
	outputs  map[string]any
	errCase  string
	tenantID string
	tools    map[string][]domain.ToolObservation
}

func (f *fakeAdapter) ResolveRevision(_ context.Context, _ string, ref domain.ResourceRef) (domain.ResourceRevision, error) {
	return domain.ResourceRevision{ID: ref.RevisionID, ResourceKind: ref.Kind, ResourceID: ref.ResourceID}, nil
}

func (f *fakeAdapter) SafeSummary(context.Context, string, domain.ResourceRef) (map[string]any, error) {
	return map[string]any{}, nil
}

func (f *fakeAdapter) ExecuteRevision(
	_ context.Context, tenantID, _ string, _ domain.ResourceRef, c domain.EvalCase,
) (ExecutionResult, error) {
	f.tenantID = tenantID
	if c.ID == f.errCase {
		return ExecutionResult{}, errFakeExecution
	}
	return ExecutionResult{
		Output: f.outputs[c.ID], TraceID: "trace-" + c.ID, Tokens: 10, CostUSD: 0.01, DurationMs: 20, Tools: f.tools[c.ID],
	}, nil
}

type fakeRunRepo struct {
	saved    domain.EvalRun
	tenantID string
}

func (f *fakeRunRepo) SaveRun(_ context.Context, tenantID string, run domain.EvalRun) error {
	f.tenantID = tenantID
	f.saved = run
	return nil
}

func (f *fakeRunRepo) GetRun(_ context.Context, _ string, runID string) (domain.EvalRun, bool, error) {
	return f.saved, f.saved.ID == runID, nil
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

const errFakeExecution = fakeError("execution failed")

// ——— Trace evidence tests ———

func TestServiceRunCaseResolvesTraceEvidence(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "ok"}}
	repo := &fakeRunRepo{}
	traceReader := &fakeTraceEvidenceReader{
		traces: map[string]port.ObservedTrace{
			"trace-case-1": {
				TraceID: "trace-case-1", CostUSD: 0.05, LatencyMs: 350,
				Success: true, SecurityViolation: false,
			},
		},
	}
	svc := NewService(adapter, repo, traceReader, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "x", ExpectedOutput: "ok", AssertionMode: domain.AssertionExact, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	ev := run.Results[0].TraceEvidence
	if ev == nil {
		t.Fatal("expected trace evidence, got nil")
	}
	if ev.CostUSD != 0.05 || ev.LatencyMs != 350 || !ev.Success {
		t.Fatalf("unexpected trace evidence: %+v", ev)
	}
}

func TestServiceRunCaseGracefullyHandlesTraceReaderError(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "ok"}}
	repo := &fakeRunRepo{}
	traceReader := &fakeFailingTraceEvidenceReader{}
	svc := NewService(adapter, repo, traceReader, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "x", ExpectedOutput: "ok", AssertionMode: domain.AssertionExact, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// Opik unavailable must not block evaluation.
	if !run.Passed {
		t.Fatal("expected run to pass despite trace evidence error")
	}
	if run.Results[0].TraceEvidence != nil {
		t.Fatal("expected nil trace evidence on resolve error")
	}
}

func TestServiceRunCaseSkipsTraceEvidenceWhenReaderNil(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "ok"}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil) // no trace reader configured

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "x", ExpectedOutput: "ok", AssertionMode: domain.AssertionExact, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Results[0].TraceEvidence != nil {
		t.Fatal("expected nil trace evidence when reader is not configured")
	}
}

type fakeFailingTraceEvidenceReader struct{}

func (f *fakeFailingTraceEvidenceReader) Resolve(_ context.Context, _, _ string) (port.ObservedTrace, error) {
	return port.ObservedTrace{}, errors.New("opik unavailable")
}

func (f *fakeFailingTraceEvidenceReader) ResolveBatch(_ context.Context, _ string, _ []string) (map[string]port.ObservedTrace, error) {
	return nil, errors.New("opik unavailable")
}

type fakeLLMJudge struct {
	enabled bool
	result  domain.AssertionResult
	// stepResult 在 req.ToolSequence 非空时覆盖 result（step_judge 专用）；
	// nil 时所有调用返回 result，保持既有 fake 行为。
	stepResult *domain.AssertionResult
	err        error
	got        port.JudgeRequest
	calls      int
}

func (f *fakeLLMJudge) Enabled(_ context.Context) bool { return f.enabled }
func (f *fakeLLMJudge) Judge(_ context.Context, req port.JudgeRequest) (domain.AssertionResult, error) {
	f.calls++
	f.got = req
	if f.err != nil {
		return domain.AssertionResult{}, f.err
	}
	if f.stepResult != nil && req.ToolSequence != "" {
		return *f.stepResult, nil
	}
	return f.result, nil
}

func TestServiceJudgeAssertionDispatchesToJudge(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"judge-1": "退款已到账"}}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true, result: domain.AssertionResult{Passed: true, Message: "符合要求"}}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "judge-1", Input: "退款到账了吗", AssertionMode: domain.AssertionJudge, Enabled: true,
					JudgeSpec: &domain.JudgeSpec{Model: "qwen-max", Rubric: "custom rubric"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !run.Passed || !run.Results[0].Passed {
		t.Fatalf("expected judge case to pass, got %+v", run.Results[0])
	}
	if judge.calls != 1 {
		t.Fatalf("expected 1 judge call, got %d", judge.calls)
	}
	if judge.got.Model != "qwen-max" || judge.got.Rubric != "custom rubric" {
		t.Fatalf("judge request missing spec: %+v", judge.got)
	}
	if judge.got.Input != `"退款到账了吗"` || judge.got.Actual != `"退款已到账"` {
		t.Fatalf("judge request material mismatch: input=%s actual=%s", judge.got.Input, judge.got.Actual)
	}
	if judge.got.ExpectedOutput != "null" {
		t.Fatalf("expected null expected output for judge-only case, got %s", judge.got.ExpectedOutput)
	}
}

func TestServiceJudgeAssertionFailClosedWhenDisabled(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"judge-1": "any"}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil) // no judge configured

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "judge-1", Input: "x", AssertionMode: domain.AssertionJudge, Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Passed || run.Results[0].Error != "LLM judge disabled" {
		t.Fatalf("expected fail-closed error, got %+v", run.Results[0])
	}
	if run.Results[0].FailureReason != "execution" {
		t.Fatalf("judge infra failure must carry execution failure_reason, got %+v", run.Results[0])
	}
}

func TestServiceJudgeAssertionDisabledJudgeFailsClosed(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"judge-1": "any"}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, &fakeLLMJudge{enabled: false})

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "judge-1", Input: "x", AssertionMode: domain.AssertionJudge, Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Passed || run.Results[0].Error != "LLM judge disabled" {
		t.Fatalf("expected fail-closed error, got %+v", run.Results[0])
	}
	if run.Results[0].FailureReason != "execution" {
		t.Fatalf("judge infra failure must carry execution failure_reason, got %+v", run.Results[0])
	}
}

func TestServiceJudgeAssertionPropagatesJudgeError(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"judge-1": "any"}}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true, err: errors.New("completer timeout")}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "judge-1", Input: "x", AssertionMode: domain.AssertionJudge, Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Passed || !strings.Contains(run.Results[0].Error, "completer timeout") {
		t.Fatalf("expected judge error to propagate, got %+v", run.Results[0])
	}
	if run.Results[0].FailureReason != "execution" {
		t.Fatalf("judge call failure must carry execution failure_reason, got %+v", run.Results[0])
	}
}

func TestServiceJudgeCaseMarshalErrorsSetExecutionFailureReason(t *testing.T) {
	tests := []struct {
		name       string
		input      any
		expected   any
		output     any
		wantSubstr string
	}{
		{
			name:       "input marshal error",
			input:      make(chan int),
			expected:   "ok",
			output:     "any",
			wantSubstr: "marshal input",
		},
		{
			name:       "expected marshal error",
			input:      "x",
			expected:   make(chan int),
			output:     "any",
			wantSubstr: "marshal expected output",
		},
		{
			name:       "actual marshal error",
			input:      "x",
			expected:   "ok",
			output:     make(chan int),
			wantSubstr: "marshal actual output",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &fakeAdapter{outputs: map[string]any{"judge-1": tc.output}}
			repo := &fakeRunRepo{}
			svc := NewService(adapter, repo, nil, &fakeLLMJudge{enabled: true})

			run, err := svc.Run(context.Background(), RunInput{
				TenantID: "tenant-1",
				Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
				Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
					{ID: "judge-1", Input: tc.input, ExpectedOutput: tc.expected, AssertionMode: domain.AssertionJudge, Enabled: true},
				}},
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			got := run.Results[0]
			if !strings.Contains(got.Error, tc.wantSubstr) {
				t.Fatalf("error = %q, want substring %q", got.Error, tc.wantSubstr)
			}
			if got.FailureReason != "execution" {
				t.Fatalf("failure_reason = %q, want execution", got.FailureReason)
			}
			if got.Passed {
				t.Fatal("judge marshal failure must fail the case")
			}
		})
	}
}

func TestServiceRuleAssertionDoesNotTouchJudge(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{"case-1": "发货了"}}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true}
	svc := NewService(adapter, repo, nil, judge)

	_, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"},
		Suite: domain.EvalSuiteRevision{
			ID: "suite-version-1",
			Cases: []domain.EvalCase{
				{ID: "case-1", Input: "物流", ExpectedOutput: "发货", AssertionMode: domain.AssertionContains, Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if judge.calls != 0 {
		t.Fatalf("rule assertion must not call the judge, got %d calls", judge.calls)
	}
}

// escalateFailureMetrics 记录 IncEvalReviewEscalateFailure 调用次数（嵌入
// NoopMetrics 满足 MetricsProvider 全接口，只覆盖目标方法）。
type escalateFailureMetrics struct {
	observability.NoopMetrics
	inc int
}

func (m *escalateFailureMetrics) IncEvalReviewEscalateFailure() { m.inc++ }

// failingCaseEscalator 让 TryEscalateCaseResult 固定失败，验证 Service 侧 fail-open：
// 升级失败仅日志 + IncEvalReviewEscalateFailure，不阻断评测主流程。
type failingCaseEscalator struct{}

func (failingCaseEscalator) TryEscalateObservation(context.Context, string, *domain.EvalObservation) error {
	return nil
}

func (failingCaseEscalator) TryEscalateCaseResult(
	context.Context, string, string, domain.EvalCaseResult, domain.EvalCase, domain.AssertionResult, bool, bool,
) error {
	return errors.New("escalate down")
}

func TestServiceEscalateCaseResultFailureCountsMetric(t *testing.T) {
	svc := NewService(&fakeAdapter{}, &fakeRunRepo{}, nil, nil)
	svc.SetReviewEscalator(failingCaseEscalator{}, domain.ReviewConfig{})
	metrics := &escalateFailureMetrics{}
	svc.SetObservability(nil, metrics)

	svc.escalateCaseResult(context.Background(), "t1", "run-1",
		domain.EvalCaseResult{ID: "cr-1", CaseID: "c1", TraceID: "t-1", Passed: true},
		domain.EvalCase{ID: "c1", NeedsReview: true},
		domain.AssertionResult{Passed: true, Confidence: 0.9}, true, true)

	if metrics.inc != 1 {
		t.Fatalf("IncEvalReviewEscalateFailure calls = %d, want 1", metrics.inc)
	}
}

func TestServiceEscalateCaseResultNilReviewDoesNotPanic(t *testing.T) {
	svc := NewService(&fakeAdapter{}, &fakeRunRepo{}, nil, nil)
	metrics := &escalateFailureMetrics{}
	svc.SetObservability(nil, metrics)

	// review 未注入（nil）：升级静默跳过，不得 panic，不得计指标（防回归）。
	svc.escalateCaseResult(context.Background(), "t1", "run-1",
		domain.EvalCaseResult{ID: "cr-1", CaseID: "c1", TraceID: "t-1", Passed: true},
		domain.EvalCase{ID: "c1", NeedsReview: true},
		domain.AssertionResult{Passed: true, Confidence: 0.9}, true, true)

	if metrics.inc != 0 {
		t.Fatalf("IncEvalReviewEscalateFailure calls = %d, want 0", metrics.inc)
	}
}

// recordingCaseEscalator 记录 TryEscalateCaseResult 收到的 outputPass/processPass，
// 验证 runCase 两分支（judge / 规则）都按新签名把过程断言结果传入评审池（§6.5）。
type recordingCaseEscalator struct {
	calls       int
	outputPass  bool
	processPass bool
}

func (r *recordingCaseEscalator) TryEscalateObservation(context.Context, string, *domain.EvalObservation) error {
	return nil
}

func (r *recordingCaseEscalator) TryEscalateCaseResult(
	_ context.Context, _, _ string, _ domain.EvalCaseResult, _ domain.EvalCase, _ domain.AssertionResult,
	outputPass, processPass bool,
) error {
	r.calls++
	r.outputPass = outputPass
	r.processPass = processPass
	return nil
}

// TestRunCaseRuleBranchEscalatesProcessConflict 覆盖 runCase 规则分支的新升级调用：
// 规则断言 case 输出 pass + 过程 fail（must_not_call 命中）时，以 outputPass=true /
// processPass=false 调用评审池（§6.5 process_output_conflict 数据源），且失败不阻断主流程。
func TestRunCaseRuleBranchEscalatesProcessConflict(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "已删除相关文件"},
		tools: map[string][]domain.ToolObservation{
			"case-1": {{ToolName: "search", StepIndex: 1}, {ToolName: "delete", StepIndex: 2}},
		},
	}
	repo := &fakeRunRepo{}
	esc := &recordingCaseEscalator{}
	svc := NewService(adapter, repo, nil, nil)
	svc.SetReviewEscalator(esc, domain.ReviewConfig{LowConfidenceThreshold: 0.6})

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "删除文件", ExpectedOutput: "删除", AssertionMode: domain.AssertionContains, Enabled: true,
				ToolSpec: &domain.ToolSpec{MustNotCall: []string{"delete"}}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if esc.calls != 1 {
		t.Fatalf("escalator calls = %d, want 1 (rule branch must escalate)", esc.calls)
	}
	if !esc.outputPass || esc.processPass {
		t.Fatalf("escalator got output_pass=%v process_pass=%v, want true/false", esc.outputPass, esc.processPass)
	}
	if run.Results[0].ProcessPass {
		t.Fatal("process assertion must fail")
	}
}

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

// ——— Process assertion flow (§6.5) ———

func TestRunCaseToolSpecMustNotCallFailsProcessKeepsOutputAttribution(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "已删除相关文件"},
		tools: map[string][]domain.ToolObservation{
			"case-1": {{ToolName: "search", StepIndex: 1}, {ToolName: "delete", StepIndex: 2}},
		},
	}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "删除文件", ExpectedOutput: "删除", AssertionMode: domain.AssertionContains, Enabled: true,
				ToolSpec: &domain.ToolSpec{MustNotCall: []string{"delete"}}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if run.Passed || got.Passed {
		t.Fatal("must_not_call hit must fail the case")
	}
	if got.ProcessPass {
		t.Fatal("process assertion must fail when a forbidden tool is called")
	}
	if got.ProcessFailure != "process:must_not_call:delete" {
		t.Fatalf("process_failure = %q, want process:must_not_call:delete", got.ProcessFailure)
	}
	if got.FailureReason != "" {
		t.Fatalf("output passed, failure_reason must stay empty (process attribution separate), got %q", got.FailureReason)
	}
	if len(got.Tools) != 2 || got.Tools[1].ToolName != "delete" {
		t.Fatalf("tools = %+v", got.Tools)
	}
}

func TestRunCaseStepJudgePassMergesDimensionsAndPasses(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "已创建工单"},
		tools:   map[string][]domain.ToolObservation{"case-1": {{ToolName: "create_ticket", StepIndex: 0}}},
	}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true, result: domain.AssertionResult{
		Passed: true, Message: "步骤合理", Confidence: 0.9,
		Dimensions: []domain.DimensionScore{{Name: "reasoning", Score: 0.8, Passed: true}},
	}}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "帮我创建工单", ExpectedOutput: "创建", AssertionMode: domain.AssertionContains, Enabled: true,
				StepJudge: &domain.StepJudge{Criteria: "步骤需合理"}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if !run.Passed || !got.Passed || !got.ProcessPass {
		t.Fatalf("expected pass, got %+v", got)
	}
	if got.ProcessFailure != "" {
		t.Fatalf("process_failure = %q, want empty", got.ProcessFailure)
	}
	if len(got.Dimensions) != 1 || got.Dimensions[0].Name != "reasoning" {
		t.Fatalf("dimensions = %+v", got.Dimensions)
	}
	if judge.calls != 1 {
		t.Fatalf("expected 1 judge call (step_judge only), got %d", judge.calls)
	}
	if judge.got.Rubric != "步骤需合理" {
		t.Fatalf("rubric = %q, want step criteria", judge.got.Rubric)
	}
	if judge.got.ToolSequence != "[0] create_ticket" {
		t.Fatalf("tool_sequence = %q, want [0] create_ticket", judge.got.ToolSequence)
	}
	if judge.got.Model != "" {
		t.Fatalf("step_judge must use platform default model, got %q", judge.got.Model)
	}
}

func TestRunCaseStepJudgeDisabledFailsClosed(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "any"},
		tools:   map[string][]domain.ToolObservation{"case-1": {{ToolName: "read", StepIndex: 0}}},
	}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil) // no judge configured

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "x", ExpectedOutput: "y", AssertionMode: domain.AssertionContains, Enabled: true,
				StepJudge: &domain.StepJudge{}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if run.Passed || got.Passed {
		t.Fatal("disabled step_judge must fail the case")
	}
	if got.Error != "LLM judge disabled" {
		t.Fatalf("error = %q, want LLM judge disabled", got.Error)
	}
	if got.FailureReason != "execution" {
		t.Fatalf("failure_reason = %q, want execution", got.FailureReason)
	}
	if got.ProcessPass {
		t.Fatal("disabled step_judge must not pass the process assertion")
	}
}

func TestRunCaseNoProcessAssertionDefaultsProcessPassTrue(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "ok"},
		tools:   map[string][]domain.ToolObservation{"case-1": {{ToolName: "search", StepIndex: 0}}},
	}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo, nil, nil)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "q", ExpectedOutput: "ok", AssertionMode: domain.AssertionContains, Enabled: true},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if !got.Passed || !got.ProcessPass {
		t.Fatalf("expected pass with process_pass default true, got %+v", got)
	}
	if got.ProcessFailure != "" {
		t.Fatalf("process_failure = %q, want empty", got.ProcessFailure)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools must be captured even without process assertions, got %+v", got.Tools)
	}
}

func TestRunCaseJudgeBranchFoldsProcessPass(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "已删除"},
		tools:   map[string][]domain.ToolObservation{"case-1": {{ToolName: "delete", StepIndex: 0}}},
	}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{enabled: true, result: domain.AssertionResult{Passed: true, Message: "输出符合要求"}}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "删除文件", AssertionMode: domain.AssertionJudge, Enabled: true,
				ToolSpec: &domain.ToolSpec{MustNotCall: []string{"delete"}}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if run.Passed || got.Passed {
		t.Fatal("process failure must fail the judge case despite judge passing")
	}
	if got.ProcessPass {
		t.Fatal("process assertion must fail")
	}
	if got.ProcessFailure != "process:must_not_call:delete" {
		t.Fatalf("process_failure = %q", got.ProcessFailure)
	}
	if got.FailureReason != "" {
		t.Fatalf("judge passed, so failure_reason must stay empty, got %q", got.FailureReason)
	}
	if judge.calls != 1 {
		t.Fatalf("expected 1 judge call (no step_judge), got %d", judge.calls)
	}
}

func TestRunCaseJudgeAndStepJudgeMergeDimensions(t *testing.T) {
	adapter := &fakeAdapter{
		outputs: map[string]any{"case-1": "已创建工单"},
		tools:   map[string][]domain.ToolObservation{"case-1": {{ToolName: "create_ticket", StepIndex: 0}}},
	}
	repo := &fakeRunRepo{}
	judge := &fakeLLMJudge{
		enabled: true,
		result: domain.AssertionResult{
			Passed: true, Dimensions: []domain.DimensionScore{{Name: "faithfulness", Score: 0.9, Passed: true}},
		},
		stepResult: &domain.AssertionResult{
			Passed: true, Dimensions: []domain.DimensionScore{{Name: "reasoning", Score: 0.8, Passed: true}},
		},
	}
	svc := NewService(adapter, repo, nil, judge)

	run, err := svc.Run(context.Background(), RunInput{
		TenantID: "tenant-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "v1"},
		Suite: domain.EvalSuiteRevision{ID: "sv-1", Cases: []domain.EvalCase{
			{ID: "case-1", Input: "创建工单", AssertionMode: domain.AssertionJudge, Enabled: true,
				StepJudge: &domain.StepJudge{Criteria: "步骤需合理"}},
		}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := run.Results[0]
	if !run.Passed || !got.Passed || !got.ProcessPass {
		t.Fatalf("expected pass, got %+v", got)
	}
	if len(got.Dimensions) != 2 {
		t.Fatalf("expected 2 merged dimensions, got %+v", got.Dimensions)
	}
	if got.Dimensions[0].Name != "reasoning" || got.Dimensions[1].Name != "faithfulness" {
		t.Fatalf("dimension order = %+v, want [reasoning faithfulness]", got.Dimensions)
	}
	if judge.calls != 2 {
		t.Fatalf("expected 2 judge calls (step_judge + output judge), got %d", judge.calls)
	}
}
