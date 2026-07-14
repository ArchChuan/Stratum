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
			Sub:         guest.UserID,
			TenantID:    guest.TenantID,
			Role:        "member",
			GlobalRole:  "",
			SystemRole:  string(systemRole),
			AvatarURL:   guest.AvatarURL,
			GitHubLogin: guest.GitHubLogin,
		},
	}
}

// GuestLogin provisions a temporary guest account and issues a token pair.
// The guest joins the default tenant as a member — same data visibility and
// permission model as a GitHub member; it only differs by an expiry after which
// the reaper removes the account and any tenants it created.
// POST /auth/guest
func (h *AuthHandler) GuestLogin(c *gin.Context) {
	ctx := c.Request.Context()

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

	// Guest is a plain default-tenant member → SystemRoleUser, no global role.
	systemRole := domain.DeriveSystemRole([]domain.TenantMembership{
		{TenantID: guest.TenantID, Role: "member"},
	})

	rawRT, accessJWT, err := h.issueTokenPair(ctx, guest.UserID, guest.TenantID, "member", "", systemRole, guest.AvatarURL, guest.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair for guest", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	h.deps.Logger.Info("guest account created", zap.String("user_id", guest.UserID), zap.String("tenant_id", guest.TenantID))
	c.JSON(http.StatusCreated, newGuestLoginResponse(guest, accessJWT, systemRole))
}
