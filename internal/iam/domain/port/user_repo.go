package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type UserRepo interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	Save(ctx context.Context, u *domain.User) error
}
