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
	// ArchiveColdFacts marks cold active facts as archived and returns the
	// archived ids so the caller can delete their vectors (status change must
	// be reflected in the vector store).
	ArchiveColdFacts(ctx context.Context, tenantID string) ([]string, error)

	// ListUserSummaries lists the user's active user-scope summaries, newest first.
	ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*domain.HistorySegment, error)
	// CountUserSummaries counts the user's active user-scope summaries.
	CountUserSummaries(ctx context.Context, tenantID, userID string) (int, error)
	// Delete removes a summary owned by userID; ErrSummaryNotFound when no row matches.
	Delete(ctx context.Context, tenantID, userID, id string) error
}
