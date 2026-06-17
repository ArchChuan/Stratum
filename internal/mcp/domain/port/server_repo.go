package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

type ServerRepo interface {
	Get(ctx context.Context, id string) (*domain.Server, error)
	List(ctx context.Context) ([]*domain.Server, error)
	Save(ctx context.Context, s *domain.Server) error
	Delete(ctx context.Context, id string) error
}
