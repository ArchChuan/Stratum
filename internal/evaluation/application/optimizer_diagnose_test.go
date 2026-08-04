package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestClassifyFailureSecurityKeywords(t *testing.T) {
	tests := []struct {
		name     string
		failure  domain.FailureSummary
		category domain.TunableCategory
		minConf  float64
	}{
		{
			name: "security violation in output",
			failure: domain.FailureSummary{
				CaseName:    "security_check",
				Description: "output contained injection attempt",
			},
			category: domain.CatPrompt,
			minConf:  0.85,
		},
		{
			name: "timeout exhaustion",
			failure: domain.FailureSummary{
				CaseName:    "timeout_test",
				Description: "context deadline exceeded after retry exhaust",
			},
			category: domain.CatModelConfig,
			minConf:  0.8,
		},
		{
			name: "tool call parse error",
			failure: domain.FailureSummary{
				CaseName:    "tool_test",
				Description: "invalid json in tool call argument",
			},
			category: domain.CatToolExec,
			minConf:  0.75,
		},
		{
			name: "retrieval failure",
			failure: domain.FailureSummary{
				CaseName:    "rag_test",
				Description: "knowledge retrieval returned irrelevant documents",
			},
			category: domain.CatRAG,
			minConf:  0.75,
		},
		{
			name: "planning failure",
			failure: domain.FailureSummary{
				CaseName:    "planning_test",
				Description: "multi-step reasoning loop did not converge",
			},
			category: domain.CatPlanning,
			minConf:  0.7,
		},
		{
			name: "compaction failure",
			failure: domain.FailureSummary{
				CaseName:    "compaction_test",
				Description: "summary truncated context limit exceeded",
			},
			category: domain.CatCompaction,
			minConf:  0.7,
		},
		{
			name: "memory forgetfulness",
			failure: domain.FailureSummary{
				CaseName:    "memory_test",
				Description: "agent forgot conversation history",
			},
			category: domain.CatContextMemory,
			minConf:  0.65,
		},
		{
			name: "prompt quality issue",
			failure: domain.FailureSummary{
				CaseName:    "quality_test",
				Description: "hallucination in output quality",
			},
			category: domain.CatPrompt,
			minConf:  0.55,
		},
		{
			name: "unclassified falls back to prompt",
			failure: domain.FailureSummary{
				CaseName:    "unknown_test",
				Description: "something went wrong",
			},
			category: domain.CatPrompt,
			minConf:  0.25,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat, conf, _ := classifyFailure(tc.failure)
			if cat != tc.category {
				t.Fatalf("expected category %s, got %s", tc.category, cat)
			}
			if conf < tc.minConf {
				t.Fatalf("confidence %.2f < min %.2f", conf, tc.minConf)
			}
		})
	}
}

func TestDiagnoseEmptyFailures(t *testing.T) {
	d := NewDiagnoser(nil)
	report, err := d.Diagnose(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalFailures != 0 {
		t.Fatalf("expected 0 failures, got %d", report.TotalFailures)
	}
}

func TestDiagnoseClassifiesAndMergesHypotheses(t *testing.T) {
	d := NewDiagnoser(nil)
	failures := []domain.FailureSummary{
		{CaseName: "case-1", Description: "timeout exceeded"},
		{CaseName: "case-2", Description: "deadline exceeded"},
		{CaseName: "case-3", Description: "invalid json in tool call"},
		{CaseName: "case-4", Description: "injection detected"},
	}

	report, err := d.Diagnose(context.Background(), failures, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalFailures != 4 {
		t.Fatalf("expected 4 failures, got %d", report.TotalFailures)
	}
	if len(report.RootCauseHypotheses) == 0 {
		t.Fatal("expected at least one hypothesis")
	}
	if len(report.AffectedTunables) == 0 {
		t.Fatal("expected affected tunables")
	}

	// Security should be highest confidence.
	if report.RootCauseHypotheses[0].Category != domain.CatPrompt {
		t.Fatalf("expected security/prompt as top hypothesis, got %s",
			report.RootCauseHypotheses[0].Category)
	}
}

func TestDiagnoseCollectsSecurityViolationsFromTraceEvidence(t *testing.T) {
	d := NewDiagnoser(nil)
	failures := []domain.FailureSummary{
		{CaseName: "case-1", Description: "timeout"},
	}
	caseResults := []domain.EvalCaseResult{
		{TraceEvidence: &domain.ObservedTraceEvidence{SecurityViolation: true}},
	}

	report, err := d.Diagnose(context.Background(), failures, caseResults)
	if err != nil {
		t.Fatal(err)
	}
	if report.CategoryBreakdown[domain.CatPrompt] < 1 {
		t.Fatal("expected prompt category bump from security violation")
	}
}

func TestMergeHypothesesDeduplicatesCategories(t *testing.T) {
	hs := []domain.RootCauseHypothesis{
		{Category: domain.CatPrompt, Confidence: 0.9, Evidence: []string{"a"}},
		{Category: domain.CatPrompt, Confidence: 0.5, Evidence: []string{"b"}},
		{Category: domain.CatModelConfig, Confidence: 0.8, Evidence: []string{"c"}},
	}
	merged := mergeHypotheses(hs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged, got %d", len(merged))
	}
	if merged[0].Confidence != 0.9 {
		t.Fatalf("expected confidence 0.9, got %.2f", merged[0].Confidence)
	}
}
