package domain

// ChangeImpact classifies how disruptive a tunable change is.
type ChangeImpact string

const (
	ImpactNone     ChangeImpact = "none"
	ImpactMinor    ChangeImpact = "minor"
	ImpactModerate ChangeImpact = "moderate"
	ImpactMajor    ChangeImpact = "major"
)

// EvolutionDiff is a structured representation of the differences between a
// baseline resource revision and an optimized candidate. It is designed to be
// directly renderable by a frontend for visual diff review.
type EvolutionDiff struct {
	BaselineRef  ResourceRef     `json:"baseline"`
	CandidateRef ResourceRef     `json:"candidate"`
	Changes      []TunableChange `json:"changes"`
	RiskScore    float64         `json:"riskScore"`
}

// TunableChange describes a single parameter change between baseline and
// candidate. The VisualHint tells the frontend which control to render.
type TunableChange struct {
	TunableKey  string          `json:"key"`
	Category    TunableCategory `json:"category"`
	DisplayName string          `json:"displayName"`
	OldValue    any             `json:"oldValue"`
	NewValue    any             `json:"newValue"`
	Impact      ChangeImpact    `json:"impact"`
	Rationale   string          `json:"rationale,omitempty"`
	VisualHint  VisualHint      `json:"visualHint"`
}

// RiskScore computes an aggregate risk score (0-1) from a set of tunable
// changes. Major changes contribute 0.15 each, moderate 0.05, minor 0.01.
// The score is capped at 1.0.
func RiskScore(changes []TunableChange) float64 {
	var score float64
	for _, c := range changes {
		switch c.Impact {
		case ImpactMajor:
			score += 0.15
		case ImpactModerate:
			score += 0.05
		case ImpactMinor:
			score += 0.01
		}
	}
	if score > 1.0 {
		return 1.0
	}
	return score
}

// NewEvolutionDiff builds an EvolutionDiff from a set of tunable changes,
// computing the aggregate risk score.
func NewEvolutionDiff(baseline, candidate ResourceRef, changes []TunableChange) EvolutionDiff {
	return EvolutionDiff{
		BaselineRef:  baseline,
		CandidateRef: candidate,
		Changes:      changes,
		RiskScore:    RiskScore(changes),
	}
}
