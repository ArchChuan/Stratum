// Package port defines interfaces consumed by the collab bounded context.
// Implementations live in api/wiring/collab.go (Phase 2).
package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
)

// AgentRunner executes a single agent step within a collaboration plan.
// The collab worker calls this for each claimed task step.
type AgentRunner interface {
	RunAgentStep(ctx context.Context, tenantID, agentID string, input map[string]any) (output map[string]any, traceID string, err error)
}

// CollaborationRepo persists collaboration plans.
type CollaborationRepo interface {
	Insert(ctx context.Context, collab domain.Collaboration) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.Collaboration, error)
	ListByTenant(ctx context.Context, tenantID string, limit, offset int) ([]domain.Collaboration, error)
	UpdateStatus(ctx context.Context, tenantID, id string, status domain.CollabStatus) error
}

// TaskStepRepo persists and claims task steps with lease-based locking.
type TaskStepRepo interface {
	InsertBatch(ctx context.Context, steps []domain.TaskStep) error
	ClaimTask(ctx context.Context, planID string) (*domain.TaskStep, error) // FOR UPDATE SKIP LOCKED
	UpdateStatus(ctx context.Context, tenantID, stepID string, status domain.TaskStatus, output map[string]any, errMsg string) error
	GetReadyTasks(ctx context.Context, planID string) ([]domain.TaskStep, error)
}

// SharedContextRepo provides optimistic-lock access to collaboration-global state.
type SharedContextRepo interface {
	Get(ctx context.Context, planID string) (*domain.SharedContext, error)
	Update(ctx context.Context, sc domain.SharedContext) error // optimistic lock: version must match
}
