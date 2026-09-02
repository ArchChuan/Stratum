package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// attributionTestRun builds an EvalRun whose failed case attribution keys
// carry classifier keywords (spec §9 / §6.2 failure_reason semantics).
func attributionTestRun(results []domain.EvalCaseResult) domain.EvalRun {
	return domain.EvalRun{
		ID:              "run-attrib-1",
		Resource:        domain.ResourceRef{Kind: domain.ResourceKindAgent, ResourceID: "agent-x"},
		SuiteRevisionID: "rev-1",
		TotalCases:      len(results),
		Results:         results,
	}
}

func failedCase(id, failureReason, processFailure, message string) domain.EvalCaseResult {
	return domain.EvalCaseResult{CaseID: id, Passed: false,
		FailureReason: failureReason, ProcessFailure: processFailure, Message: message}
}

func TestAnalyzeRunEmptyWhenNoFailures(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		{CaseID: "a", Passed: true},
		{CaseID: "b", Passed: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 0 {
		t.Fatalf("FailedCases = %d, want 0", report.FailedCases)
	}
	if report.Diagnosis.TotalFailures != 0 {
		t.Fatalf("Diagnosis.TotalFailures = %d, want 0", report.Diagnosis.TotalFailures)
	}
	if len(report.Clusters) != 0 || len(report.TunableDirections) != 0 {
		t.Fatalf("expected no clusters/directions for a passing run, got %d/%d",
			len(report.Clusters), len(report.TunableDirections))
	}
	if report.Advanceable {
		t.Fatal("a passing run must not be advanceable")
	}
}

func TestAnalyzeRunClassifiesTimeoutToModelConfig(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		failedCase("case-timeout-retry", "execution", "", "context deadline exceeded; retry exhausted"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if report.FailedCases != 1 || report.Diagnosis.TotalFailures != 1 {
		t.Fatalf("want 1 attributed failure, got run=%d diagnosis=%d",
			report.FailedCases, report.Diagnosis.TotalFailures)
	}
	if report.Diagnosis.CategoryBreakdown[domain.CatModelConfig] < 1 {
		t.Errorf("expected model_config category from timeout failure, got %+v",
			report.Diagnosis.CategoryBreakdown)
	}
	// Model-config tunables surface as concrete adjustment directions.
	assertHasDirection(t, report, "temperature")
	if !report.Advanceable {
		t.Fatal("a failed run with directions must be advanceable")
	}
}

func TestAnalyzeRunClassifiesToolExecToRetriesTimeout(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		failedCase("tool-schema-case", "execution", "", "invalid json: structured output parse error"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if report.Diagnosis.CategoryBreakdown[domain.CatToolExec] < 1 {
		t.Errorf("expected tool_exec category, got %+v", report.Diagnosis.CategoryBreakdown)
	}
	assertHasDirection(t, report, "max_retries")
	assertHasDirection(t, report, "timeout_ms")
}

func TestAnalyzeRunClustersByFailureReason(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		failedCase("timeout-1", "execution", "", "timed out"),
		failedCase("timeout-2", "execution", "", "context deadline exceeded"),
		failedCase("dim-1", "dimension:relevance_score", "", "low relevance"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Clusters) != 2 {
		t.Fatalf("expected 2 clusters (execution / dimension), got %d: %+v",
			len(report.Clusters), report.Clusters)
	}
	for _, c := range report.Clusters {
		switch c.Reason {
		case "execution":
			if c.Count != 2 || len(c.Cases) != 2 {
				t.Errorf("execution cluster = %+v, want 2 cases", c)
			}
		case "dimension:relevance_score":
			if c.Count != 1 {
				t.Errorf("dimension cluster = %+v, want 1", c)
			}
		default:
			t.Errorf("unexpected cluster reason %q", c.Reason)
		}
	}
}

func TestAnalyzeRunJudgeDimensionFlagsEvalChainDirection(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		failedCase("dim-1", "dimension:relevance_score", "", "output not grounded in context"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Judge dimension failures cannot be fixed by grid-searching agent params:
	// the attribution must point at the (platform-scoped, non-optimizable)
	// evaluation.judge.* parameters instead of the agent search space.
	if len(report.EvalChainDirections) == 0 {
		t.Fatal("expected an eval-chain direction for a judge dimension failure")
	}
	found := false
	for _, d := range report.EvalChainDirections {
		if d.PlatformKey == "evaluation.judge.temperature" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected evaluation.judge.temperature direction, got %+v",
			report.EvalChainDirections)
	}
}

func TestAnalyzeRunProcessAssertionFlagsRuleGuardDirection(t *testing.T) {
	svc := NewAttributionService()
	report, err := svc.AnalyzeRun(context.Background(), attributionTestRun([]domain.EvalCaseResult{
		failedCase("delete-hit", "", "process:must_not_call:delete", ""),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Clusters) != 1 || report.Clusters[0].Reason != "process:must_not_call:delete" {
		t.Fatalf("expected process cluster, got %+v", report.Clusters)
	}
	found := false
	for _, d := range report.EvalChainDirections {
		if d.PlatformKey == "evaluation.ruleguard.enabled" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected evaluation.ruleguard.enabled direction, got %+v",
			report.EvalChainDirections)
	}
}

func assertHasDirection(t *testing.T, report AttributionReport, key string) {
	t.Helper()
	for _, d := range report.TunableDirections {
		if d.Key == key {
			return
		}
	}
	t.Errorf("expected tunable direction %q, got %+v", key, report.TunableDirections)
}
