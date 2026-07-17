package port

import (
	"context"
)

// ExtractedFact represents a fact extracted from conversation.
type ExtractedFact struct {
	Content    string   `json:"content"`
	Importance float64  `json:"importance"`
	Entities   []string `json:"entities"`
	// FactType classifies the fact: preference|skill|event|state|relationship|other
	FactType string `json:"fact_type"`
	// Confidence is the LLM-reported confidence in [0,1].
	// Pointer to distinguish omitted (nil → default to Importance) from explicit 0.0
	// (filtered by low-confidence gate in Phase 0).
	Confidence *float64 `json:"confidence,omitempty"`
}

// LLMExtractor extracts structured facts from conversation messages.
type LLMExtractor interface {
	ExtractFacts(ctx context.Context, userID, agentID, message string) ([]*ExtractedFact, error)
}

// SupersedeJudgment contains LLM's decision on whether new fact supersedes old.
type SupersedeJudgment struct {
	Supersedes bool
	Reason     string
}

// LLMSuperseder judges whether new fact supersedes old fact.
type LLMSuperseder interface {
	JudgeSupersede(ctx context.Context, oldFact, newFact string) (*SupersedeJudgment, error)
}

// EntityProfiler generates rolling summaries for entities.
type EntityProfiler interface {
	GenerateProfile(ctx context.Context, entityName, entityType string, facts []string) (string, error)
}
