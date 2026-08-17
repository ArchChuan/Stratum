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
	UpdateSystemAssistantModel(ctx context.Context, model string, memoryScope string, maxIterations int, maxContextTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error)
	UpdateSystemAssistantAll(ctx context.Context, model, memoryScope string, maxIterations, maxContextTokens, maxTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error)
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

// TaskRepo persists cross-session goal progress for agents. All methods touch
// tenant-scoped agent_tasks and take an explicit tenantID.
type TaskRepo interface {
	// Claim 原子抢占/续约 task：条件更新（status=active 且未过期 且 无主/本会话/lease 过期），
	// bump generation 作 fence，返回 claim 后的 task（含新 generation）与是否成功。
	// 无行或不可 claim（completed/abandoned/被活跃会话占用）→ (nil, false, nil)。
	Claim(ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration) (*domain.Task, bool, error)
	// Save 新建或乐观锁写回：INSERT 新行（generation=task.Generation）；已存在行仅当
	// generation==expectedGeneration 时更新（claim bump 后 stale 写被拒），冲突返回
	// ErrGenerationConflict。
	Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error
	// Get 加载单个 task；不存在返回 nil。
	Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error)
	// GetLatestActiveForOwner 返回该 owner 最新的活跃 task（updated_at DESC），
	// 无活跃 task 返回 nil。恢复入口。
	GetLatestActiveForOwner(ctx context.Context, tenantID, agentID, userID string) (*domain.Task, error)
	// DetachConversation 解除某会话的 task 引用（claimed_by='', lease 清空），
	// task 本身保留。conversation 删除时在 DeleteConversation 事务内调用。
	DetachConversation(ctx context.Context, tenantID, conversationID string) error
	// DeleteExpired 回收 expires_at 已过的 task，返回删除行数。
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
	// ListPending 返回未过期 pending 审批；userID 非空时仅返回该用户发起的（member 语义）。
	ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error)
	// ListHistory 返回非 pending 状态（decided/executed/expired/invalidated/voided/cancelled）分页列表，
	// 第二返回值为总数（admin/owner 工作台用）。
	ListHistory(ctx context.Context, tenantID string, page, pageSize int) ([]domain.ToolApproval, int, error)
	// Invalidate CAS：仅 approved/executing → invalidated，写入 invalidation_reason（审批语义失效）。
	Invalidate(ctx context.Context, tenantID, id, reason string) error
	// Void CAS：仅 approved → voided，写入 invalidation_reason（执行上下文销毁）。
	Void(ctx context.Context, tenantID, id, reason string) error
	// UpdateAssignee CAS：仅 pending 可改指定审批人（软绑定）。
	UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error
	// CascadeByConversation 事务内将关联审批 pending→cancelled、approved→voided（原因 conversation_deleted）。
	CascadeByConversation(ctx context.Context, tenantID, conversationID string) error
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
