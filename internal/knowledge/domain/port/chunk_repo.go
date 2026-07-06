package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type ChunkRepo interface {
	InsertBatch(ctx context.Context, tenantID, workspaceID string, chunks []domain.Chunk) error
	KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error)
	DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error
}
