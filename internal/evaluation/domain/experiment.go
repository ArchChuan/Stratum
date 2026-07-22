package domain

import "hash/fnv"

type ExperimentStatus string

const (
	ExperimentRunning    ExperimentStatus = "running"
	ExperimentPaused     ExperimentStatus = "paused"
	ExperimentCompleted  ExperimentStatus = "completed"
	ExperimentRolledBack ExperimentStatus = "rolled_back"
)

type Decision string

const (
	DecisionHold     Decision = "hold"
	DecisionAdvance  Decision = "advance"
	DecisionPromote  Decision = "promote"
	DecisionRollback Decision = "rollback"
)

type PromotionPolicy struct {
	Stages                []int   `json:"stages"`
	MinSamples            int     `json:"min_samples"`
	MinObservationMinutes int     `json:"min_observation_minutes"`
	MaxCostRegression     float64 `json:"max_cost_regression"`
	MaxLatencyRegression  float64 `json:"max_latency_regression"`
	MaxErrorRateIncrease  float64 `json:"max_error_rate_increase"`
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
	Samples              int     `json:"samples"`
	ObservedMinutes      int     `json:"observed_minutes"`
	QualityImprovement   float64 `json:"quality_improvement"`
	QualitySignificant   bool    `json:"quality_significant"`
	CostRegression       float64 `json:"cost_regression"`
	P95LatencyRegression float64 `json:"p95_latency_regression"`
	ErrorRateIncrease    float64 `json:"error_rate_increase"`
	SecurityViolation    bool    `json:"security_violation"`
}

type Experiment struct {
	ID               string           `json:"id"`
	ResourceKind     ResourceKind     `json:"resource_kind"`
	ResourceID       string           `json:"resource_id"`
	StableRevisionID string           `json:"stable_revision_id"`
	CanaryRevisionID string           `json:"canary_revision_id"`
	SuiteRevisionID  string           `json:"suite_revision_id"`
	Status           ExperimentStatus `json:"status"`
	Stage            int              `json:"stage"`
	Policy           PromotionPolicy  `json:"policy"`
	StateVersion     int64            `json:"state_version"`
	Recommendation   Decision         `json:"recommendation"`
	SafetyStopped    bool             `json:"safety_stopped"`
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

type RevisionAssignment struct {
	RevisionID   string `json:"revision_id"`
	ExperimentID string `json:"experiment_id,omitempty"`
	Variant      string `json:"variant"`
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
	if e.SafetyStopped {
		e.Stage = 0
		e.Recommendation = DecisionRollback
		return e, DecisionRollback
	}
	e.Recommendation = DecisionHold
	if metrics.SecurityViolation ||
		metrics.CostRegression > policy.MaxCostRegression ||
		metrics.P95LatencyRegression > policy.MaxLatencyRegression ||
		metrics.ErrorRateIncrease > policy.MaxErrorRateIncrease {
		e.Stage = 0
		e.Recommendation = DecisionRollback
		e.SafetyStopped = true
		return e, DecisionRollback
	}
	if metrics.Samples < policy.MinSamples || metrics.ObservedMinutes < policy.MinObservationMinutes ||
		!metrics.QualitySignificant || metrics.QualityImprovement <= 0 {
		return e, DecisionHold
	}
	for i, stage := range policy.Stages {
		if stage != e.Stage {
			continue
		}
		if i+1 >= len(policy.Stages) {
			e.Recommendation = DecisionPromote
			return e, DecisionPromote
		}
		e.Stage = policy.Stages[i+1]
		e.Recommendation = DecisionAdvance
		return e, DecisionAdvance
	}
	return e, DecisionHold
}

func CanApplyExperimentCommand(status ExperimentStatus, action ExperimentCommandAction) bool {
	switch status {
	case ExperimentRunning:
		return action == CommandPause || action == CommandPromote || action == CommandRollback
	case ExperimentPaused:
		return action == CommandRollback
	default:
		return false
	}
}
