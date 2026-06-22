package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SwitchTenant issues a new token pair scoped to a different tenant the user belongs to.
// POST /auth/switch-tenant
func (h *AuthHandler) SwitchTenant(c *gin.Context) {
	ctx := c.Request.Context()

	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("missing Authorization header")))
		return
	}
	claims, err := h.deps.JWTService.Verify(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid token")))
		return
	}

	var req struct {
		TenantID string `json:"tenant_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("tenant_id required")))
		return
	}

	isMember, err := h.deps.OnboardSvc.IsMember(ctx, claims.Sub, req.TenantID)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("membership check failed")))
		return
	}
	if !isMember {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("not a member of this tenant")))
		return
	}

	globalRole, err := h.deps.OnboardSvc.GetGlobalRole(ctx, claims.Sub)
	if err != nil {
		globalRole = claims.GlobalRole
	}
	tenantRole, err := h.deps.OnboardSvc.GetTenantRole(ctx, claims.Sub, req.TenantID)
	if err != nil {
		tenantRole = "member"
	}

	// Derive SystemRole from all user memberships
	// For now, use a simple derivation based on current tenant role
	// A full implementation would query all user tenants
	memberships := []domain.TenantMembership{
		{TenantID: req.TenantID, Role: tenantRole},
	}
	systemRole := domain.DeriveSystemRole(memberships)

	rawRT, accessJWT, err := h.issueTokenPair(ctx, claims.Sub, req.TenantID, tenantRole, globalRole, systemRole, claims.AvatarURL, claims.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("switch tenant: issue token pair", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
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
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("missing Authorization header")))
		return
	}
	claims, err := h.deps.JWTService.Verify(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid token")))
		return
	}

	var req struct {
		TenantName string `json:"tenant_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.TenantName == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("tenant_name required")))
		return
	}

	tenantID, err := h.deps.OnboardSvc.CreateTenantForUser(ctx, claims.Sub, req.TenantName)
	if err != nil {
		h.deps.Logger.Error("create tenant for user", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("failed to create tenant")))
		return
	}
	if h.deps.SchemaProvisioner != nil {
		if pErr := h.deps.SchemaProvisioner.ProvisionSchema(ctx, tenantID); pErr != nil {
			h.deps.Logger.Error("provision tenant schema", zap.String("tenant_id", tenantID), zap.Error(pErr))
		}
	}

	globalRole, _ := h.deps.OnboardSvc.GetGlobalRole(ctx, claims.Sub)

	// Derive SystemRole - new tenant makes user owner
	// For simplicity, derive from the new tenant owner role
	memberships := []domain.TenantMembership{
		{TenantID: tenantID, Role: "owner"},
	}
	systemRole := domain.DeriveSystemRole(memberships)

	rawRT, accessJWT, err := h.issueTokenPair(ctx, claims.Sub, tenantID, "owner", globalRole, systemRole, claims.AvatarURL, claims.GitHubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair after create tenant", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}
