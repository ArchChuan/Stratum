// Package port declares consumer-side interfaces for the agent context.
//
// Repository ports are implemented by infrastructure/persistence and
// consumed by application orchestration. Cross-context capabilities
// live in their own files (capability.go, memory.go, skill.go, etc.).

package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// AgentRepo persists agent configurations in the tenant schema.
type AgentRepo interface {
	Register(ctx context.Context, cfg *domain.AgentConfig) error
	Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error)
	GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error)
	GetAll(ctx context.Context) ([]*domain.AgentConfig, error)
	Remove(ctx context.Context, id string) error
	Update(ctx context.Context, cfg *domain.AgentConfig) error
	UpdateSystemAssistantModel(ctx context.Context, model string) (*domain.AgentConfig, error)
}

// AgentSkillBinding resolves which agent is wired to a given skill through the
// agent_skill_links relation. It is a focused read port (interface
// segregation) so a consumer needing only the skill→agent lookup — e.g. the
// evaluation composition root running a skill scenario through its owning
// agent — does not have to depend on the full AgentRepo surface.
type AgentSkillBinding interface {
	FindAgentBySkill(ctx context.Context, skillID string) (agentID string, found bool, err error)
}

// CheckpointRepo persists resumable runtime snapshots for long-running agents.
type CheckpointRepo interface {
	Upsert(ctx context.Context, tenantID string, checkpoint domain.AgentExecutionCheckpoint) error
	GetLatest(ctx context.Context, tenantID, executionID string) (*domain.AgentExecutionCheckpoint, error)
	MarkCompleted(ctx context.Context, tenantID, executionID string) error
}

type ToolApprovalRepo interface {
	Create(ctx context.Context, tenantID string, approval domain.ToolApproval) (string, error)
	Get(ctx context.Context, tenantID, approvalID string) (domain.ToolApproval, error)
	Decide(ctx context.Context, tenantID, approvalID, decision, decidedBy, reason string, now time.Time) error
	ClaimExecution(ctx context.Context, tenantID, approvalID string) error
	ReleaseExecution(ctx context.Context, tenantID, approvalID string) error
	MarkOutcomeUnknown(ctx context.Context, tenantID, approvalID string) error
	MarkExecuted(ctx context.Context, tenantID, approvalID string) error
	ListPending(ctx context.Context, tenantID string) ([]domain.ToolApproval, error)
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
