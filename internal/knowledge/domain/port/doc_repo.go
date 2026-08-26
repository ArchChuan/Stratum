package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type DocRepo interface {
	// Save persists the document row. It returns true when the row was
	// inserted and false when an existing row with the same ID conflicted
	// (INSERT ... ON CONFLICT (id) DO NOTHING). The boolean is the
	// cross-instance admission gate: a deterministic docID (seed) inserted by
	// two pods concurrently must run the async pipeline exactly once — only
	// the instance that actually inserted the row spawns the job.
	Save(ctx context.Context, tenantID, kbID string, doc *domain.Document) (bool, error)
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

	// VisibleDocIDs returns the doc IDs of a workspace visible to viewerID.
	// role is the viewer's tenant role (resolved by the caller) — whitelist
	// matching is user OR role OR creator. Rows with both whitelist arrays
	// empty are always visible (workspace visibility inheritance).
	VisibleDocIDs(ctx context.Context, tenantID, workspaceID, viewerID, role string) ([]string, error)
	// GetByID returns a document scoped to a workspace (doc_id has no FK,
	// workspace_id + id double constraint prevents cross-workspace access).
	GetByID(ctx context.Context, tenantID, workspaceID, docID string) (*domain.Document, error)
	// SetDocAccess replaces the document-level whitelist.
	SetDocAccess(ctx context.Context, tenantID, docID string, userIDs, roleIDs []string) error
}
