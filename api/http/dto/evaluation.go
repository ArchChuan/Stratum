package dto

type EvaluationResourceRef struct {
	Kind       string `json:"kind" binding:"required,oneof=skill"`
	ResourceID string `json:"resource_id" binding:"required"`
	RevisionID string `json:"revision_id" binding:"required"`
}

type EvaluationCaseRequest struct {
	Name           string `json:"name"`
	Input          any    `json:"input" binding:"required"`
	ExpectedOutput any    `json:"expected_output" binding:"required"`
	AssertionMode  string `json:"assertion_mode" binding:"required,oneof=exact contains regex"`
	Enabled        *bool  `json:"enabled"`
}

type CreateEvaluationSuiteRequest struct {
	Name         string                  `json:"name" binding:"required,max=255"`
	Description  string                  `json:"description" binding:"max=2048"`
	ResourceKind string                  `json:"resource_kind" binding:"required,oneof=skill"`
	Cases        []EvaluationCaseRequest `json:"cases" binding:"required,min=1,dive"`
}

type EnqueueEvaluationRunRequest struct {
	Resource        EvaluationResourceRef `json:"resource" binding:"required"`
	SuiteRevisionID string                `json:"suite_revision_id" binding:"required"`
	IdempotencyKey  string                `json:"idempotency_key" binding:"required,max=255"`
}

type EvaluationJobResponse struct {
	JobID        string `json:"job_id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	ResultID     string `json:"result_id,omitempty"`
}

type GenerateOptimizationRequest struct {
	Baseline         EvaluationResourceRef `json:"baseline" binding:"required"`
	SuiteRevisionID  string                `json:"suite_revision_id" binding:"required"`
	SearchSpace      map[string][]any      `json:"search_space" binding:"required,min=1"`
	FailureSummaries []string              `json:"failure_summaries" binding:"max=50,dive,max=2048"`
}

type CreateEvaluationExperimentRequest struct {
	Stable          EvaluationResourceRef `json:"stable" binding:"required"`
	Canary          EvaluationResourceRef `json:"canary" binding:"required"`
	SuiteRevisionID string                `json:"suite_revision_id" binding:"required"`
}

type EvaluateExperimentStageRequest struct {
	Samples              int     `json:"samples" binding:"min=0"`
	ObservedMinutes      int     `json:"observed_minutes" binding:"min=0"`
	QualityImprovement   float64 `json:"quality_improvement"`
	QualitySignificant   bool    `json:"quality_significant"`
	CostRegression       float64 `json:"cost_regression"`
	P95LatencyRegression float64 `json:"p95_latency_regression"`
	ErrorRateIncrease    float64 `json:"error_rate_increase"`
	SecurityViolation    bool    `json:"security_violation"`
}
