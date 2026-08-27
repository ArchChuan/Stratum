package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// EntityRepo manages memory entities persistence.
type EntityRepo interface {
	// Create inserts a new entity.
	Create(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error

	// GetByID retrieves an entity by ID.
	GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryEntity, error)

	// Update modifies an existing entity.
	Update(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error

	// FindByNameAndType finds an entity by fuzzy name match within a scope.
	FindByNameAndType(ctx context.Context, tenantID string, filter domain.ScopeFilter, name, entityType string, threshold float64) (*domain.MemoryEntity, error)

	// ListUserEntities lists the user's active user-scope entities, newest-seen first.
	ListUserEntities(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.MemoryEntity, error)

	// CountUserEntities returns the user's active user-scope entity count.
	CountUserEntities(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteAllByUser hard-deletes all entities owned by userID within the tenant schema.
	DeleteAllByUser(ctx context.Context, tenantID, userID string) error

	// DeleteAllByAgent hard-deletes all entities owned by agentID within the tenant schema.
	DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error

	// Delete removes a single entity by id.
	Delete(ctx context.Context, tenantID, id string) error
}
