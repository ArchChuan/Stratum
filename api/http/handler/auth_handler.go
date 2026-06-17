// Package handler implements HTTP API request handlers.

package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	iamoauth "github.com/byteBuilderX/stratum/internal/iam/infrastructure/oauth"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const refreshTokenCookie = "refresh_token"

// AuthHandlerDeps groups all dependencies for AuthHandler.
type AuthHandlerDeps struct {
	GitHubClient  *iamoauth.GitHubClient
	JWTService    *application.JWTService
	TokenStore    *iampersistence.TokenStore
	OnboardSvc    *application.OnboardService
	Logger        *zap.Logger
	Pool          *pgxpool.Pool
	CallbackURL   string
	FrontendURL   string
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

	frontendURL := h.deps.FrontendURL
	if frontendURL == "" {
		frontendURL = "http://localhost:3002"
	}

	globalRole := ""
	if h.deps.GlobalAdmin != "" && strings.EqualFold(ghUser.Login, h.deps.GlobalAdmin) {
		globalRole = "global_admin"
	}

	// Returning user: check all tenants, pick the right one per rules.
	githubIDStr := fmt.Sprintf("%d", ghUser.ID)
	userID, dbGlobalRole, tenants, exists, lookupErr := h.deps.OnboardSvc.GetUserTenants(ctx, githubIDStr)
	if lookupErr != nil {
		h.deps.Logger.Warn("get user tenants failed, falling back to auto-join", zap.Error(lookupErr))
	}

	// Config-based GlobalAdmin overrides DB value; also sync DB if needed.
	if globalRole == "global_admin" {
		if dbGlobalRole != "global_admin" && userID != "" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		}
	} else if dbGlobalRole != "" {
		globalRole = dbGlobalRole
	}

	var targetTenantID string
	var tenantRole string
	if lookupErr == nil && exists && len(tenants) > 0 {
		// Pick earliest non-default tenant; fall back to default tenant.
		for _, t := range tenants {
			if !t.IsDefault {
				targetTenantID = t.TenantID
				tenantRole = t.Role
				break
			}
		}
		if targetTenantID == "" {
			targetTenantID = tenants[0].TenantID
			tenantRole = tenants[0].Role
		}
		rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, targetTenantID, tenantRole, globalRole, ghUser.AvatarURL, ghUser.Login)
		if err != nil {
			h.deps.Logger.Error("issue token pair for returning user", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
			return
		}
		h.setRefreshCookie(c, rawRT)
		h.deps.Logger.Info("returning user login", zap.String("user_id", userID), zap.String("tenant_id", targetTenantID))
		c.Redirect(http.StatusFound, frontendURL+"/auth/callback?access_token="+accessJWT)
		return
	}

	// New user: auto-join default tenant.
	userID, targetTenantID, dbGlobalRole, err = h.deps.OnboardSvc.AutoJoinDefaultTenant(ctx, ghUser.ID, ghUser.Login, ghUser.AvatarURL, h.deps.GlobalAdmin)
	if err != nil {
		h.deps.Logger.Warn("auto-join default tenant failed, falling back to onboarding", zap.Error(err))
	} else {
		// Sync global_admin from config on first join.
		if globalRole == "global_admin" && dbGlobalRole != "global_admin" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		} else if globalRole == "" && dbGlobalRole != "" {
			globalRole = dbGlobalRole
		}
		rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, targetTenantID, func() string {
			if h.deps.GlobalAdmin != "" && strings.EqualFold(ghUser.Login, h.deps.GlobalAdmin) {
				return "owner"
			}
			return "member"
		}(), globalRole, ghUser.AvatarURL, ghUser.Login)
		if err != nil {
			h.deps.Logger.Error("issue token pair for new user", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
			return
		}
		h.setRefreshCookie(c, rawRT)
		h.deps.Logger.Info("new user auto-joined default tenant", zap.String("user_id", userID), zap.String("tenant_id", targetTenantID))
		c.Redirect(http.StatusFound, frontendURL+"/auth/callback?access_token="+accessJWT)
		return
	}
	h.deps.Logger.Info("new user, redirecting to onboarding", zap.String("github_login", ghUser.Login))

	ob := application.OnboardingClaims{
		GitHubID:    ghUser.ID,
		GitHubLogin: ghUser.Login,
		AvatarURL:   ghUser.AvatarURL,
	}
	obToken, err := h.deps.JWTService.SignOnboarding(ob, constants.OnboardingTTL)
	if err != nil {
		h.deps.Logger.Error("sign onboarding token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
		return
	}

	redirectURL := fmt.Sprintf("%s/auth/callback?onboarding_token=%s&github_login=%s&avatar_url=%s",
		frontendURL, obToken, ghUser.Login, ghUser.AvatarURL)
	c.Redirect(http.StatusFound, redirectURL)
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

	var userID, tenantID string
	switch req.Action {
	case "create":
		if req.TenantName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_name required for action=create"})
			return
		}
		result, err := h.deps.OnboardSvc.CreateTenant(ctx, application.CreateTenantInput{
			GitHubID:    ob.GitHubID,
			GitHubLogin: ob.GitHubLogin,
			AvatarURL:   ob.AvatarURL,
			Name:        req.TenantName,
			GitHubOrg:   req.GitHubOrg,
		})
		if err != nil {
			h.deps.Logger.Error("create tenant", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create tenant"})
			return
		}
		tenantID = result.TenantID
		userID = result.UserUUID
		if h.deps.Pool != nil {
			if pErr := tenantdb.ProvisionTenantSchema(ctx, h.deps.Pool, tenantID); pErr != nil {
				h.deps.Logger.Error("provision tenant schema", zap.String("tenant_id", tenantID), zap.Error(pErr))
			}
		}
		// Sync global_admin from config into DB on first create.
		if globalRole == "global_admin" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		} else {
			// Read existing DB role in case it was set before.
			if dbRole, rErr := h.deps.OnboardSvc.GetGlobalRole(ctx, userID); rErr == nil && dbRole != "" {
				globalRole = dbRole
			}
		}

	case "join":
		if req.InvitationToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invitation_token required for action=join"})
			return
		}
		if err := h.deps.OnboardSvc.JoinTenant(ctx, application.JoinTenantInput{
			UserID: ob.GitHubLogin, InvitationToken: req.InvitationToken,
		}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid invitation token"})
			return
		}
		c.JSON(http.StatusNotImplemented, gin.H{"error": "join flow requires Plan 3 user DB"})
		return

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be 'create' or 'join'"})
		return
	}

	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tenantID, "owner", globalRole, ob.AvatarURL, ob.GitHubLogin)
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

	storedClaims, err := h.deps.TokenStore.GetActiveClaims(ctx, rawRT)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	newRawRT, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}
	if err := h.deps.TokenStore.Rotate(ctx, rawRT, newRawRT, constants.RefreshTokenTTL); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	// Read global_role from DB so it stays current across refreshes.
	globalRole := ""
	if h.deps.OnboardSvc != nil {
		if dbRole, rErr := h.deps.OnboardSvc.GetGlobalRole(ctx, storedClaims.UserID); rErr == nil {
			globalRole = dbRole
		}
	}

	// Read actual tenant role from DB so role changes take effect on next refresh.
	tenantRole := "member"
	if h.deps.OnboardSvc != nil {
		if r, rErr := h.deps.OnboardSvc.GetTenantRole(ctx, storedClaims.UserID, storedClaims.TenantID); rErr == nil {
			tenantRole = r
		}
	}

	claims := application.TokenClaims{
		Sub: storedClaims.UserID, TenantID: storedClaims.TenantID, Role: tenantRole, JTI: newRawRT[:8],
		GlobalRole: globalRole,
		AvatarURL:  storedClaims.AvatarURL, GitHubLogin: storedClaims.GitHubLogin,
	}
	accessJWT, err := h.deps.JWTService.Sign(claims, constants.AccessTokenTTL)
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
		"sub":          claims.Sub,
		"tenant_id":    claims.TenantID,
		"role":         claims.Role,
		"global_role":  claims.GlobalRole,
		"avatar_url":   claims.AvatarURL,
		"github_login": claims.GitHubLogin,
	})
}

