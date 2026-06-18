package port

import (
	"context"

	domain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// Persistence is the dependency interface for memory persistence to storage.
type Persistence interface {
	Save(ctx context.Context, entry *domain.MemoryEntry) error
	Load(ctx context.Context, sessionCtx *domain.SessionContext) ([]*domain.MemoryEntry, error)
	SaveEntities(ctx context.Context, entities []*domain.Entity) error
	LoadEntities(ctx context.Context, sessionCtx *domain.SessionContext) ([]*domain.Entity, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteTenant(ctx context.Context, tenantID string) error
}
