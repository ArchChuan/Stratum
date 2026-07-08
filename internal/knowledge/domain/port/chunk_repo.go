package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// ParentChunk is the large context unit stored only in PG for Parent-Child strategies.
type ParentChunk struct {
	ID          string
	WorkspaceID string
	DocID       string
	Index       int64
	Content     string
}

type ChunkRepo interface {
	InsertBatch(ctx context.Context, tenantID, workspaceID string, chunks []domain.Chunk) error
	InsertParentBatch(ctx context.Context, tenantID, workspaceID string, parents []ParentChunk) error
	GetParentByID(ctx context.Context, tenantID, workspaceID, parentID string) (*ParentChunk, error)
	GetChunksByIDs(ctx context.Context, tenantID, workspaceID string, ids []string) ([]domain.Chunk, error)
	KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error)
	DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error
}
