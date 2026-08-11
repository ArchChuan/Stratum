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
	// KeywordSearch returns up to topK chunks matching query in a workspace.
	// The caller guarantees docIDs is the viewer's visible set: when non-empty
	// it acts as a whitelist (AND doc_id = ANY(...)); empty means no filter.
	KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, docIDs []string, topK int) ([]domain.Chunk, error)
	// ListByDoc returns every chunk of a document ordered by chunk index.
	// workspace_id + doc_id double constraint prevents cross-workspace access
	// (doc_id has no FK).
	ListByDoc(ctx context.Context, tenantID, workspaceID, docID string) ([]domain.Chunk, error)
	CountByWorkspace(ctx context.Context, tenantID, workspaceID string) (int64, error)
	DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error
}
