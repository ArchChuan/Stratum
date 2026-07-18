package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// SupersedeCandidate pairs a fact with its trigram similarity to a query string.
type SupersedeCandidate struct {
	Fact       *domain.MemoryFact
	Similarity float64
}

// ExtractedFactWrite carries stable extraction provenance into atomic persistence.
type ExtractedFactWrite struct {
	Fact        *domain.MemoryFact
	Identity    domain.FactSourceIdentity
	PayloadHash string
	EntityNames []string
}

// ExtractedFactWriter atomically persists one replay-safe fact and its entity mutations.
type ExtractedFactWriter interface {
	CreateExtracted(ctx context.Context, tenantID string, write *ExtractedFactWrite) (fact *domain.MemoryFact, created bool, err error)
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
	// PurgeSuperseded hard-deletes superseded facts whose updated_at is older
	// than olderThan, capped at limit rows per call. It targets only
	// status='superseded' (facts replaced by newer ones — true dead weight);
	// archived facts are durable long-term memory and are never purged here.
	// Returns the number of rows deleted.
	PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) (int, error)
}
