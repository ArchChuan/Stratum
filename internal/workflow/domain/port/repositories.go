package port

import (
	"context"
	"errors"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
)

// ErrAgentApprovalPending 表示 workflow 执行 agent 节点时触发了 agent 原生工具审批
// （tool_approvals），节点应暂停等待审批，不进入失败重试路径。由 executor/adapter
// 翻译 agent 审批错误后返回，reconcile 据此把节点切到 agent 审批恢复通道。
var ErrAgentApprovalPending = errors.New("agent approval pending")

type DefinitionRepository interface {
	CreateDefinition(context.Context, string, *domain.Definition, *auditdomain.ResourceChangeAuditEvent) error
	GetDefinition(context.Context, string, string) (*domain.Definition, error)
	UpdateDefinition(context.Context, string, *domain.Definition, int64, *auditdomain.ResourceChangeAuditEvent) error
	DeleteDefinition(context.Context, string, string, *auditdomain.ResourceChangeAuditEvent) error
}

type DefinitionListQuery struct {
	Query  string
	Offset int
	Limit  int
}

type DefinitionQueryRepository interface {
	ListDefinitions(context.Context, string, DefinitionListQuery) ([]domain.Definition, int, error)
}

type VersionRepository interface {
	CreateVersion(context.Context, string, *domain.Version, *auditdomain.ResourceChangeAuditEvent) error
	GetVersion(context.Context, string, string) (*domain.Version, error)
	NextVersionNumber(context.Context, string, string) (int64, error)
	// SetActiveVersion 把生效指针指回历史已发布版本（回退，不产生新版本）；
	// 事务内更新 active_version_id 并写入审计。目标版本归属由调用方校验。
	SetActiveVersion(context.Context, string, string, string, *auditdomain.ResourceChangeAuditEvent) error
}

type VersionListQuery struct {
	Offset int
	Limit  int
}

type VersionQueryRepository interface {
	ListVersions(context.Context, string, string, VersionListQuery) ([]domain.Version, int, error)
}

type AtomicVersionPublisher interface {
	CreateNextVersion(context.Context, string, *domain.Definition, string, *auditdomain.ResourceChangeAuditEvent) (*domain.Version, error)
}

type RunRepository interface {
	FindRunByIdempotency(context.Context, string, string) (*domain.Run, error)
	CreateRun(context.Context, string, *domain.Run) error
	GetRun(context.Context, string, string) (*domain.Run, error)
	UpdateRun(context.Context, string, *domain.Run) error
}

type RunListQuery struct {
	CreatedBy    string
	DefinitionID string
	Status       domain.RunStatus
	Offset       int
	Limit        int
}

type RunQueryRepository interface {
	ListRuns(context.Context, string, RunListQuery) ([]domain.Run, int, error)
}

type IdempotentRunCreator interface {
	CreateRunIdempotent(context.Context, string, *domain.Run) (*domain.Run, bool, error)
}

type AttemptRepository interface {
	SaveAttempt(context.Context, string, domain.NodeAttempt) error
	ListAttempts(context.Context, string, string) ([]domain.NodeAttempt, error)
}

type EventRepository interface {
	AppendEvent(context.Context, string, domain.Event) (domain.Event, error)
	ListEvents(context.Context, string, string, int64, int) ([]domain.Event, error)
}

type AtomicCheckpointRepository interface {
	CheckpointAttempt(context.Context, string, domain.NodeAttempt, domain.Event) error
	CheckpointRun(context.Context, string, *domain.Run, domain.Event) error
}

type NodeExecutionRequest struct {
	TenantID       string
	RunID          string
	Node           domain.Node
	AttemptNo      int
	Input          string
	RunInput       map[string]any
	NodeOutputs    map[string]string
	IdempotencyKey string
	Approved       bool
	ApprovalID     string
	BeforeEffect   func() error
	OnOutputDelta  func(string) error
	// UserID 是发起运行的执行人真实 user_id（run.CreatedBy），透传给 agent/skill
	// 执行链路，使审批请求人、会话、记忆与轨迹归属执行人而非合成标识。
	UserID string
	// ExecutionID 是该 agent 节点在该 run 内确定性的执行 ID（"wf:runID:nodeID"），
	// 首次执行与审批后/暂停后重跑一致，供 agent checkpoint 续跑命中同一恢复键。
	ExecutionID string
}

type ApprovalRepository interface {
	CreateApproval(context.Context, string, *domain.Approval, domain.Event) error
	ListApprovals(context.Context, string, string, bool) ([]domain.Approval, error)
}

type EffectRepository interface {
	CreateEffectIntent(context.Context, string, *domain.EffectIntent) error
	UpdateEffectIntent(context.Context, string, *domain.EffectIntent, domain.EffectIntentStatus) error
	ListEffectIntents(context.Context, string, string) ([]domain.EffectIntent, error)
}

type EffectFenceRepository interface {
	EffectRepository
	StartExternalEffect(context.Context, string, *domain.EffectIntent, string, int64) error
}

type NodeExecutionResult struct {
	Output         string
	TraceID        string
	ConditionValue bool
	Retryable      bool
	ErrorCode      string
	Paused         bool
	ToolSteps      []NodeToolStep
}

type NodeToolStep struct {
	ToolName   string `json:"tool_name"`
	DurationMS int64  `json:"duration_ms"`
	Summary    string `json:"summary"`
}

type NodeExecutorRegistry interface {
	Execute(context.Context, NodeExecutionRequest) (NodeExecutionResult, error)
}

type Clock interface {
	Now() time.Time
}

type RuntimePort interface {
	ClaimRun(context.Context, string, time.Duration) (tenantID string, run *domain.Run, claimed bool, err error)
	ReleaseRun(context.Context, string, string, string, int64) error
}

type ControlRepository interface {
	GetRun(context.Context, string, string) (*domain.Run, error)
	ControlRun(context.Context, string, string, int64, domain.RunStatus, string, domain.Event) error
	ListApprovals(context.Context, string, string, bool) ([]domain.Approval, error)
	DecideApproval(context.Context, string, string, int64, string, domain.ApprovalDecision, string, string, domain.Event) error
	ListEffectIntents(context.Context, string, string) ([]domain.EffectIntent, error)
	ResolveEffect(context.Context, string, string, int64, domain.ManualAction, string, string, domain.Event) error
}

type AgentExecutor interface {
	ExecuteAgent(context.Context, string, string, string, string, string) (output, traceID string, err error)
}

// AgentApprovalResolver 判断指定 executionID 对应的 agent 原生工具审批是否已全部
// 终态（可续跑）。workflow 执行 agent 节点遇审批暂停后，reconcile 用它判定恢复
// 时机；pending 审批存在时继续等待，全部终态后切回正常重跑续跑。
type AgentApprovalResolver interface {
	ResolveAgentApproval(ctx context.Context, tenantID, executionID string) (done bool, err error)
}

// SkillBindingResolver 提供 agent 已挂载的技能（allowedSkills），用于校验 workflow
// 的 skill 节点绑定：只有选定 agent 实际启用的技能才能被 workflow 引用。
type SkillBindingResolver interface {
	AgentAllowedSkills(context.Context, string, string) ([]string, error)
}
