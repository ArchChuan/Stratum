package port

import (
	"context"

	domain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// VectorMemory is the dependency interface for vector-backed long-term memory.
type VectorMemory interface {
	Add(ctx context.Context, entry *domain.MemoryEntry) error
	Get(ctx context.Context, id string) (*domain.MemoryEntry, error)
	Search(ctx context.Context, req *domain.MemorySearchRequest) ([]*domain.MemorySearchResult, error)
	Delete(ctx context.Context, id string) error
	Clear(ctx context.Context, sessionCtx *domain.SessionContext) error
	GetStats(ctx context.Context, sessionCtx *domain.SessionContext) (*domain.MemoryStats, error)
	Cleanup(ctx context.Context) error
	AddWithVector(ctx context.Context, entry *domain.MemoryEntry, vector []float32) error
	SemanticSearch(ctx context.Context, query string, sessionCtx *domain.SessionContext, limit int) ([]*domain.MemorySearchResult, error)
	HybridSearch(ctx context.Context, req *domain.MemorySearchRequest) ([]*domain.MemorySearchResult, error)
}
