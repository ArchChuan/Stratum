package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("onboarding_token required")))
		return
	}
	if h.deps.JWTService == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("service not initialized")))
		return
	}
	ob, err := h.deps.JWTService.VerifyOnboarding(req.OnboardingToken)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid or expired onboarding token")))
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
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("tenant_name required for action=create")))
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
			_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("failed to create tenant")))
			return
		}
		tenantID = result.TenantID
		userID = result.UserUUID
		if h.deps.SchemaProvisioner != nil {
			if pErr := h.deps.SchemaProvisioner.ProvisionSchema(ctx, tenantID); pErr != nil {
				h.deps.Logger.Error("provision tenant schema", zap.String("tenant_id", tenantID), zap.Error(pErr))
			}
		}
		if globalRole == "global_admin" {
			_ = h.deps.OnboardSvc.SetGlobalRole(ctx, userID, "global_admin")
		} else if dbRole, rErr := h.deps.OnboardSvc.GetGlobalRole(ctx, userID); rErr == nil && dbRole != "" {
			globalRole = dbRole
		}

	case "join":
		if req.InvitationToken == "" {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("invitation_token required for action=join")))
			return
		}
		if err := h.deps.OnboardSvc.JoinTenant(ctx, application.JoinTenantInput{
			UserID: ob.GitHubLogin, InvitationToken: req.InvitationToken,
		}); err != nil {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("invalid invitation token")))
			return
		}
		_ = c.Error(middleware.NewHTTPError(http.StatusNotImplemented, errors.New("join flow requires Plan 3 user DB")))
		return

	default:
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("action must be 'create' or 'join'")))
		return
	}

	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tenantID, "owner", globalRole, ob.AvatarURL, ob.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}
