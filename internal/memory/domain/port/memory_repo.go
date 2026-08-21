package port

import (
	"context"
	"time"

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
	// ListExpired returns up to limit ids of memory entries that must be
	// physically removed: entries whose expires_at predates now, or entries
	// without expires_at whose created_at predates createdBefore (episodic
	// TTL). Ordered by created_at for stable bounded draining. The GC deletes
	// vectors first, then the rows, so PG stays the source of truth.
	ListExpired(ctx context.Context, tenantID string, now, createdBefore time.Time, limit int) ([]string, error)
	// DeleteByIDs removes the given entry rows in one tenant transaction.
	// Missing ids are no-ops (DELETE ... WHERE id::text = ANY($1)).
	DeleteByIDs(ctx context.Context, tenantID string, ids []string) error
	Stats(ctx context.Context, tenantID string) (*domain.MemoryStats, error)
	GetSummary(ctx context.Context, tenantID, sessionID string) (string, error)
}
