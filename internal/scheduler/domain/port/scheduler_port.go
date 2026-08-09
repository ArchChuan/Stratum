// Package port defines interfaces consumed by the scheduled-task bounded
// context. Implementations live in internal/scheduler/infrastructure and in
// api/wiring (thin adapters over the workflow context).
package port

import (
	"context"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/scheduler/domain"
)

// ErrDeterministicFailure marks a fire failure that retrying cannot fix
// (version missing, input no longer matching the schema). Workers advance
// the schedule and record last_error_message; any other error from a
// WorkflowRunner is treated as transient and retried next poll.
var ErrDeterministicFailure = errors.New("deterministic scheduled fire failure")

// WorkflowRunner starts queued workflow runs on behalf of the scheduler.
// The wiring adapter delegates to the workflow RunService; the idempotency
// key makes concurrent workers' duplicate fires no-ops.
type WorkflowRunner interface {
	StartAsync(ctx context.Context, tenantID, versionID string, input map[string]any, idempotencyKey, createdBy string) error
}

// VersionInfo is the scheduler-consumable projection of a workflow version.
type VersionInfo struct {
	DefinitionID string
}

// WorkflowVersionResolver validates workflow-version references at
// create/update time so admins get a 400 instead of a silently doomed
// schedule.
type WorkflowVersionResolver interface {
	// GetVersion returns the owning workflow definition ID for versionID.
	GetVersion(ctx context.Context, tenantID, versionID string) (*VersionInfo, error)
	// ValidateInput checks input against the version's declared input schema.
	ValidateInput(ctx context.Context, tenantID, versionID string, input map[string]any) error
}

// Repository persists scheduled tasks. All methods are tenant-scoped and
// must go through the tenant schema boundary.
type Repository interface {
	Insert(ctx context.Context, tenantID string, task *domain.ScheduledTask) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.ScheduledTask, error)
	// List returns tasks newest-first (created_at DESC, id DESC tiebreak)
	// together with the total count for pagination.
	List(ctx context.Context, tenantID string, limit, offset int) ([]domain.ScheduledTask, int, error)
	Update(ctx context.Context, tenantID string, task *domain.ScheduledTask) error
	Delete(ctx context.Context, tenantID, id string) error
	// SetEnabled flips the enabled flag; nextFireAt is required on re-enable
	// (the scheduler recomputes it) and nil on disable.
	SetEnabled(ctx context.Context, tenantID, id string, enabled bool, nextFireAt *time.Time) error
	// ListDue returns enabled tasks with next_fire_at <= now, oldest first.
	ListDue(ctx context.Context, tenantID string, now time.Time, limit int) ([]domain.ScheduledTask, error)
	// RecordFire advances next_fire_at guarded on the row's current value
	// (WHERE next_fire_at = oldNext). It returns (false, nil) when a
	// concurrent worker already advanced the row — the loser skips silently.
	// Real storage errors are wrapped and propagated.
	RecordFire(ctx context.Context, tenantID, id string, firedAt time.Time, status, errorMsg string, oldNext, newNext time.Time) (bool, error)
}
