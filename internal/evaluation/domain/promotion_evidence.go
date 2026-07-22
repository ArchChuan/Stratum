package domain

type GateStatus string

const (
	GatePassed        GateStatus = "passed"
	GateFailed        GateStatus = "failed"
	GatePending       GateStatus = "pending"
	GateNotApplicable GateStatus = "not_applicable"
)

type PromotionBlockerCode string

const (
	BlockerSamples             PromotionBlockerCode = "insufficient_samples"
	BlockerDuration            PromotionBlockerCode = "insufficient_duration"
	BlockerEvidenceUnavailable PromotionBlockerCode = "evidence_unavailable"
	BlockerGuardrail           PromotionBlockerCode = "guardrail_violation"
	BlockerSafetyStop          PromotionBlockerCode = "safety_stop"
	BlockerRecommendation      PromotionBlockerCode = "recommendation_hold"
)

type PromotionGates struct {
	Quality   GateStatus `json:"quality"`
	Cost      GateStatus `json:"cost"`
	Latency   GateStatus `json:"latency"`
	ErrorRate GateStatus `json:"error_rate"`
	Security  GateStatus `json:"security"`
}

type PromotionBlocker struct {
	Code     PromotionBlockerCode `json:"code"`
	Category string               `json:"category"`
	Message  string               `json:"message"`
}

type PromotionEvidence struct {
	Eligible bool               `json:"eligible"`
	Gates    PromotionGates     `json:"gates"`
	Blockers []PromotionBlocker `json:"blockers"`
}

func BuildPromotionEvidence(
	policy PromotionPolicy,
	metrics *StageMetrics,
	recommendation Decision,
	safetyStopped bool,
) PromotionEvidence {
	if len(policy.Stages) == 0 {
		policy = DefaultPromotionPolicy()
	}
	if safetyStopped {
		return blockedEvidence(PromotionGates{
			Quality: GateNotApplicable, Cost: GateNotApplicable, Latency: GateNotApplicable,
			ErrorRate: GateNotApplicable, Security: GateFailed,
		}, BlockerSafetyStop, "safety", "安全停止已触发")
	}
	if metrics == nil {
		return blockedEvidence(PromotionGates{
			Quality: GateNotApplicable, Cost: GateNotApplicable, Latency: GateNotApplicable,
			ErrorRate: GateNotApplicable, Security: GateNotApplicable,
		}, BlockerEvidenceUnavailable, "evidence", "证据依赖暂不可用")
	}
	gates := PromotionGates{Quality: GatePassed, Cost: GatePassed, Latency: GatePassed,
		ErrorRate: GatePassed, Security: GatePassed}
	if metrics.Samples < policy.MinSamples {
		gates.Quality = GatePending
		return blockedEvidence(gates, BlockerSamples, "sample", "样本量不足")
	}
	if metrics.ObservedMinutes < policy.MinObservationMinutes {
		gates.Latency = GatePending
		return blockedEvidence(gates, BlockerDuration, "duration", "观测时长不足")
	}
	if metrics.SecurityViolation {
		gates.Security = GateFailed
		return blockedEvidence(gates, BlockerGuardrail, "security", "安全门禁违反")
	}
	if metrics.CostRegression > policy.MaxCostRegression {
		gates.Cost = GateFailed
		return blockedEvidence(gates, BlockerGuardrail, "cost", "成本门禁违反")
	}
	if metrics.P95LatencyRegression > policy.MaxLatencyRegression {
		gates.Latency = GateFailed
		return blockedEvidence(gates, BlockerGuardrail, "latency", "时延门禁违反")
	}
	if metrics.ErrorRateIncrease > policy.MaxErrorRateIncrease {
		gates.ErrorRate = GateFailed
		return blockedEvidence(gates, BlockerGuardrail, "error_rate", "错误率门禁违反")
	}
	if recommendation != DecisionPromote {
		if !metrics.QualitySignificant || metrics.QualityImprovement <= 0 {
			gates.Quality = GatePending
		}
		return blockedEvidence(gates, BlockerRecommendation, "recommendation", "系统建议继续观察")
	}
	return PromotionEvidence{Eligible: true, Gates: gates, Blockers: []PromotionBlocker{}}
}

func blockedEvidence(gates PromotionGates, code PromotionBlockerCode, category, message string) PromotionEvidence {
	return PromotionEvidence{Gates: gates, Blockers: []PromotionBlocker{{Code: code, Category: category, Message: message}}}
}
