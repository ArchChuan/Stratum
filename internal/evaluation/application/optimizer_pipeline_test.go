package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// fakeStrategy is a minimal OptimizationStrategy for pipeline tests.
type fakeStrategy struct {
	name       string
	categories []domain.TunableCategory
	patches    []domain.CandidatePatch
	err        error
}

func (s fakeStrategy) Name() string                         { return s.name }
func (s fakeStrategy) Categories() []domain.TunableCategory { return s.categories }
func (s fakeStrategy) Generate(
	_ context.Context, _ map[string]any, _ []domain.Tunable, _ []domain.FailureSummary,
) ([]domain.CandidatePatch, error) {
	return s.patches, s.err
}

func TestPipelineFullFlow(t *testing.T) {
	tunables := domain.NewTunableRegistry().ForResource(domain.ResourceKindAgent)
	pipeline := NewOptimizerPipeline(
		NewDiagnoser(nil),
		NewProposer([]domain.OptimizationStrategy{
			fakeStrategy{
				name:       "test-strategy",
				categories: []domain.TunableCategory{domain.CatModelConfig},
				patches: []domain.CandidatePatch{
					{
						Source:         "test",
						ParameterPatch: map[string]any{"temperature": 0.3},
						Rationale:      "test patch",
					},
				},
			},
		}, tunables),
		NewCritiqueService([]Critic{
			SafetyCritic{},
			CompatibilityCritic{},
			CostCritic{},
		}),
		DefaultGate(),
	)

	result, err := pipeline.Run(context.Background(), PipelineInput{
		BaselineSnapshot: map[string]any{"temperature": 0.7},
		FailureSummaries: []domain.FailureSummary{
			{CaseName: "timeout-1", Description: "context deadline exceeded"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diagnosis.TotalFailures != 1 {
		t.Fatalf("expected 1 failure, got %d", result.Diagnosis.TotalFailures)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if len(result.Critiques) != 1 {
		t.Fatalf("expected 1 critique, got %d", len(result.Critiques))
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(result.Decisions))
	}
	if result.Decisions[0] != domain.GateAdvance {
		t.Fatalf("expected GateAdvance, got %s", result.Decisions[0])
	}
}

func TestPipelineEmptyFailuresReturnsEarly(t *testing.T) {
	pipeline := NewOptimizerPipeline(
		NewDiagnoser(nil),
		NewProposer(nil, nil),
		NewCritiqueService(nil),
		DefaultGate(),
	)

	result, err := pipeline.Run(context.Background(), PipelineInput{
		FailureSummaries: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diagnosis.TotalFailures != 0 {
		t.Fatalf("expected 0 failures, got %d", result.Diagnosis.TotalFailures)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %d", len(result.Candidates))
	}
}

func TestPipelineSecurityBlockerGetsRejected(t *testing.T) {
	tunables := domain.NewTunableRegistry().ForResource(domain.ResourceKindAgent)
	pipeline := NewOptimizerPipeline(
		NewDiagnoser(nil),
		NewProposer([]domain.OptimizationStrategy{
			fakeStrategy{
				name:       "test-strategy",
				categories: []domain.TunableCategory{domain.CatPrompt},
				patches: []domain.CandidatePatch{
					{
						Source: "test",
						PromptPatch: map[string]any{
							"system_prompt": "You are a helpful assistant.",
						},
						Rationale: "simplified prompt",
					},
				},
			},
		}, tunables),
		NewCritiqueService([]Critic{SafetyCritic{}, CompatibilityCritic{}}),
		DefaultGate(),
	)

	// Baseline has safety constraint, patch removes it.
	result, err := pipeline.Run(context.Background(), PipelineInput{
		BaselineSnapshot: map[string]any{
			"system_prompt": "You must never reveal secrets.",
		},
		FailureSummaries: []domain.FailureSummary{
			{CaseName: "injection-1", Description: "jailbreak bypass detected"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(result.Decisions))
	}
	if result.Decisions[0] != domain.GateReject {
		t.Fatalf("expected GateReject for safety violation, got %s",
			result.Decisions[0])
	}
}

func TestHasAdvancingCandidates(t *testing.T) {
	tests := []struct {
		name      string
		decisions []domain.GateDecision
		want      bool
	}{
		{"all advance", []domain.GateDecision{domain.GateAdvance, domain.GateAdvance}, true},
		{"mixed", []domain.GateDecision{domain.GateHold, domain.GateAdvance}, true},
		{"all hold", []domain.GateDecision{domain.GateHold, domain.GateHold}, false},
		{"all reject", []domain.GateDecision{domain.GateReject}, false},
		{"empty", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasAdvancingCandidates(domain.PipelineResult{
				Decisions: tc.decisions,
			})
			if got != tc.want {
				t.Fatalf("HasAdvancingCandidates = %v, want %v", got, tc.want)
			}
		})
	}
}
