package port

import (
	"context"
	"errors"
	"time"
)

type OAuthExchangeKind string

const (
	OAuthExchangeLogin      OAuthExchangeKind = "login"
	OAuthExchangeOnboarding OAuthExchangeKind = "onboarding"
)

var ErrOAuthExchangeInvalid = errors.New("oauth exchange code is invalid or expired")

// OAuthExchange is the server-side result referenced by a short-lived opaque code.
type OAuthExchange struct {
	Kind            OAuthExchangeKind `json:"kind"`
	AccessToken     string            `json:"access_token,omitempty"`
	OnboardingToken string            `json:"onboarding_token,omitempty"`
	GitHubLogin     string            `json:"github_login,omitempty"`
	AvatarURL       string            `json:"avatar_url,omitempty"`
}

type OAuthExchangeStore interface {
	Create(ctx context.Context, exchange *OAuthExchange, ttl time.Duration) (string, error)
	Consume(ctx context.Context, code string) (*OAuthExchange, error)
}
