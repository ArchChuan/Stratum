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

// RefreshTokenStore is the consumer-side port for refresh token lifecycle used by auth handlers.
type RefreshTokenStore interface {
	Create(ctx context.Context, userID, tenantID, rawToken string, ttl time.Duration) error
	Rotate(ctx context.Context, oldRaw, newRaw string, ttl time.Duration) error
	Revoke(ctx context.Context, rawToken string) error
	IsBlacklisted(ctx context.Context, rawToken string) (bool, error)
	GetActiveClaims(ctx context.Context, rawToken string) (*domain.StoredSession, error)
}
