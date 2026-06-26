package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type ChunkRepo interface {
	InsertBatch(ctx context.Context, tenantID, workspaceName string, chunks []domain.Chunk) error
	KeywordSearch(ctx context.Context, tenantID, workspaceName, query string, topK int) ([]domain.Chunk, error)
	DeleteByWorkspace(ctx context.Context, tenantID, workspaceName string) error
}
