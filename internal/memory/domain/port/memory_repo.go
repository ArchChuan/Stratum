package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// MemoryRepo persists and queries memory entries within a tenant schema.
// Implementations resolve the tenant from ctx (tenantdb.WithTenant).
//
// Returns domain.ErrEntryNotFound for misses; nil tx errors for "no rows
// affected" mutations are not surfaced — callers should not assume entry
// existed.
type MemoryRepo interface {
	Add(ctx context.Context, entry *domain.MemoryEntry) error
	Get(ctx context.Context, tenantID, id string) (*domain.MemoryEntry, error)
	Search(ctx context.Context, tenantID, userID, query string, limit int) ([]*domain.MemoryEntry, error)
	Delete(ctx context.Context, tenantID, id string) error
	ClearSession(ctx context.Context, tenantID, sessionID string) error
	DeleteAllByUser(ctx context.Context, tenantID, userID string) error
	DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error
	Stats(ctx context.Context, tenantID string) (*domain.MemoryStats, error)
	GetSummary(ctx context.Context, tenantID, sessionID string) (string, error)
}
