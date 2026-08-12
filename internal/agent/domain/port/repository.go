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
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// AgentRepo persists agent configurations in the tenant schema.
//
// Write methods take an optional *auditdomain.ResourceChangeAuditEvent; when
// non-nil the audit row is inserted in the SAME transaction as the business
// write (audit failure rolls the business change back). Callers must always
// construct the event on user-facing write paths; nil is reserved for
// internal reentrant paths.
type AgentRepo interface {
	// Register creates an agent. editors (optional) are persisted in the same
	// transaction; every id must hold role admin/owner at write time or the
	// transaction fails with domain.ErrEditorNotEligible.
	Register(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editors []string) error
	Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error)
	GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error)
	GetAll(ctx context.Context) ([]*domain.AgentConfig, error)
	Remove(ctx context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error
	// Update replaces an agent's mutable fields. When editorActor is non-empty
	// the write re-validates, inside the same transaction, that the actor still
	// holds role admin/owner AND appears in resource_editors — closing the
	// check-then-write TOCTOU window for editors acting on someone else's
	// resource. Empty means no editor re-validation (owner/creator path).
	// replaceParams selects the agents.parameters JSONB write semantics:
	// true = overall replace (promote; zero fields become explicit nulls that
	// clear previously persisted values), false = merge (zero fields are
	// omitted so an old client PUT cannot erase stored sampling parameters).
	Update(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor string, replaceParams bool) error
	UpdateSystemAssistantModel(ctx context.Context, model string, memoryScope string, checkpointEnabled bool, maxIterations int, maxContextTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error)
	UpdateSystemAssistantAll(ctx context.Context, model, memoryScope string, checkpointEnabled bool, maxIterations, maxContextTokens, maxTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error)
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
	UpdateStatus(ctx context.Context, tenantID, executionID, status string) error
	DeleteExpired(ctx context.Context, tenantID string) (int64, error)
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
