package port

import (
	"context"
)

// ExtractedFact represents a fact extracted from conversation.
type ExtractedFact struct {
	Content    string
	Importance float64
	Entities   []string // entity names mentioned in this fact
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
