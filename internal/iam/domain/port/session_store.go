package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type SessionStore interface {
	Get(ctx context.Context, id string) (*domain.Session, error)
	Save(ctx context.Context, s *domain.Session) error
	Delete(ctx context.Context, id string) error
}
