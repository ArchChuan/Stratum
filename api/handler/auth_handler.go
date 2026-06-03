// Package handler implements HTTP API request handlers.

package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	refreshTokenCookie = "refresh_token"
	accessTokenTTL     = 15 * time.Minute
	refreshTokenTTL    = 7 * 24 * time.Hour
	onboardingTTL      = 5 * time.Minute
)

// AuthHandlerDeps groups all dependencies for AuthHandler.
type AuthHandlerDeps struct {
	GitHubClient  *auth.GitHubClient
	JWTService    *auth.JWTService
	TokenStore    *auth.TokenStore
	OnboardSvc    *auth.OnboardService
	Logger        *zap.Logger
	CallbackURL   string
	GlobalAdmin   string
	SecureCookies bool
}

// AuthHandler implements the /auth/* HTTP routes.
type AuthHandler struct {
	deps AuthHandlerDeps
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(deps AuthHandlerDeps) *AuthHandler {
	return &AuthHandler{deps: deps}
}

// GitHubLogin redirects the user to GitHub OAuth authorize URL.
// GET /auth/github
func (h *AuthHandler) GitHubLogin(c *gin.Context) {
	state, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}
	c.SetCookie("oauth_state", state, 300, "/", "", h.deps.SecureCookies, true)
	redirectURL := "https://github.com/login/oauth/authorize" +
		"?client_id=" + h.deps.GitHubClient.ClientID() +
		"&redirect_uri=" + h.deps.CallbackURL +
		"&scope=user:email" +
		"&state=" + state
	c.Redirect(http.StatusFound, redirectURL)
}

// GitHubCallback handles the OAuth callback, exchanges code, and issues an onboarding token.
// GET /auth/github/callback
func (h *AuthHandler) GitHubCallback(c *gin.Context) {
	ctx := c.Request.Context()

	stateCookie, err := c.Cookie("oauth_state")
	if err != nil || stateCookie != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", h.deps.SecureCookies, true)

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	accessToken, err := h.deps.GitHubClient.ExchangeCode(ctx, code, h.deps.CallbackURL)
	if err != nil {
		h.deps.Logger.Error("github exchange code", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "github oauth failed"})
		return
	}

	ghUser, err := h.deps.GitHubClient.GetUser(ctx, accessToken)
	if err != nil {
		h.deps.Logger.Error("github get user", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch github user"})
		return
	}

	ob := auth.OnboardingClaims{
		GitHubID:    ghUser.ID,
		GitHubLogin: ghUser.Login,
		AvatarURL:   ghUser.AvatarURL,
	}
	obToken, err := h.deps.JWTService.SignOnboarding(ob, onboardingTTL)
	if err != nil {
		h.deps.Logger.Error("sign onboarding token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"onboarding_token": obToken,
		"github_login":     ghUser.Login,
		"avatar_url":       ghUser.AvatarURL,
	})
}

// Register creates or joins a tenant using the onboarding token.
// POST /auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		OnboardingToken string `json:"onboarding_token"`
		Action          string `json:"action"`
		TenantName      string `json:"tenant_name"`
		GitHubOrg       string `json:"github_org"`
		InvitationToken string `json:"invitation_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OnboardingToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "onboarding_token required"})
		return
	}

	if h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	ob, err := h.deps.JWTService.VerifyOnboarding(req.OnboardingToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired onboarding token"})
		return
	}

	globalRole := ""
	if h.deps.GlobalAdmin != "" && strings.EqualFold(ob.GitHubLogin, h.deps.GlobalAdmin) {
		globalRole = "global_admin"
	}

	userID := "github:" + ob.GitHubLogin

	var tenantID string
	switch req.Action {
	case "create":
		if req.TenantName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_name required for action=create"})
			return
		}
		result, err := h.deps.OnboardSvc.CreateTenant(ctx, auth.CreateTenantInput{
			UserID: userID, Name: req.TenantName, GitHubOrg: req.GitHubOrg,
		})
		if err != nil {
			h.deps.Logger.Error("create tenant", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create tenant"})
			return
		}
		tenantID = result.TenantID

	case "join":
		if req.InvitationToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invitation_token required for action=join"})
			return
		}
		if err := h.deps.OnboardSvc.JoinTenant(ctx, auth.JoinTenantInput{
			UserID: userID, InvitationToken: req.InvitationToken,
		}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid invitation token"})
			return
		}
		// Plan 3 adds the tenant_id lookup after join.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "join flow requires Plan 3 user DB"})
		return

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be 'create' or 'join'"})
		return
	}

	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tenantID, "admin", globalRole)
	if err != nil {
		h.deps.Logger.Error("issue token pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
		return
	}

	h.setRefreshCookie(c, rawRT)
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}

// Refresh issues a new access token and rotates the refresh token.
// POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	ctx := c.Request.Context()

	rawRT, err := c.Cookie(refreshTokenCookie)
	if err != nil || rawRT == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token cookie missing"})
		return
	}

	if h.deps.TokenStore == nil || h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	blacklisted, err := h.deps.TokenStore.IsBlacklisted(ctx, rawRT)
	if err != nil || blacklisted {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token revoked"})
		return
	}

	newRawRT, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}
	if err := h.deps.TokenStore.Rotate(ctx, rawRT, newRawRT, refreshTokenTTL); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	// Plan 3 will load full claims from DB; stub with minimal claims for now.
	claims := auth.TokenClaims{Sub: "stub", TenantID: "stub", Role: "member", JTI: newRawRT[:8]}
	accessJWT, err := h.deps.JWTService.Sign(claims, accessTokenTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
		return
	}

	h.setRefreshCookie(c, newRawRT)
	c.JSON(http.StatusOK, gin.H{"access_token": accessJWT})
}

// Logout revokes the refresh token.
// POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()
	rawRT, err := c.Cookie(refreshTokenCookie)
	if err == nil && rawRT != "" && h.deps.TokenStore != nil {
		_ = h.deps.TokenStore.Revoke(ctx, rawRT)
	}
	h.setRefreshCookie(c, "")
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// Me returns the current user's claims from the Authorization header.
// GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid Authorization header"})
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	if h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	claims, err := h.deps.JWTService.Verify(tokenStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub":         claims.Sub,
		"tenant_id":   claims.TenantID,
		"role":        claims.Role,
		"global_role": claims.GlobalRole,
	})
}

func (h *AuthHandler) issueTokenPair(ctx context.Context, userID, tenantID, role, globalRole string) (rawRT, accessJWT string, err error) {
	rawRT, err = randomState()
	if err != nil {
		return "", "", err
	}
	jti := rawRT[:8]
	if err = h.deps.TokenStore.Create(ctx, userID, tenantID, rawRT, refreshTokenTTL); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}
	claims := auth.TokenClaims{
		Sub: userID, TenantID: tenantID, Role: role, GlobalRole: globalRole, JTI: jti,
	}
	accessJWT, err = h.deps.JWTService.Sign(claims, accessTokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("sign access token: %w", err)
	}
	return rawRT, accessJWT, nil
}

func (h *AuthHandler) setRefreshCookie(c *gin.Context, value string) {
	maxAge := int(refreshTokenTTL.Seconds())
	if value == "" {
		maxAge = -1
	}
	c.SetCookie(refreshTokenCookie, value, maxAge, "/auth/refresh", "", h.deps.SecureCookies, true)
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
