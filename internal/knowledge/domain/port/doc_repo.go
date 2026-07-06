package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type DocRepo interface {
	Save(ctx context.Context, tenantID, kbID string, doc *domain.Document) error
	List(ctx context.Context, tenantID, kbID string) ([]*domain.Document, error)
	Delete(ctx context.Context, tenantID, kbID, docID string) error
	ExistsByHash(ctx context.Context, tenantID, workspaceID, hash string) (bool, error)
	CountByWorkspace(ctx context.Context, tenantID, workspaceID string) (int, error)

	// MarkIngestStarted transitions a doc into 'processing' state with the
	// planned total chunk count. Called before dispatching the async goroutine.
	MarkIngestStarted(ctx context.Context, tenantID, docID string, totalChunks int) error
	// MarkIngestCompleted transitions a doc into 'completed' state.
	MarkIngestCompleted(ctx context.Context, tenantID, docID string, processedChunks int) error
	// MarkIngestFailed transitions a doc into 'failed' with an error message.
	MarkIngestFailed(ctx context.Context, tenantID, docID, errMsg string) error
	// RecoverStuckIngests marks docs stuck in 'processing' for longer than
	// threshold as 'failed'. Returns number of rows affected. Called on startup.
	RecoverStuckIngests(ctx context.Context, tenantID string, threshold time.Duration) (int, error)
}
