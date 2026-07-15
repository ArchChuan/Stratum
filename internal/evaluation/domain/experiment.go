package domain

import "hash/fnv"

type ExperimentStatus string

const (
	ExperimentRunning    ExperimentStatus = "running"
	ExperimentCompleted  ExperimentStatus = "completed"
	ExperimentRolledBack ExperimentStatus = "rolled_back"
)

type Decision string

const (
	DecisionHold     Decision = "hold"
	DecisionPromote  Decision = "promote"
	DecisionRollback Decision = "rollback"
)

type PromotionPolicy struct {
	Stages                []int
	MinSamples            int
	MinObservationMinutes int
	MaxCostRegression     float64
	MaxLatencyRegression  float64
	MaxErrorRateIncrease  float64
}

func DefaultPromotionPolicy() PromotionPolicy {
	return PromotionPolicy{
		Stages:                []int{5, 20, 50, 100},
		MinSamples:            100,
		MinObservationMinutes: 60,
		MaxCostRegression:     0.15,
		MaxLatencyRegression:  0.20,
		MaxErrorRateIncrease:  0.01,
	}
}

type StageMetrics struct {
	Samples              int
	ObservedMinutes      int
	QualityImprovement   float64
	QualitySignificant   bool
	CostRegression       float64
	P95LatencyRegression float64
	ErrorRateIncrease    float64
	SecurityViolation    bool
}

type Experiment struct {
	ID               string
	ResourceKind     ResourceKind
	ResourceID       string
	StableRevisionID string
	CanaryRevisionID string
	SuiteRevisionID  string
	Status           ExperimentStatus
	Stage            int
	Policy           PromotionPolicy
}

type Deployment struct {
	ResourceKind     ResourceKind `json:"resource_kind"`
	ResourceID       string       `json:"resource_id"`
	StableRevisionID string       `json:"stable_revision_id"`
	CanaryRevisionID string       `json:"canary_revision_id,omitempty"`
	CanaryPercent    int          `json:"canary_percent"`
	ExperimentID     string       `json:"experiment_id,omitempty"`
	PolicyVersion    int          `json:"policy_version"`
}

func AssignVariant(key string, canaryPercent int) bool {
	if canaryPercent <= 0 {
		return false
	}
	if canaryPercent >= 100 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()%100) < canaryPercent
}

func (e Experiment) Decide(metrics StageMetrics, policy PromotionPolicy) (Experiment, Decision) {
	if e.Status != ExperimentRunning {
		return e, DecisionHold
	}
	if metrics.SecurityViolation ||
		metrics.CostRegression > policy.MaxCostRegression ||
		metrics.P95LatencyRegression > policy.MaxLatencyRegression ||
		metrics.ErrorRateIncrease > policy.MaxErrorRateIncrease {
		e.Status = ExperimentRolledBack
		return e, DecisionRollback
	}
	if metrics.Samples < policy.MinSamples || metrics.ObservedMinutes < policy.MinObservationMinutes ||
		!metrics.QualitySignificant || metrics.QualityImprovement <= 0 {
		return e, DecisionHold
	}
	for i, stage := range policy.Stages {
		if stage != e.Stage || i+1 >= len(policy.Stages) {
			continue
		}
		e.Stage = policy.Stages[i+1]
		if e.Stage == 100 {
			e.Status = ExperimentCompleted
		}
		return e, DecisionPromote
	}
	return e, DecisionHold
}
