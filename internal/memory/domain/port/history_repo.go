package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type HistoryRepo interface {
	NextBatch(ctx context.Context, tenantID string, minEntries, limit int) (*domain.HistoryBatch, error)
	Upsert(ctx context.Context, tenantID string, segment *domain.HistorySegment) error
	NextOverflow(ctx context.Context, tenantID string, recentMax, earlierMax, limit int) (*domain.HistoryOverflowGroup, error)
	ReplaceOverflow(ctx context.Context, tenantID string, replacement *domain.HistorySegment, sourceIDs []string) error
	Maintain(ctx context.Context, tenantID string) error
	ArchiveColdFacts(ctx context.Context, tenantID string) (int, error)
}