func (h *AuthHandler) issueTokenPair(ctx context.Context, userID, tenantID, role, globalRole, avatarURL, githubLogin string) (rawRT, accessJWT string, err error) {
	rawRT, err = randomState()
	if err != nil {
		return "", "", err
	}
	jti := rawRT[:8]
	if err = h.deps.TokenStore.Create(ctx, userID, tenantID, rawRT, constants.RefreshTokenTTL); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}
	claims := application.TokenClaims{
		Sub: userID, TenantID: tenantID, Role: role, GlobalRole: globalRole, JTI: jti,
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
	// SameSite=None allows cross-origin requests (frontend on :3002, backend on :8080).
	// Requires Secure=true in production; dev uses SecureCookies=false.
	if h.deps.SecureCookies {
		c.SetSameSite(http.SameSiteNoneMode)
	} else {
		c.SetSameSite(http.SameSiteLaxMode)
	}
	c.SetCookie(refreshTokenCookie, value, maxAge, "/", "", h.deps.SecureCookies, true)
}

// SwitchTenant issues a new token pair scoped to a different tenant the user belongs to.
// POST /auth/switch-tenant
func (h *AuthHandler) SwitchTenant(c *gin.Context) {
	ctx := c.Request.Context()

	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
		return
	}
	claims, err := h.deps.JWTService.Verify(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	var req struct {
		TenantID string `json:"tenant_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id required"})
		return
	}

	// Verify the user is a member of the requested tenant.
	isMember, err := h.deps.OnboardSvc.IsMember(ctx, claims.Sub, req.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "membership check failed"})
		return
	}
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "not a member of this tenant"})
		return
	}

	// Always read global_role from DB so it reflects the latest value.
	globalRole, err := h.deps.OnboardSvc.GetGlobalRole(ctx, claims.Sub)
	if err != nil {
		globalRole = claims.GlobalRole // fallback to token value on DB error
	}

	// Read the actual tenant role from DB for the target tenant.
	tenantRole, err := h.deps.OnboardSvc.GetTenantRole(ctx, claims.Sub, req.TenantID)
	if err != nil {
		tenantRole = "member" // safe fallback
	}

	rawRT, accessJWT, err := h.issueTokenPair(ctx, claims.Sub, req.TenantID, tenantRole, globalRole, claims.AvatarURL, claims.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("switch tenant: issue token pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
		return
	}
	h.setRefreshCookie(c, rawRT)
	c.JSON(http.StatusOK, gin.H{"access_token": accessJWT, "tenant_id": req.TenantID})
}

// CreateUserTenant creates a new tenant for an already-authenticated user and switches
// them to it as owner. Requires a valid Bearer JWT.
// POST /auth/create-tenant
func (h *AuthHandler) CreateUserTenant(c *gin.Context) {
	ctx := c.Request.Context()

	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
		return
	}
	claims, err := h.deps.JWTService.Verify(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	var req struct {
		TenantName string `json:"tenant_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TenantName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_name required"})
		return
	}

	// For authenticated users we already have their user UUID; use a minimal CreateTenant
	// that skips the GitHub re-upsert by passing a sentinel GitHubID of 0 (owner insert
	// happens through a raw INSERT instead). We use OnboardSvc.CreateTenantForUser.
	tenantID, err := h.deps.OnboardSvc.CreateTenantForUser(ctx, claims.Sub, req.TenantName)
	if err != nil {
		h.deps.Logger.Error("create tenant for user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create tenant"})
		return
	}
	if h.deps.Pool != nil {
		if pErr := tenantdb.ProvisionTenantSchema(ctx, h.deps.Pool, tenantID); pErr != nil {
			h.deps.Logger.Error("provision tenant schema", zap.String("tenant_id", tenantID), zap.Error(pErr))
		}
	}

	globalRole, _ := h.deps.OnboardSvc.GetGlobalRole(ctx, claims.Sub)

	rawRT, accessJWT, err := h.issueTokenPair(ctx, claims.Sub, tenantID, "owner", globalRole, claims.AvatarURL, claims.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair after create tenant", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
		return
	}

	h.setRefreshCookie(c, rawRT)
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
