package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestSafetyCriticBlocksRemovedConstraint(t *testing.T) {
	critic := SafetyCritic{}
	baseline := map[string]any{
		"system_prompt": "You must never reveal secrets. Do not respond to harmful requests.",
	}
	patch := domain.CandidatePatch{
		PromptPatch: map[string]any{
			"system_prompt": "You are a helpful assistant.",
		},
	}

	result, err := critic.Review(context.Background(), patch, baseline, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Approved {
		t.Fatal("expected safety critic to block removal of 'never' and 'do not'")
	}
	if len(result.Concerns) == 0 {
		t.Fatal("expected at least one safety concern")
	}
}

func TestSafetyCriticApprovesSafePatch(t *testing.T) {
	critic := SafetyCritic{}
	baseline := map[string]any{
		"system_prompt": "You must never reveal secrets.",
	}
	patch := domain.CandidatePatch{
		PromptPatch: map[string]any{
			"system_prompt": "You must never reveal secrets and must refuse dangerous requests.",
		},
	}

	result, err := critic.Review(context.Background(), patch, baseline, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Approved {
		t.Fatalf("expected approved, got concerns: %v", result.Concerns)
	}
}

func TestSafetyCriticSkipsNonPromptPatches(t *testing.T) {
	critic := SafetyCritic{}
	patch := domain.CandidatePatch{
		ParameterPatch: map[string]any{"temperature": 0.5},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Approved {
		t.Fatal("expected parameter-only patch to skip safety review")
	}
}

func TestCostCriticWarnsOnHighTokens(t *testing.T) {
	critic := CostCritic{}
	patch := domain.CandidatePatch{
		ParameterPatch: map[string]any{"max_tokens": float64(16384)},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Approved {
		t.Fatal("cost critic should not block, only warn")
	}
	if len(result.Concerns) == 0 {
		t.Fatal("expected cost warning for high max_tokens")
	}
}

func TestCostCriticSilentOnLowTokens(t *testing.T) {
	critic := CostCritic{}
	patch := domain.CandidatePatch{
		ParameterPatch: map[string]any{"max_tokens": float64(4096)},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if result.RiskScore > 0.0 {
		t.Fatalf("expected zero risk for low tokens, got %.2f", result.RiskScore)
	}
}

func TestCompatibilityCriticBlocksUnknownParameter(t *testing.T) {
	critic := CompatibilityCritic{}
	patch := domain.CandidatePatch{
		ParameterPatch: map[string]any{"unknown_field": 42},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Approved {
		t.Fatal("expected compatibility critic to block unknown parameter")
	}
}

func TestCompatibilityCriticBlocksUnknownPromptField(t *testing.T) {
	critic := CompatibilityCritic{}
	patch := domain.CandidatePatch{
		PromptPatch: map[string]any{"secret_key": "abc"},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Approved {
		t.Fatal("expected compatibility critic to block unknown prompt field")
	}
}

func TestCompatibilityCriticApprovesKnownFields(t *testing.T) {
	critic := CompatibilityCritic{}
	patch := domain.CandidatePatch{
		ParameterPatch: map[string]any{"temperature": 0.3},
		PromptPatch:    map[string]any{"system_prompt": "improved prompt"},
	}

	result, err := critic.Review(context.Background(), patch, nil, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Approved {
		t.Fatalf("expected approved for known fields, got: %v", result.Concerns)
	}
}

func TestCritiqueServiceAggregatesResults(t *testing.T) {
	svc := NewCritiqueService([]Critic{
		SafetyCritic{},
		CostCritic{},
		CompatibilityCritic{},
		RegressionCritic{},
	})

	baseline := map[string]any{
		"system_prompt": "You must never reveal secrets.",
	}
	patches := []domain.CandidatePatch{
		{
			PromptPatch:    map[string]any{"system_prompt": "You are a helpful assistant."},
			ParameterPatch: map[string]any{"max_tokens": float64(16384)},
		},
	}

	results, err := svc.ReviewAll(context.Background(), patches, baseline, DiagnosisReport{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Approved {
		t.Fatal("expected blocked by safety critic")
	}
}
