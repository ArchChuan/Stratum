package port

import (
	"context"

	domain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// EntityMemory is the dependency interface for entity extraction and relation management.
type EntityMemory interface {
	ExtractEntities(ctx context.Context, text string, sessionCtx *domain.SessionContext) ([]*domain.Entity, error)
	GetEntity(ctx context.Context, id string) (*domain.Entity, error)
	SearchEntities(ctx context.Context, query string, sessionCtx *domain.SessionContext) ([]*domain.Entity, error)
	AddRelation(ctx context.Context, relation *domain.EntityRelation) error
	GetEntityRelations(ctx context.Context, entityID string) ([]*domain.EntityRelation, error)
	UpdateEntity(ctx context.Context, entity *domain.Entity) error
}
