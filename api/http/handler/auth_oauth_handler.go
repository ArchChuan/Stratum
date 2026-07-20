package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GitHubLogin redirects the user to GitHub OAuth authorize URL.
// GET /auth/github
func (h *AuthHandler) GitHubLogin(c *gin.Context) {
	state, err := randomState()
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("failed to generate state")))
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
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("invalid oauth state")))
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", h.deps.SecureCookies, true)

	code := c.Query("code")
	if code == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("missing code")))
		return
	}

	accessToken, err := h.deps.GitHubClient.ExchangeCode(ctx, code, h.deps.CallbackURL)
	if err != nil {
		h.deps.Logger.Error("github exchange code", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadGateway, errors.New("github oauth failed")))
		return
	}

	ghUser, err := h.deps.GitHubClient.GetUser(ctx, accessToken)
	if err != nil {
		h.deps.Logger.Error("github get user", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadGateway, errors.New("failed to fetch github user")))
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

	githubIDStr := fmt.Sprintf("%d", ghUser.ID)
	userID, dbGlobalRole, tenants, exists, lookupErr := h.deps.OnboardSvc.GetUserTenants(ctx, githubIDStr)
	if lookupErr != nil {
		h.deps.Logger.Warn("get user tenants failed, falling back to auto-join", zap.Error(lookupErr))
	}

	if globalRole == "global_admin" {
		if dbGlobalRole != "global_admin" && userID != "" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		}
	} else if dbGlobalRole != "" {
		globalRole = dbGlobalRole
	}

	if lookupErr == nil && exists && len(tenants) > 0 {
		var targetTenantID, tenantRole string
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

		// Derive SystemRole from tenant memberships
		memberships := make([]domain.TenantMembership, len(tenants))
		for i, t := range tenants {
			memberships[i] = domain.TenantMembership{TenantID: t.TenantID, Role: t.Role}
		}
		systemRole := domain.DeriveSystemRole(memberships)

		rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, targetTenantID, tenantRole, globalRole, systemRole, ghUser.AvatarURL, ghUser.Login)
		if err != nil {
			h.deps.Logger.Error("issue token pair for returning user", zap.Error(err))
			_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
			return
		}
		exchangeCode, err := h.createOAuthExchange(ctx, &iamport.OAuthExchange{
			Kind:        iamport.OAuthExchangeLogin,
			AccessToken: accessJWT,
		})
		if err != nil {
			h.deps.Logger.Error("create oauth exchange", zap.Error(err))
			_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("oauth exchange unavailable")))
			return
		}
		h.setRefreshCookie(c, rawRT)
		h.deps.Logger.Info("returning user login", zap.String("user_id", userID), zap.String("tenant_id", targetTenantID))
		h.redirectWithOAuthCode(c, frontendURL, exchangeCode)
		return
	}

	userID, targetTenantID, dbGlobalRole, err := h.deps.OnboardSvc.AutoJoinDefaultTenant(ctx, ghUser.ID, ghUser.Login, ghUser.AvatarURL, h.deps.GlobalAdmin)
	if err == nil {
		if globalRole == "global_admin" && dbGlobalRole != "global_admin" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		} else if globalRole == "" && dbGlobalRole != "" {
			globalRole = dbGlobalRole
		}
		role := "member"
		if h.deps.GlobalAdmin != "" && strings.EqualFold(ghUser.Login, h.deps.GlobalAdmin) {
			role = "owner"
		}

		// Derive SystemRole for default tenant
		systemRole := domain.DeriveSystemRole([]domain.TenantMembership{
			{TenantID: targetTenantID, Role: role},
		})

		rawRT, accessJWT, tErr := h.issueTokenPair(ctx, userID, targetTenantID, role, globalRole, systemRole, ghUser.AvatarURL, ghUser.Login)
		if tErr != nil {
			h.deps.Logger.Error("issue token pair for new user", zap.Error(tErr))
			_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
			return
		}
		exchangeCode, err := h.createOAuthExchange(ctx, &iamport.OAuthExchange{
			Kind:        iamport.OAuthExchangeLogin,
			AccessToken: accessJWT,
		})
		if err != nil {
			h.deps.Logger.Error("create oauth exchange", zap.Error(err))
			_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("oauth exchange unavailable")))
			return
		}
		h.setRefreshCookie(c, rawRT)
		h.deps.Logger.Info("new user auto-joined default tenant", zap.String("user_id", userID), zap.String("tenant_id", targetTenantID))
		h.redirectWithOAuthCode(c, frontendURL, exchangeCode)
		return
	}
	h.deps.Logger.Warn("auto-join default tenant failed, falling back to onboarding", zap.Error(err))

	ob := iamport.OnboardingClaims{
		GitHubID:    ghUser.ID,
		GitHubLogin: ghUser.Login,
		AvatarURL:   ghUser.AvatarURL,
	}
	obToken, err := h.deps.JWTService.SignOnboarding(ob, constants.OnboardingTTL)
	if err != nil {
		h.deps.Logger.Error("sign onboarding token", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token signing failed")))
		return
	}

	exchangeCode, err := h.createOAuthExchange(ctx, &iamport.OAuthExchange{
		Kind:            iamport.OAuthExchangeOnboarding,
		OnboardingToken: obToken,
		GitHubLogin:     ghUser.Login,
		AvatarURL:       ghUser.AvatarURL,
	})
	if err != nil {
		h.deps.Logger.Error("create onboarding oauth exchange", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("oauth exchange unavailable")))
		return
	}
	h.redirectWithOAuthCode(c, frontendURL, exchangeCode)
}

func (h *AuthHandler) createOAuthExchange(ctx context.Context, exchange *iamport.OAuthExchange) (string, error) {
	if h.deps.OAuthExchangeStore == nil {
		return "", errors.New("oauth exchange store unavailable")
	}
	return h.deps.OAuthExchangeStore.Create(ctx, exchange, constants.OAuthExchangeTTL)
}

func (h *AuthHandler) redirectWithOAuthCode(c *gin.Context, frontendURL, code string) {
	callback, err := url.Parse(frontendURL + "/auth/callback")
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("invalid frontend url")))
		return
	}
	query := callback.Query()
	query.Set("code", code)
	callback.RawQuery = query.Encode()
	c.Redirect(http.StatusFound, callback.String())
}
