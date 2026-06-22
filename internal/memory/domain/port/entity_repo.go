package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// EntityRepo manages memory entities persistence.
type EntityRepo interface {
	// Create inserts a new entity.
	Create(ctx context.Context, entity *domain.MemoryEntity) error

	// GetByID retrieves an entity by ID.
	GetByID(ctx context.Context, id string) (*domain.MemoryEntity, error)

	// Update modifies an existing entity.
	Update(ctx context.Context, entity *domain.MemoryEntity) error

	// FindByNameAndType finds an entity by fuzzy name match within a scope.
	FindByNameAndType(ctx context.Context, userID, name, entityType string, threshold float64) (*domain.MemoryEntity, error)

	// ListProfiles returns entities with profiles for context injection.
	ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error)

	// CountByUser returns total entity count for a user.
	CountByUser(ctx context.Context, userID string) (int, error)

	// TopByFactCount returns entities with highest fact counts.
	TopByFactCount(ctx context.Context, tenantID string, limit int) ([]EntityFactCount, error)
}

// EntityFactCount holds entity name and associated fact count.
type EntityFactCount struct {
	Name  string
	Count int
}
