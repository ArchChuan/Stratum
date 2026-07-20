package port

import "context"

type ExtractFactsRequest struct {
	TenantID        string
	UserID          string
	AgentID         string
	ConversationID  string
	Scope           string
	SourceMessageID string
	SourceTaskID    int64
	Messages        []MessageDTO
}

type MessageDTO struct {
	Role    string
	Content string
}

type FactExtractionService interface {
	ExtractFacts(ctx context.Context, req *ExtractFactsRequest) error
}
