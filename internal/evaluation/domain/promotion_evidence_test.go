package domain

import "testing"

func TestBuildPromotionEvidenceUsesPolicyMetricsAndRecommendation(t *testing.T) {
	policy := DefaultPromotionPolicy()
	tests := []struct {
		name       string
		metrics    *StageMetrics
		decision   Decision
		safetyStop bool
		want       PromotionBlockerCode
	}{
		{name: "evidence unavailable", want: BlockerEvidenceUnavailable},
		{name: "samples", metrics: &StageMetrics{Samples: 20}, want: BlockerSamples},
		{name: "duration", metrics: &StageMetrics{Samples: 200, ObservedMinutes: 10}, want: BlockerDuration},
		{name: "guardrail", metrics: &StageMetrics{Samples: 200, ObservedMinutes: 90, QualitySignificant: true,
			QualityImprovement: .2, CostRegression: policy.MaxCostRegression + .1}, want: BlockerGuardrail},
		{name: "safety", metrics: &StageMetrics{Samples: 200, ObservedMinutes: 90}, safetyStop: true,
			want: BlockerSafetyStop},
		{name: "hold", metrics: &StageMetrics{Samples: 200, ObservedMinutes: 90, QualitySignificant: true,
			QualityImprovement: .2}, decision: DecisionHold, want: BlockerRecommendation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := BuildPromotionEvidence(policy, tt.metrics, tt.decision, tt.safetyStop)
			if len(evidence.Blockers) == 0 || evidence.Blockers[0].Code != tt.want {
				t.Fatalf("evidence=%+v want blocker=%s", evidence, tt.want)
			}
		})
	}
	eligible := BuildPromotionEvidence(policy, &StageMetrics{Samples: 200, ObservedMinutes: 90,
		QualitySignificant: true, QualityImprovement: .2}, DecisionPromote, false)
	if !eligible.Eligible || len(eligible.Blockers) != 0 || eligible.Gates.Quality != GatePassed {
		t.Fatalf("eligible evidence=%+v", eligible)
	}
}
