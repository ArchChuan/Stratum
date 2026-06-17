package port

import "context"

type OAuthProvider interface {
	ExchangeCode(ctx context.Context, code string) (accessToken string, err error)
	Profile(ctx context.Context, accessToken string) (id, email string, err error)
}
