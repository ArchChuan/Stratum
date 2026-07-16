package domain

import "errors"

// ErrConcurrencyLimit is returned when a tenant or global execution cap is reached.
var ErrConcurrencyLimit = errors.New("concurrency limit reached")

// Repository / service-level sentinels.
var (
	ErrSkillNotFound        = errors.New("skill not found")
	ErrSkillNameConflict    = errors.New("skill name already exists")
	ErrSkillLinked          = errors.New("skill still linked to agents")
	ErrSkillTypeImmutable   = errors.New("cannot change skill type")
	ErrNotCodeSkill         = errors.New("skill is not a code skill")
	ErrSkillUnsupportedType = errors.New("unsupported skill type")
	ErrSkillCodeAnalysis    = errors.New("code analysis failed")
	// ErrSkillNotPublishable is returned when a draft fails publish validation.
	// Callers wrap the detail: fmt.Errorf("reason: %w", ErrSkillNotPublishable).
	ErrSkillNotPublishable = errors.New("skill not publishable")
)

// AnalysisError carries analyzer reasons for a static-analysis rejection.
type AnalysisError struct {
	Reasons []string
}

func (e *AnalysisError) Error() string { return ErrSkillCodeAnalysis.Error() }

func (e *AnalysisError) Unwrap() error { return ErrSkillCodeAnalysis }
