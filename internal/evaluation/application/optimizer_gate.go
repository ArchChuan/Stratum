package application

import (
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// Default gate thresholds aligned with Experiment PromotionPolicy.
const (
	defaultMinQualityImprovement = 0.0  // must be > 0 (statistically significant)
	defaultMaxCostRegression     = 0.15 // 15%
	defaultMaxLatencyRegression  = 0.20 // 20%
	defaultMaxErrorRateIncrease  = 0.01 // 1%
)

// OptimizerGate applies quality thresholds to decide whether a candidate
// patch should advance to the experiment queue, be held for more data,
// or be rejected outright.
type OptimizerGate struct {
	MinQualityImprovement float64
	MaxCostRegression     float64
	MaxLatencyRegression  float64
	MaxErrorRateIncrease  float64
}

func DefaultGate() OptimizerGate {
	return OptimizerGate{
		MinQualityImprovement: defaultMinQualityImprovement,
		MaxCostRegression:     defaultMaxCostRegression,
		MaxLatencyRegression:  defaultMaxLatencyRegression,
		MaxErrorRateIncrease:  defaultMaxErrorRateIncrease,
	}
}

// Decide evaluates a candidate patch against its critique result and any
// available evaluation data. Returns the gate decision.
func (g OptimizerGate) Decide(
	candidate domain.CandidatePatch,
	critique domain.CritiqueResult,
	evalMetrics *EvalMetrics,
) domain.GateDecision {
	if decision := g.checkBlockers(evalMetrics, critique); decision != domain.GateAdvance {
		return decision
	}
	if decision := g.evalThresholds(evalMetrics); decision != domain.GateAdvance {
		return decision
	}
	if evalMetrics == nil && critique.RiskScore > 0.7 {
		return domain.GateHold
	}
	return domain.GateAdvance
}

// checkBlockers returns GateReject for security violations or blocker concerns.
func (g OptimizerGate) checkBlockers(
	evalMetrics *EvalMetrics, critique domain.CritiqueResult,
) domain.GateDecision {
	if evalMetrics != nil && evalMetrics.SecurityViolation {
		return domain.GateReject
	}
	for _, concern := range critique.Concerns {
		if concern.Severity == "blocker" {
			return domain.GateReject
		}
	}
	return domain.GateAdvance
}

// evalThresholds checks quality, cost, latency, and error rate against gate limits.
func (g OptimizerGate) evalThresholds(evalMetrics *EvalMetrics) domain.GateDecision {
	if evalMetrics == nil {
		return domain.GateAdvance
	}
	if evalMetrics.QualityImprovement <= g.MinQualityImprovement {
		return domain.GateHold
	}
	if evalMetrics.CostRegression > g.MaxCostRegression {
		return domain.GateHold
	}
	if evalMetrics.LatencyRegression > g.MaxLatencyRegression {
		return domain.GateHold
	}
	if evalMetrics.ErrorRateIncrease > g.MaxErrorRateIncrease {
		return domain.GateReject
	}
	return domain.GateAdvance
}

// EvalMetrics carries aggregated evaluation metrics for gate decision.
type EvalMetrics struct {
	QualityImprovement float64
	CostRegression     float64
	LatencyRegression  float64
	ErrorRateIncrease  float64
	SecurityViolation  bool
}
