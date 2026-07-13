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
