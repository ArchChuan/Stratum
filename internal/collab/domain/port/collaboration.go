// Package port defines interfaces consumed by the collab bounded context.
// Implementations live in internal/collab/infrastructure/persistence.
package port

import (
	"context"
	"time"

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
	// UpdateStatus advances plan status, guarded by WHERE status IN
	// ('created','running') so a terminal plan is never rewritten and the
	// worker's stale completion on a canceled plan is a tolerated no-op
	// (RowsAffected == 0). startedAt/completedAt are written on the migrating
	// transitions: Start (startedAt), terminal states (completedAt).
	UpdateStatus(ctx context.Context, tenantID, id string, status domain.CollabStatus, startedAt, completedAt *time.Time) error
}

// TaskStepRepo persists and claims task steps with lease-based locking.
// All methods are tenant-scoped; ClaimNextTask scans tenants transactionally.
type TaskStepRepo interface {
	InsertBatch(ctx context.Context, tenantID string, steps []domain.TaskStep) error
	// ClaimNextTask claims one ready step across all tenants: public.tenants
	// scan + per-tenant advisory lock + lease reclamation. A step is claimable
	// when status='pending', or claimed/running with an expired lease, and the
	// owning plan is still created|running. On success the step generation is
	// incremented and the lease (owner + expiry) is written.
	ClaimNextTask(ctx context.Context, owner string, lease time.Duration) (tenantID string, step *domain.TaskStep, ok bool, err error)
	// RenewLease extends the lease of a step owned by owner. Stale owners are
	// rejected: WHERE id = $2 AND claimed_by = $1 AND lease_expires_at > NOW().
	RenewLease(ctx context.Context, tenantID, stepID, owner string, lease time.Duration) error
	// UpdateStatus writes a terminal/release state guarded by the claim
	// generation: a stale writer (re-claimed by another worker) is rejected.
	// A release write back to pending increments retry_count (the claimer is
	// the only writer of pending); terminal writes leave retry_count intact.
	UpdateStatus(ctx context.Context, tenantID, stepID string, expectedGeneration int64, status domain.TaskStatus, output map[string]any, errMsg string) error
	GetReadyTasks(ctx context.Context, tenantID, planID string) ([]domain.TaskStep, error)
	// CancelPending marks pending steps of a plan canceled (worker refuses to
	// claim them); used by plan cancellation.
	CancelPending(ctx context.Context, tenantID, planID string) error
	// CountByStatus tallies step statuses for plan completion judgment.
	CountByStatus(ctx context.Context, tenantID, planID string) (map[domain.TaskStatus]int, error)
}

// SharedContextRepo provides optimistic-lock access to collaboration-global state.
type SharedContextRepo interface {
	Get(ctx context.Context, tenantID, planID string) (*domain.SharedContext, error)
	// Update is an upsert: the first writer creates the row at version 0,
	// later writers bump version with WHERE version = sc.Version; a mismatch
	// (or a concurrent first-insert) yields ErrCollabConflict for a retry.
	Update(ctx context.Context, tenantID string, sc domain.SharedContext) error
}
