package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestServiceRunEvaluatesEnabledCasesAndPersistsResults(t *testing.T) {
	adapter := &fakeAdapter{outputs: map[string]any{
		"case-1": "订单已经发货",
		"case-2": map[string]any{"label": "refund"},
	}}
	repo := &fakeRunRepo{}
	svc := NewService(adapter, repo)

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
	svc := NewService(adapter, repo)

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
	svc := NewService(adapter, runRepo, suiteRepo)

	run, err := svc.RunStored(context.Background(), "tenant-1", domain.ResourceRef{
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
	svc := NewService(&fakeAdapter{}, repo)

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

func (f *fakeAdapter) ExecuteRevision(_ context.Context, tenantID string, _ domain.ResourceRef, c domain.EvalCase) (ExecutionResult, error) {
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
