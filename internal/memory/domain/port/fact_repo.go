package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// FactRepo manages memory facts persistence.
type FactRepo interface {
	Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryFact, error)
	Update(ctx context.Context, tenantID string, fact *domain.MemoryFact) error
	ListActive(ctx context.Context, tenantID string, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error)
	SearchByContent(ctx context.Context, tenantID string, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error)
	FindSupersedeCandidates(ctx context.Context, tenantID, userID, agentID, content string, minSimilarity, maxCount float64) ([]*domain.MemoryFact, error)
	CountByUser(ctx context.Context, tenantID, userID string) (int, error)
	Delete(ctx context.Context, tenantID, id string) error
	DeleteAllByUser(ctx context.Context, tenantID, userID string) ([]string, error)
	DeleteAllByAgent(ctx context.Context, tenantID, agentID string) ([]string, error)
}
