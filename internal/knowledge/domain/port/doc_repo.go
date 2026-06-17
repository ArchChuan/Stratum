package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type DocRepo interface {
	Save(ctx context.Context, kbID string, doc *domain.Document) error
	List(ctx context.Context, kbID string) ([]*domain.Document, error)
	Delete(ctx context.Context, kbID, docID string) error
}
