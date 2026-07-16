// Package port declares consumer-side interfaces for the agent context.
//
// Repository ports are implemented by infrastructure/persistence and
// consumed by application orchestration. Cross-context capabilities
// live in their own files (capability.go, memory.go, skill.go, etc.).

package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// AgentRepo persists agent configurations in the tenant schema.
type AgentRepo interface {
	Register(ctx context.Context, cfg *domain.AgentConfig) error
	Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error)
	GetAll(ctx context.Context) ([]*domain.AgentConfig, error)
	Remove(ctx context.Context, id string) error
	Update(ctx context.Context, cfg *domain.AgentConfig) error
}

// ExecutionRepo persists agent execution history in the tenant schema.
type ExecutionRepo interface {
	Insert(ctx context.Context, r domain.ExecutionRecord) error
	List(ctx context.Context, opts domain.ListOptions) ([]domain.ExecutionRecord, int64, error)
}

// ToolTraceRepo persists raw tool IO and compact summaries for audit/debug.
type ToolTraceRepo interface {
	InsertBatch(ctx context.Context, tenantID string, traces []domain.ToolObservation) error
	ListByTraceID(ctx context.Context, tenantID, traceID string) ([]domain.ToolObservation, error)
	ListByConversation(ctx context.Context, tenantID, conversationID string, limit int) ([]domain.ToolObservation, error)
}

// TraceEventRepo persists append-only agent trajectory events.
type TraceEventRepo interface {
	Insert(ctx context.Context, tenantID string, event domain.AgentTraceEvent) error
	InsertBatch(ctx context.Context, tenantID string, events []domain.AgentTraceEvent) error
	ListByTraceID(ctx context.Context, tenantID, traceID string) ([]domain.AgentTraceEvent, error)
}

// CheckpointRepo persists resumable runtime snapshots for long-running agents.
type CheckpointRepo interface {
	Upsert(ctx context.Context, tenantID string, checkpoint domain.AgentExecutionCheckpoint) error
	GetLatest(ctx context.Context, tenantID, executionID string) (*domain.AgentExecutionCheckpoint, error)
	MarkCompleted(ctx context.Context, tenantID, executionID string) error
}

// ChatRepo persists chat conversations and messages in the tenant schema.
type ChatRepo interface {
	CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*domain.ChatConversation, error)
	GetConversation(ctx context.Context, tenantID, convID string) (*domain.ChatConversation, error)
	ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*domain.ChatConversation, error)
	RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error
	DeleteConversation(ctx context.Context, tenantID, convID, userID string) error
	AddMessage(ctx context.Context, tenantID string, msg *domain.ChatMessage) error
	ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*domain.ChatMessage, error)
	CleanupExpired(ctx context.Context, tenantID string) error
	DeleteByAgent(ctx context.Context, tenantID, agentID string) error
}
