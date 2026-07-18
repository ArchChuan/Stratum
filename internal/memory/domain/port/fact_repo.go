package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// SupersedeCandidate pairs a fact with its trigram similarity to a query string.
type SupersedeCandidate struct {
	Fact       *domain.MemoryFact
	Similarity float64
}

// FactRepo manages memory facts persistence.
type FactRepo interface {
	Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryFact, error)
	Update(ctx context.Context, tenantID string, fact *domain.MemoryFact) error
	ListActive(ctx context.Context, tenantID string, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error)
	SearchByContent(ctx context.Context, tenantID string, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error)
	FindSupersedeCandidates(ctx context.Context, tenantID string, filter domain.ScopeFilter, content string, minSimilarity, maxCount float64) ([]*SupersedeCandidate, error)
	CountByUser(ctx context.Context, tenantID, userID string) (int, error)
	Delete(ctx context.Context, tenantID, id string) error
	DeleteAllByUser(ctx context.Context, tenantID, userID string) ([]string, error)
	DeleteAllByAgent(ctx context.Context, tenantID, agentID string) ([]string, error)
}
