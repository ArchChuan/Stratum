package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// FactRepo manages memory facts persistence.
type FactRepo interface {
	// Create inserts a new fact.
	Create(ctx context.Context, fact *domain.MemoryFact) error

	// GetByID retrieves a fact by ID.
	GetByID(ctx context.Context, id string) (*domain.MemoryFact, error)

	// Update modifies an existing fact.
	Update(ctx context.Context, fact *domain.MemoryFact) error

	// ListActive returns active facts within a scope.
	ListActive(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error)

	// SearchByContent performs full-text search on fact content.
	SearchByContent(ctx context.Context, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error)

	// FindSupersedeCandidates returns facts that may be superseded by new content.
	FindSupersedeCandidates(ctx context.Context, userID, agentID, content string, minSimilarity, maxCount float64) ([]*domain.MemoryFact, error)

	// CountByUser returns total fact count for a user.
	CountByUser(ctx context.Context, userID string) (int, error)

	// DeleteOldSoftDeleted removes soft-deleted facts older than retention days.
	DeleteOldSoftDeleted(ctx context.Context, retentionDays int) (int, error)
}
