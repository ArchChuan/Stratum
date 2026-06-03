// Package auth provides JWT token management and authentication.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// GitHubUser holds the fields we need from the GitHub /user API.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// GitHubClient handles GitHub OAuth code exchange and user fetching.
type GitHubClient struct {
	clientID     string
	clientSecret string
	tokenURL     string
	userURL      string
	httpClient   *http.Client
}

// NewGitHubClient creates a GitHubClient.
// Pass tokenURL="" and userURL="" to use GitHub production endpoints.
func NewGitHubClient(clientID, clientSecret, tokenURL, userURL string) *GitHubClient {
	if tokenURL == "" {
		tokenURL = "https://github.com/login/oauth/access_token"
	}
	if userURL == "" {
		userURL = "https://api.github.com/user"
	}
	return &GitHubClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     tokenURL,
		userURL:      userURL,
		httpClient:   &http.Client{},
	}
}

// ClientID returns the OAuth client ID.
func (c *GitHubClient) ClientID() string { return c.clientID }

// ExchangeCode exchanges an OAuth authorization code for a GitHub access token.
func (c *GitHubClient) ExchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	params := url.Values{}
	params.Set("client_id", c.clientID)
	params.Set("client_secret", c.clientSecret)
	params.Set("code", code)
	params.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("github: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("github: decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("github: oauth error %s: %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}

// GetUser fetches GitHub user info using the given access token.
func (c *GitHubClient) GetUser(ctx context.Context, accessToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userURL, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: user request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github: user endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("github: decode user response: %w", err)
	}
	return &user, nil
}
