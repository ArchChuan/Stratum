// Package handler implements HTTP API request handlers.

package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const refreshTokenCookie = "refresh_token"

// AuthHandlerDeps groups all dependencies for AuthHandler.
type AuthHandlerDeps struct {
	GitHubClient      iamport.GitHubOAuthClient
	JWTService        *application.JWTService
	TokenStore        iamport.RefreshTokenStore
	OnboardSvc        *application.OnboardService
	Logger            *zap.Logger
	SchemaProvisioner iamport.TenantSchemaProvisioner
	CallbackURL       string
	FrontendURL       string
	GlobalAdmin       string
	SecureCookies     bool
}

// AuthHandler implements the /auth/* HTTP routes.
type AuthHandler struct {
	deps AuthHandlerDeps
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(deps AuthHandlerDeps) *AuthHandler {
	return &AuthHandler{deps: deps}
}

func (h *AuthHandler) issueTokenPair(ctx context.Context, userID, tenantID, role, globalRole string, systemRole domain.SystemRole, avatarURL, githubLogin string) (rawRT, accessJWT string, err error) {
	rawRT, err = randomState()
	if err != nil {
		return "", "", err
	}
	jti := rawRT[:8]
	if err = h.deps.TokenStore.Create(ctx, userID, tenantID, rawRT, constants.RefreshTokenTTL); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}
	claims := application.TokenClaims{
		Sub: userID, TenantID: tenantID, Role: role, GlobalRole: globalRole, SystemRole: systemRole, JTI: jti,
		AvatarURL: avatarURL, GitHubLogin: githubLogin,
	}
	accessJWT, err = h.deps.JWTService.Sign(claims, constants.AccessTokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("sign access token: %w", err)
	}
	return rawRT, accessJWT, nil
}

func (h *AuthHandler) setRefreshCookie(c *gin.Context, value string) {
	maxAge := int(constants.RefreshTokenTTL.Seconds())
	if value == "" {
		maxAge = -1
	}
	if h.deps.SecureCookies {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(refreshTokenCookie, value, maxAge, "/", "", h.deps.SecureCookies, true)
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
