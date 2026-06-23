package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Refresh issues a new access token and rotates the refresh token.
// POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	ctx := c.Request.Context()

	rawRT, err := c.Cookie(refreshTokenCookie)
	if err != nil || rawRT == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("refresh token cookie missing")))
		return
	}

	if h.deps.TokenStore == nil || h.deps.JWTService == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("service not initialized")))
		return
	}

	blacklisted, err := h.deps.TokenStore.IsBlacklisted(ctx, rawRT)
	if err != nil || blacklisted {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("refresh token revoked")))
		return
	}

	storedClaims, err := h.deps.TokenStore.GetActiveClaims(ctx, rawRT)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid refresh token")))
		return
	}

	newRawRT, err := randomState()
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token generation failed")))
		return
	}
	if err := h.deps.TokenStore.Rotate(ctx, rawRT, newRawRT, constants.RefreshTokenTTL); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid refresh token")))
		return
	}

	globalRole := ""
	if h.deps.OnboardSvc != nil {
		if dbRole, rErr := h.deps.OnboardSvc.GetGlobalRole(ctx, storedClaims.UserID); rErr == nil {
			globalRole = dbRole
		}
	}

	tenantRole := "member"
	if h.deps.OnboardSvc != nil {
		if r, rErr := h.deps.OnboardSvc.GetTenantRole(ctx, storedClaims.UserID, storedClaims.TenantID); rErr == nil {
			tenantRole = r
		}
	}

	claims := application.TokenClaims{
		Sub: storedClaims.UserID, TenantID: storedClaims.TenantID, Role: tenantRole, JTI: newRawRT[:8],
		GlobalRole: globalRole,
		SystemRole: domain.DeriveSystemRole([]domain.TenantMembership{{TenantID: storedClaims.TenantID, Role: tenantRole}}),
		AvatarURL:  storedClaims.AvatarURL, GitHubLogin: storedClaims.GitHubLogin,
	}
	accessJWT, err := h.deps.JWTService.Sign(claims, constants.AccessTokenTTL)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token signing failed")))
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
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("missing or invalid Authorization header")))
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	if h.deps.JWTService == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("service not initialized")))
		return
	}

	claims, err := h.deps.JWTService.Verify(tokenStr)
	if err != nil {
		h.deps.Logger.Debug("auth/me verify", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid token")))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub":          claims.Sub,
		"tenant_id":    claims.TenantID,
		"role":         claims.Role,
		"global_role":  claims.GlobalRole,
		"system_role":  string(claims.SystemRole),
		"avatar_url":   claims.AvatarURL,
		"github_login": claims.GitHubLogin,
	})
}
