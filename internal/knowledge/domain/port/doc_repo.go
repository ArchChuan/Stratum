package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type DocRepo interface {
	Save(ctx context.Context, tenantID, kbID string, doc *domain.Document) error
	List(ctx context.Context, tenantID, kbID string) ([]*domain.Document, error)
	Delete(ctx context.Context, tenantID, kbID, docID string) error
	ExistsByHash(ctx context.Context, tenantID, workspaceID, hash string) (bool, error)
	CountByWorkspace(ctx context.Context, tenantID, workspaceID string) (int, error)
}
