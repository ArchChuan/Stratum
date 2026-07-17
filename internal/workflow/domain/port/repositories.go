package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
)

type DefinitionRepository interface {
	CreateDefinition(context.Context, string, *domain.Definition) error
	GetDefinition(context.Context, string, string) (*domain.Definition, error)
	UpdateDefinition(context.Context, string, *domain.Definition, int64) error
}

type VersionRepository interface {
	CreateVersion(context.Context, string, *domain.Version) error
	GetVersion(context.Context, string, string) (*domain.Version, error)
	NextVersionNumber(context.Context, string, string) (int64, error)
}

type AtomicVersionPublisher interface {
	CreateNextVersion(context.Context, string, *domain.Definition, string) (*domain.Version, error)
}

type RunRepository interface {
	FindRunByIdempotency(context.Context, string, string) (*domain.Run, error)
	CreateRun(context.Context, string, *domain.Run) error
	GetRun(context.Context, string, string) (*domain.Run, error)
	UpdateRun(context.Context, string, *domain.Run) error
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
	ExecuteAgent(context.Context, string, string, string) (output, traceID string, err error)
}
