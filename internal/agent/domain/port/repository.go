package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type AgentRepo interface {
	Get(ctx context.Context, id string) (*domain.Agent, error)
	List(ctx context.Context, f domain.ListFilter) ([]*domain.Agent, error)
	Save(ctx context.Context, a *domain.Agent) error
	Delete(ctx context.Context, id string) error
}

type ExecutionRepo interface {
	Get(ctx context.Context, id string) (*domain.Execution, error)
	Save(ctx context.Context, e *domain.Execution) error
	List(ctx context.Context, agentID string, f domain.ListFilter) ([]*domain.Execution, error)
}

type ChatRepo interface {
	GetConversation(ctx context.Context, id string) (*domain.Conversation, error)
	SaveConversation(ctx context.Context, c *domain.Conversation) error
	AppendMessage(ctx context.Context, conversationID string, msg *domain.ChatMessage) error
	ListMessages(ctx context.Context, conversationID string, f domain.ListFilter) ([]*domain.ChatMessage, error)
}
