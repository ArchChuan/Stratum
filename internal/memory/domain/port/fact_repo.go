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
	// ListUserFacts 返回某用户的 active 记忆事实，newest first，分页。
	// 与 CountByUser（同样只统计 active）配套构成分页 total。
	ListUserFacts(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.MemoryFact, error)
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
	// Returns the ids of the deleted rows so the caller can remove their
	// vectors (the GC backstop for superseded facts past retention).
	PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) ([]string, error)

	// CountAll 统计租户 memory_facts 的全部行数（任意状态）——迁移开始时的
	// 快照总数（progress.total），口径不随迁移期间并发写入漂移。
	CountAll(ctx context.Context, tenantID string) (int, error)

	// ListAllFacts 分页返回租户全部事实（任意状态，按 created_at, id 稳定排序），
	// 作为迁移回填的主数据源（key=fact.ID 幂等 Upsert，断点续传按 offset 推进）。
	ListAllFacts(ctx context.Context, tenantID string, limit, offset int) ([]*domain.MemoryFact, error)
}
