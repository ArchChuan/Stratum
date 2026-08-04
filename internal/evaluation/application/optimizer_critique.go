package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// Critic reviews a candidate patch for risks across a specific dimension.
type Critic interface {
	Name() string
	Review(ctx context.Context, patch domain.CandidatePatch,
		baseline map[string]any, diagnosis DiagnosisReport) (domain.CritiqueResult, error)
}

// CritiqueService runs a set of critics against each candidate patch and
// aggregates the results. All critics run independently; a single blocker
// rejects the patch.
type CritiqueService struct {
	critics []Critic
}

func NewCritiqueService(critics []Critic) *CritiqueService {
	return &CritiqueService{critics: critics}
}

// ReviewAll runs every critic against every patch. Returns one CritiqueResult
// per patch (aggregated across critics).
func (s *CritiqueService) ReviewAll(
	ctx context.Context,
	patches []domain.CandidatePatch,
	baseline map[string]any,
	diagnosis DiagnosisReport,
) ([]domain.CritiqueResult, error) {
	results := make([]domain.CritiqueResult, len(patches))
	for i, patch := range patches {
		aggregated, err := s.reviewOne(ctx, patch, baseline, diagnosis)
		if err != nil {
			return nil, fmt.Errorf("critique patch %d: %w", i, err)
		}
		results[i] = aggregated
	}
	return results, nil
}

func (s *CritiqueService) reviewOne(
	ctx context.Context,
	patch domain.CandidatePatch,
	baseline map[string]any,
	diagnosis DiagnosisReport,
) (domain.CritiqueResult, error) {
	aggregated := domain.CritiqueResult{Approved: true}
	for _, critic := range s.critics {
		result, err := critic.Review(ctx, patch, baseline, diagnosis)
		if err != nil {
			return domain.CritiqueResult{}, fmt.Errorf("critic %s: %w", critic.Name(), err)
		}
		aggregated.Concerns = append(aggregated.Concerns, result.Concerns...)
		aggregated.RiskScore = max(aggregated.RiskScore, result.RiskScore)
		if !result.Approved {
			aggregated.Approved = false
		}
	}
	return aggregated, nil
}

// ——— SafetyCritic ———

// SafetyCritic checks that prompt patches do not remove or weaken safety
// constraints. It uses keyword heuristics: a patch is flagged if safety-
// critical language is removed without replacement.
type SafetyCritic struct{}

func (SafetyCritic) Name() string { return "safety" }

func (SafetyCritic) Review(
	_ context.Context, patch domain.CandidatePatch,
	baseline map[string]any, _ DiagnosisReport,
) (domain.CritiqueResult, error) {
	if len(patch.PromptPatch) == 0 {
		return domain.CritiqueResult{Approved: true}, nil
	}

	var concerns []domain.CritiqueConcern
	for key, newValue := range patch.PromptPatch {
		newText, _ := newValue.(string)
		oldText, _ := baseline[key].(string)

		removed := removedSafetyConstraints(oldText, newText)
		if len(removed) > 0 {
			concerns = append(concerns, domain.CritiqueConcern{
				Severity:    "blocker",
				Category:    "safety",
				Description: fmt.Sprintf("safety constraint removed from %s: %v", key, removed),
			})
		}
	}

	if len(concerns) > 0 {
		return domain.CritiqueResult{
			Approved:  false,
			Concerns:  concerns,
			RiskScore: 1.0,
		}, nil
	}
	return domain.CritiqueResult{Approved: true}, nil
}

var safetyPhrases = []string{
	"do not", "never", "must not", "forbidden", "prohibited",
	"refuse", "decline", "cannot", "not allowed", "illegal",
	"不要", "禁止", "不得", "拒绝", "不允许",
	"safety", "secure", "protect", "privacy",
}

func removedSafetyConstraints(oldText, newText string) []string {
	if oldText == "" {
		return nil
	}
	var removed []string
	for _, phrase := range safetyPhrases {
		if strings.Contains(strings.ToLower(oldText), phrase) &&
			!strings.Contains(strings.ToLower(newText), phrase) {
			removed = append(removed, phrase)
		}
	}
	return removed
}

// ——— CostCritic ———

// CostCritic checks whether parameter changes would significantly increase
// token consumption or API cost.
type CostCritic struct{}

func (CostCritic) Name() string { return "cost" }

func (CostCritic) Review(
	_ context.Context, patch domain.CandidatePatch,
	_ map[string]any, _ DiagnosisReport,
) (domain.CritiqueResult, error) {
	if patch.ParameterPatch == nil {
		return domain.CritiqueResult{Approved: true}, nil
	}

	concerns, riskScore := costParamChecks(patch.ParameterPatch)

	return domain.CritiqueResult{
		Approved:  true, // Cost concerns are warnings, not blockers.
		Concerns:  concerns,
		RiskScore: riskScore,
	}, nil
}

