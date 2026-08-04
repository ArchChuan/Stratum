package domain

import "testing"

func change(impact ChangeImpact) TunableChange {
	return TunableChange{TunableKey: "k", Category: "strategy", DisplayName: "K", Impact: impact}
}

func TestRiskScoreAccumulatesByImpact(t *testing.T) {
	cases := []struct {
		name    string
		changes []TunableChange
		want    float64
	}{
		{"empty changes", nil, 0.0},
		{"single major", []TunableChange{change(ImpactMajor)}, 0.15},
		{"single moderate", []TunableChange{change(ImpactModerate)}, 0.05},
		{"single minor", []TunableChange{change(ImpactMinor)}, 0.01},
		{"none contributes nothing", []TunableChange{change(ImpactNone), change(ImpactNone)}, 0.0},
		{"mixed accumulation", []TunableChange{change(ImpactMajor), change(ImpactModerate), change(ImpactMinor)}, 0.21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 浮点累加用 epsilon 比较（0.15+0.05+0.01 = 0.21000000000000002）。
			if got := RiskScore(tc.changes); got-tc.want > 1e-9 || tc.want-got > 1e-9 {
				t.Errorf("RiskScore = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRiskScoreCapsAtOne(t *testing.T) {
	// 极端情况：7 个 major（1.05）必须封顶为 1.0。
	var changes []TunableChange
	for i := 0; i < 7; i++ {
		changes = append(changes, change(ImpactMajor))
	}
	if got := RiskScore(changes); got != 1.0 {
		t.Errorf("expected capped 1.0, got %v", got)
	}
}

func TestRiskScoreExactlyAtCap(t *testing.T) {
	// 极端情况：恰好累计 1.0 不封顶也不放大。
	var changes []TunableChange
	for i := 0; i < 20; i++ {
		changes = append(changes, change(ImpactModerate))
	}
	if got := RiskScore(changes); got != 1.0 {
		t.Errorf("expected 1.0, got %v", got)
	}
}

func TestNewEvolutionDiffComputesRisk(t *testing.T) {
	baseline := ResourceRef{Kind: ResourceKindAgent, ResourceID: "r1", RevisionID: "rev-base"}
	candidate := ResourceRef{Kind: ResourceKindAgent, ResourceID: "r1", RevisionID: "rev-cand"}
	changes := []TunableChange{change(ImpactMajor), change(ImpactModerate)}

	diff := NewEvolutionDiff(baseline, candidate, changes)
	if diff.BaselineRef != baseline || diff.CandidateRef != candidate {
		t.Errorf("refs not preserved: %+v", diff)
	}
	if len(diff.Changes) != 2 {
		t.Errorf("expected 2 changes, got %d", len(diff.Changes))
	}
	if got := diff.RiskScore; got-0.20 > 1e-9 || 0.20-got > 1e-9 {
		t.Errorf("expected risk 0.20, got %v", diff.RiskScore)
	}
}

func TestNewEvolutionDiffEmptyChanges(t *testing.T) {
	// 极端情况：无变更的 diff 风险分必须为 0。
	diff := NewEvolutionDiff(ResourceRef{}, ResourceRef{}, nil)
	if diff.RiskScore != 0.0 {
		t.Errorf("expected 0 risk, got %v", diff.RiskScore)
	}
}
