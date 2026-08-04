package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestGateRejectsOnSecurityViolation(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true},
		&EvalMetrics{SecurityViolation: true},
	)
	if decision != domain.GateReject {
		t.Fatalf("expected reject on security violation, got %s", decision)
	}
}

func TestGateRejectsOnBlockerConcern(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{
			Approved: false,
			Concerns: []domain.CritiqueConcern{
				{Severity: "blocker", Category: "safety", Description: "unsafe"},
			},
		},
		nil,
	)
	if decision != domain.GateReject {
		t.Fatalf("expected reject on blocker, got %s", decision)
	}
}

func TestGateHoldsOnLowQualityImprovement(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true},
		&EvalMetrics{QualityImprovement: 0.0},
	)
	if decision != domain.GateHold {
		t.Fatalf("expected hold on zero improvement, got %s", decision)
	}
}

func TestGateHoldsOnHighCostRegression(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true},
		&EvalMetrics{
			QualityImprovement: 0.1,
			CostRegression:     0.20, // > 15%
		},
	)
	if decision != domain.GateHold {
		t.Fatalf("expected hold on cost regression, got %s", decision)
	}
}

func TestGateHoldsOnHighLatencyRegression(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true},
		&EvalMetrics{
			QualityImprovement: 0.1,
			LatencyRegression:  0.25, // > 20%
		},
	)
	if decision != domain.GateHold {
		t.Fatalf("expected hold on latency regression, got %s", decision)
	}
}

func TestGateRejectsOnHighErrorRate(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true},
		&EvalMetrics{
			QualityImprovement: 0.1,
			ErrorRateIncrease:  0.02, // > 1%
		},
	)
	if decision != domain.GateReject {
		t.Fatalf("expected reject on error rate increase, got %s", decision)
	}
}

func TestGateHoldsOnHighRiskWithoutMetrics(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true, RiskScore: 0.8},
		nil,
	)
	if decision != domain.GateHold {
		t.Fatalf("expected hold on high risk without metrics, got %s", decision)
	}
}

func TestGateAdvancesGoodCandidate(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true, RiskScore: 0.1},
		&EvalMetrics{
			QualityImprovement: 0.15,
			CostRegression:     0.05,
			LatencyRegression:  0.02,
		},
	)
	if decision != domain.GateAdvance {
		t.Fatalf("expected advance on good candidate, got %s", decision)
	}
}

func TestGateAdvancesWithLowRiskNoMetrics(t *testing.T) {
	gate := DefaultGate()
	decision := gate.Decide(
		domain.CandidatePatch{},
		domain.CritiqueResult{Approved: true, RiskScore: 0.3},
		nil,
	)
	if decision != domain.GateAdvance {
		t.Fatalf("expected advance on low risk without metrics, got %s", decision)
	}
}