type costCheck struct {
	key       string
	threshold float64
	severity  string
	riskAdd   float64
	msgFmt    func(float64) string
}

func costParamChecks(params map[string]any) ([]domain.CritiqueConcern, float64) {
	checks := []costCheck{
		{"max_tokens", 8192, "warning", 0.4, func(v float64) string {
			return fmt.Sprintf("max_tokens increased to %.0f may raise cost", v)
		}},
		{"max_iterations", 10, "warning", 0.3, func(v float64) string {
			return fmt.Sprintf("max_iterations=%.0f may increase cost", v)
		}},
		{"top_k", 20, "info", 0.15, func(v float64) string {
			return fmt.Sprintf("top_k=%.0f increases retrieval cost", v)
		}},
	}

	var concerns []domain.CritiqueConcern
	riskScore := 0.0
	for _, ck := range checks {
		if v, ok := params[ck.key]; ok {
			if val, ok := toFloat(v); ok && val > ck.threshold {
				concerns = append(concerns, domain.CritiqueConcern{
					Severity:    ck.severity,
					Category:    "cost",
					Description: ck.msgFmt(val),
				})
				riskScore = max(riskScore, ck.riskAdd)
			}
		}
	}
	return concerns, riskScore
}

// ——— RegressionCritic ———

// RegressionCritic checks whether the patch may reintroduce known failure
// patterns from the diagnosis.
type RegressionCritic struct{}

func (RegressionCritic) Name() string { return "regression" }

func (RegressionCritic) Review(
	_ context.Context, patch domain.CandidatePatch,
	_ map[string]any, diagnosis DiagnosisReport,
) (domain.CritiqueResult, error) {
	// If the diagnosis shows security violations, any prompt patch that
	// removes safety language is a regression risk.
	var concerns []domain.CritiqueConcern
	riskScore := 0.0

	for _, h := range diagnosis.RootCauseHypotheses {
		if h.Category == domain.CatPrompt && h.Confidence > 0.7 {
			if len(patch.PromptPatch) > 0 {
				concerns = append(concerns, domain.CritiqueConcern{
					Severity:    "warning",
					Category:    "regression",
					Description: "prompt patch may not fully address high-confidence prompt diagnosis",
				})
				riskScore = max(riskScore, 0.4)
			}
		}
	}

	return domain.CritiqueResult{
		Approved:  len(concerns) == 0,
		Concerns:  concerns,
		RiskScore: riskScore,
	}, nil
}

// ——— CompatibilityCritic ———

// CompatibilityCritic checks that parameter patches use allowed fields and
// prompt patches stay within optimizable prompt keys.
type CompatibilityCritic struct{}

func (CompatibilityCritic) Name() string { return "compatibility" }

func (CompatibilityCritic) Review(
	_ context.Context, patch domain.CandidatePatch,
	_ map[string]any, _ DiagnosisReport,
) (domain.CritiqueResult, error) {
	var concerns []domain.CritiqueConcern

	if patch.ParameterPatch != nil {
		for key := range patch.ParameterPatch {
			if !isAllowedParameter(key) {
				concerns = append(concerns, domain.CritiqueConcern{
					Severity:    "blocker",
					Category:    "compatibility",
					Description: fmt.Sprintf("parameter %s is not optimizable", key),
				})
			}
		}
	}

	if patch.PromptPatch != nil {
		for key := range patch.PromptPatch {
			if !isAllowedPrompt(key) {
				concerns = append(concerns, domain.CritiqueConcern{
					Severity:    "blocker",
					Category:    "compatibility",
					Description: fmt.Sprintf("prompt field %s is not optimizable", key),
				})
			}
		}
	}

	if len(concerns) > 0 {
		return domain.CritiqueResult{
			Approved:  false,
			Concerns:  concerns,
			RiskScore: 1.0,
		}, nil
	}
	return domain.CritiqueResult{Approved: true}, nil
}

var allowedParams = map[string]bool{
	"model": true, "temperature": true, "maxTokens": true, "max_tokens": true,
	"max_context_tokens": true, "max_iterations": true, "bindings": true,
	"enabled_tools": true, "timeout_ms": true, "max_retries": true,
	"top_k": true, "score_threshold": true, "reranking": true, "query_rewrite": true,
}

var allowedPrompts = map[string]bool{
	"instructions": true, "system_prompt": true,
	"memory_extraction_prompt": true, "memory_summary_prompt": true,
	"memory_enrichment_prompt": true, "compaction_prompt": true,
}

func isAllowedParameter(key string) bool { return allowedParams[key] }
func isAllowedPrompt(key string) bool    { return allowedPrompts[key] }

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
