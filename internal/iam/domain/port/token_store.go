package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type TokenStore interface {
	Put(ctx context.Context, tok *domain.Token, ttl time.Duration) error
	Lookup(ctx context.Context, hash string) (*domain.Token, error)
	Revoke(ctx context.Context, hash string) error
}
