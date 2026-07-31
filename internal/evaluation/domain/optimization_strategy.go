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
