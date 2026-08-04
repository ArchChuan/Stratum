package domain

import "context"

// FailureSummary is a concise description of an evaluation failure used as
// input to prompt rewriters and other LLM-driven optimizers.
type FailureSummary struct {
	CaseName    string `json:"case_name"`
	Actual      string `json:"actual"`
	Expected    string `json:"expected"`
	Description string `json:"description"`
}

// OptimizationStrategy is a pluggable optimizer that generates candidate
// patches from a baseline snapshot and evaluation failures. Each strategy
// declares which tunable categories it can optimize.
//
// Built-in strategies:
//   - LLMPromptRewriter — rewrites CatPrompt tunables via LLM
//   - GridSearchOptimizer — grid search over discrete parameter spaces
//   - RandomSearchOptimizer — random sampling for large search spaces
type OptimizationStrategy interface {
	Name() string
	Categories() []TunableCategory
	Generate(ctx context.Context, baseline map[string]any,
		tunables []Tunable, failures []FailureSummary) ([]CandidatePatch, error)
}

// ——— Optimizer Pipeline domain types ———

// DiagnosisReport is the output of the DIAGNOSE stage, classifying evaluation
// failures by tunable category and proposing root cause hypotheses.
type DiagnosisReport struct {
	TotalFailures       int                     `json:"total_failures"`
	CategoryBreakdown   map[TunableCategory]int `json:"category_breakdown"`
	RootCauseHypotheses []RootCauseHypothesis   `json:"root_cause_hypotheses"`
	AffectedTunables    []string                `json:"affected_tunables"`
}

// RootCauseHypothesis links a set of failures to a probable tunable category
// with confidence and supporting evidence.
type RootCauseHypothesis struct {
	Category    TunableCategory `json:"category"`
	Description string          `json:"description"`
	Confidence  float64         `json:"confidence"`
	Evidence    []string        `json:"evidence"`
	Source      string          `json:"source"` // "rule" | "llm"
}

// CritiqueResult is the output of a single Critic review.
type CritiqueResult struct {
	Approved  bool              `json:"approved"`
	Concerns  []CritiqueConcern `json:"concerns"`
	RiskScore float64           `json:"risk_score"`
}

// CritiqueConcern describes a specific issue found during adversarial review.
type CritiqueConcern struct {
	Severity    string `json:"severity"` // "blocker" | "warning" | "info"
	Category    string `json:"category"` // "safety" | "cost" | "latency" | "compatibility" | "regression"
	Description string `json:"description"`
}

// GateDecision is the output of the Gate stage.
type GateDecision string

const (
	GateAdvance GateDecision = "advance"
	GateHold    GateDecision = "hold"
	GateReject  GateDecision = "reject"
)

// PipelineResult summarizes a full optimizer pipeline run.
type PipelineResult struct {
	Diagnosis  DiagnosisReport  `json:"diagnosis"`
	Candidates []CandidatePatch `json:"candidates"`
	Critiques  []CritiqueResult `json:"critiques"`
	Decisions  []GateDecision   `json:"decisions"`
}
