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
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
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
	// version, when non-nil, is written into resource_versions (通用产品版本基座)
	// in the SAME transaction: current published version demoted, new row inserted
	// with revision_no=MAX+1, agents.active_version_id pointed at it. nil skips
	// version writes entirely (internal reentrant paths with no product change).
	Update(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor string, replaceParams bool, version *versioningdomain.Version) error
	// Rollback restores a deprecated product version, all in one transaction:
	// the target version's payload is applied back to the agent row (full
	// parameters replace), bindings replaced, the target version promoted back
	// to published, and agents.active_version_id repointed at it. editorActor,
	// when non-empty, re-validates editor eligibility inside the write
	// transaction (same TOCTOU closure as Update). The write fails closed with
	// domain.ErrNotFound if the agent no longer exists and with
	// versioningdomain.ErrVersionNotFound if the target is not a deprecated
	// historical version of this agent.
	Rollback(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor, targetVersionID string) error
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
	// GetLatestActiveByConversation returns the freshest active checkpoint for a
	// conversation (status in running/paused/waiting_approval, not expired).
	// Freshness window (ActiveExecutionFreshnessWindow) applies only to
	// running/paused — a waiting_approval row's updated_at does not advance
	// while a human reviews the approval, so it is gated solely by expires_at.
	// Returns (nil, nil) when no active checkpoint exists.
	GetLatestActiveByConversation(ctx context.Context, tenantID, conversationID string) (*domain.AgentExecutionCheckpoint, error)
	// UpdateStatusFrom CAS-transitions a checkpoint from one status to another.
	// Used to claim a waiting_approval checkpoint before resuming: only the
	// tab/device that wins the CAS may continue the execution.
	UpdateStatusFrom(ctx context.Context, tenantID, executionID, from, to string) error
	// AdvanceRunGeneration atomically increments the resume-generation fence
	// only when it still equals expect. A failure means a concurrent resume
	// already won the race (double-tab/double-device protection).
	AdvanceRunGeneration(ctx context.Context, tenantID, executionID string, expect int) error
	// Terminate moves a checkpoint to a terminal status (failed/expired) with a
	// refreshed expires_at so DeleteExpired reclaims it after retention.
	Terminate(ctx context.Context, tenantID, executionID, status string) error
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
	// Cancel CAS：仅 pending 且未过期 → cancelled（发起人主动撤回 / 管理员代撤）。
	// 0 行（非 pending 或已过期）→ ErrApprovalAlreadyDecided，与 Decide 同语义。
	Cancel(ctx context.Context, tenantID, approvalID, actor, reason string, now time.Time) error
	ClaimExecution(ctx context.Context, tenantID, approvalID string) error
	ReleaseExecution(ctx context.Context, tenantID, approvalID string) error
	MarkOutcomeUnknown(ctx context.Context, tenantID, approvalID string) error
	MarkExecuted(ctx context.Context, tenantID, approvalID string) error
	// ListPending 返回未过期 pending 审批；userID 非空时仅返回该用户发起的（member 语义）。
	ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error)
	// ListActionable 返回未过期 pending + approved 审批（F2：审批列表展示 approved
	// 待执行态）。身份过滤语义与 ListPending 一致；配额 enforcePendingQuota 仍用
	// ListPending（pending-only），防止把待执行行计入 MaxPendingApprovalsPerActor。
	ListActionable(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error)
	// ListHistory 返回非 pending 状态（decided/executed/expired/invalidated/voided/cancelled）分页列表，
	// 第二返回值为总数（admin/owner 工作台用）。userID 非空时仅返回该用户发起的
	// （member 语义），COUNT 与 SELECT 同步按 user_id 过滤保证 total 与列表一致。
	ListHistory(ctx context.Context, tenantID, userID string, page, pageSize int) ([]domain.ToolApproval, int, error)
	// Invalidate CAS：仅 approved/executing → invalidated，写入 invalidation_reason（审批语义失效）。
	Invalidate(ctx context.Context, tenantID, id, reason string) error
	// Void CAS：仅 approved → voided，写入 invalidation_reason（执行上下文销毁）。
	Void(ctx context.Context, tenantID, id, reason string) error
	// InvalidateStaleForTool 作废同 execution 同 server+tool 的旧 pending 审批（方案 C，
	// mcp_tool 门控 + 未过期）。返回作废行数。只作废 pending，approved/executed 由消费
	// 路径（CAS）或过期处理，严禁作废。
	InvalidateStaleForTool(ctx context.Context, tenantID, executionID, serverID, toolName string) (int64, error)
	// ExpireStale 将 expires_at 已过的 pending/approved 审批标记为 expired（H4 过期清扫，
	// decided_by=system:expiry 保证审计可对账）。返回过期行数；executed 等终态不受影响。
	ExpireStale(ctx context.Context, tenantID string) (int64, error)
	// UpdateAssignee CAS：仅 pending 可改指定审批人（软绑定）。
	UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error
	// CascadeByConversation 事务内将关联审批 pending→cancelled、approved→voided（原因 conversation_deleted）。
	CascadeByConversation(ctx context.Context, tenantID, conversationID string) error
	// ListByExecution 返回指定 execution_id 的全部审批行（含终态）。workflow 恢复
	// 判定用：存在未过期 pending 即视为仍未决，全部终态/过期才可续跑。
	ListByExecution(ctx context.Context, tenantID, executionID string) ([]domain.ToolApproval, error)
}

// ChatRepo persists chat conversations and messages in the tenant schema.
type ChatRepo interface {
	// CreateConversation 创建会话；source 标记会话来源（manual/workflow 等），
	// 空值按 manual 处理。workflow 自动会话带 source 标记供列表过滤隐藏。
	CreateConversation(ctx context.Context, tenantID, agentID, userID, name, source string) (*domain.ChatConversation, error)
	GetConversation(ctx context.Context, tenantID, convID string) (*domain.ChatConversation, error)
	ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*domain.ChatConversation, error)
	RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error
	DeleteConversation(ctx context.Context, tenantID, convID, userID string) error
	AddMessage(ctx context.Context, tenantID string, msg *domain.ChatMessage) error
	ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*domain.ChatMessage, error)
	CleanupExpired(ctx context.Context, tenantID string) error
	DeleteByAgent(ctx context.Context, tenantID, agentID string) error
}

// CompactionStore 持久化跨轮复用的压缩摘要。组装侧与循环侧共用同一存储：
// 组装侧读写（covered_until 单调推进），循环侧只读组装侧已生成的摘要。
// 与 memory_summaries 语义独立（对话连续性 vs facts/偏好），独立建表。
//
// 契约：
//   - GetCoverage 无覆盖时返回 (nil, nil)，不返回错误。
//   - Upsert 按 conversation_id upsert（每会话一条），覆盖已存在段并递增 version。
//   - 任何持久化失败都必须返回 error 供调用方降级，禁止吞错。
type CompactionStore interface {
	GetCoverage(ctx context.Context, tenantID, conversationID string) (*domain.CompactionCoverage, error)
	Upsert(ctx context.Context, tenantID string, seg *domain.CompactionSegment) error
}
