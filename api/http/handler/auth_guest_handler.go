package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type guestLoginUserResponse struct {
	Sub         string `json:"sub"`
	TenantID    string `json:"tenant_id"`
	Role        string `json:"role"`
	GlobalRole  string `json:"global_role"`
	SystemRole  string `json:"system_role"`
	AvatarURL   string `json:"avatar_url"`
	GitHubLogin string `json:"github_login"`
}

type guestLoginResponse struct {
	AccessToken string                 `json:"access_token"`
	TenantID    string                 `json:"tenant_id"`
	User        guestLoginUserResponse `json:"user"`
}

func newGuestLoginResponse(guest *application.GuestAccount, accessToken string, systemRole domain.SystemRole) guestLoginResponse {
	return guestLoginResponse{
		AccessToken: accessToken,
		TenantID:    guest.TenantID,
		User: guestLoginUserResponse{
			Sub:      guest.UserID,
			TenantID: guest.TenantID,
			// Guest owns their private sandbox tenant; "member" would not permit
			// creating the agents/knowledge a trial requires.
			Role:        "owner",
			GlobalRole:  "",
			SystemRole:  string(systemRole),
			AvatarURL:   guest.AvatarURL,
			GitHubLogin: guest.GitHubLogin,
		},
	}
}

// GuestLogin provisions a temporary guest account and issues a token pair.
//
// Security model: guests never join the default tenant. Every guest gets a
// dedicated per-guest sandbox tenant (owner seat) that is provisioned and
// activated before tokens are issued, so an unauthenticated caller can only
// ever reach data inside their own empty tenant. The sandbox tenant and the
// guest user are removed by the guest reaper after GuestAccountTTL.
//
// The endpoint is gated by AuthHandlerDeps.GuestAuthEnabled: when disabled the
// handler fails closed with 403. It defaults to enabled (restricted sandbox
// mode) because the frontend login page exposes a guest trial entry; operators
// can fully disable it with GUEST_AUTH_ENABLED=false.
// POST /auth/guest
func (h *AuthHandler) GuestLogin(c *gin.Context) {
	ctx := c.Request.Context()

	if !h.deps.GuestAuthEnabled {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("guest login disabled")))
		return
	}
	if h.deps.OnboardSvc == nil || h.deps.JWTService == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("service not initialized")))
		return
	}

	guest, err := h.deps.OnboardSvc.CreateGuest(ctx)
	if err != nil {
		h.deps.Logger.Error("create guest account", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("failed to create guest account")))
		return
	}

	// Activate the sandbox tenant: apply tenant DDL and flip status to active.
	// On failure the tenant stays provisioning (all requireActive routes fail
	// closed) and the reaper cleans it up with the guest.
	if err := completeTenantProvision(ctx, h.deps.SchemaProvisioner, guest.TenantID); err != nil {
		h.deps.Logger.Error("provision guest sandbox tenant", zap.String("tenant_id", guest.TenantID), zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("guest sandbox provisioning failed")))
		return
	}

	// Guest is owner of their own sandbox tenant → SystemRoleUser, no global role.
	systemRole := domain.DeriveSystemRole([]domain.TenantMembership{
		{TenantID: guest.TenantID, Role: "owner"},
	})

	rawRT, accessJWT, err := h.issueTokenPair(ctx, guest.UserID, guest.TenantID, "owner", "", systemRole, guest.AvatarURL, guest.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair for guest", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	h.deps.Logger.Info("guest account created", zap.String("user_id", guest.UserID), zap.String("tenant_id", guest.TenantID))
	c.JSON(http.StatusCreated, newGuestLoginResponse(guest, accessJWT, systemRole))
}
