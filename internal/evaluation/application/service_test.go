package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
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
	return ExecutionResult{Output: f.outputs[c.ID], TraceID: "trace-" + c.ID, Tokens: 10, CostUSD: 0.01, DurationMs: 20}, nil
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
	err     error
	got     port.JudgeRequest
	calls   int
}

func (f *fakeLLMJudge) Enabled(_ context.Context) bool { return f.enabled }
func (f *fakeLLMJudge) Judge(_ context.Context, req port.JudgeRequest) (domain.AssertionResult, error) {
	f.calls++
	f.got = req
	return f.result, f.err
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
