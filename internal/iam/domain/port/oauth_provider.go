package port

import "context"

// GitHubProfile carries the user fields returned by the GitHub /user API.
type GitHubProfile struct {
	ID        int64
	Login     string
	AvatarURL string
}

// GitHubOAuthClient abstracts GitHub OAuth code exchange and profile retrieval.
type GitHubOAuthClient interface {
	ClientID() string
	ExchangeCode(ctx context.Context, code, redirectURI string) (accessToken string, err error)
	GetUser(ctx context.Context, accessToken string) (*GitHubProfile, error)
}
