// Package handler implements HTTP API request handlers.

package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TenantHandler handles /tenant/* endpoints; it delegates business logic to TenantService.
type TenantHandler struct {
	svc      *application.TenantService
	adminSvc *application.AdminService
	logger   *zap.Logger
}

// NewTenantHandler returns a TenantHandler bound to the given service.
func NewTenantHandler(svc *application.TenantService, adminSvc *application.AdminService, logger *zap.Logger) *TenantHandler {
	return &TenantHandler{svc: svc, adminSvc: adminSvc, logger: logger}
}

// ListMembers GET /tenant/members?page=1&page_size=20
func (h *TenantHandler) ListMembers(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(constants.DefaultPageSize)))

	members, total, normalizedPage, normalizedPageSize, err := h.svc.ListMembers(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		_ = c.Error(err)
		return
	}

	resp := dto.ListMembersResponse{
		Members:  make([]dto.MemberResponse, 0, len(members)),
		Total:    total,
		Page:     normalizedPage,
		PageSize: normalizedPageSize,
	}
	for _, m := range members {
		resp.Members = append(resp.Members, dto.MemberResponse{
			UserID:      m.UserID,
			GitHubLogin: m.GitHubLogin,
			AvatarURL:   m.AvatarURL,
			Role:        m.Role,
			JoinedAt:    m.JoinedAt,
		})
	}
	c.JSON(http.StatusOK, resp)
}

// UpdateMemberRole PATCH /tenant/members/:user_id/role
func (h *TenantHandler) UpdateMemberRole(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleVal, _ := c.Get("auth.role")
	callerRole, _ := roleVal.(string)
	if callerRole != "owner" {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenOwner))
		return
	}
	callerID, _ := c.Get("auth.sub")
	callerIDStr, _ := callerID.(string)
	userID := c.Param("user_id")
	if callerIDStr == userID {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenSelfModify))
		return
	}

	targetRole, err := h.svc.GetMemberRole(c.Request.Context(), tenantID, userID)
	if err != nil {
		if !errors.Is(err, domain.ErrMemberNotFound) {
			err = middleware.NewHTTPError(http.StatusNotFound, domain.ErrMemberNotFound)
		}
		_ = c.Error(err)
		return
	}
	if targetRole == "owner" {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenOwnerRole))
		return
	}

	var req dto.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	if err := h.svc.UpdateMemberRole(c.Request.Context(), tenantID, callerIDStr, callerRole, userID, req.Role); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role updated"})
}

// RemoveMember DELETE /tenant/members/:user_id
func (h *TenantHandler) RemoveMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleVal, _ := c.Get("auth.role")
	callerRole, _ := roleVal.(string)
	callerID, _ := c.Get("auth.sub")
	callerIDStr, _ := callerID.(string)
	userID := c.Param("user_id")

	if err := h.svc.RemoveMember(c.Request.Context(), tenantID, callerIDStr, callerRole, userID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

// GetSettings GET /tenant/settings
func (h *TenantHandler) GetSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	name, isDefault, settings, err := h.svc.GetSettings(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.SettingsResponse{TenantID: tenantID, TenantName: name, IsDefault: isDefault, Settings: settings})
}

// UpdateSettings PATCH /tenant/settings
func (h *TenantHandler) UpdateSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenAdminOrOwner))
		return
	}

	var req dto.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	err := h.svc.UpdateSettings(c.Request.Context(), tenantID, roleStr, application.UpdateSettingsInput{
		Name:     req.Name,
		Settings: req.Settings,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}

// ListUserTenants GET /tenant/list — all tenants the current user belongs to.
func (h *TenantHandler) ListUserTenants(c *gin.Context) {
	userID, ok := c.Get("auth.sub")
	userIDStr, _ := userID.(string)
	if !ok || userIDStr == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errUnauthorized))
		return
	}
	tenants, err := h.svc.ListUserTenants(c.Request.Context(), userIDStr)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items := make([]dto.TenantListItem, 0, len(tenants))
	for _, t := range tenants {
		items = append(items, dto.TenantListItem{TenantID: t.TenantID, Name: t.Name, IsDefault: t.IsDefault})
	}
	c.JSON(http.StatusOK, dto.TenantListResponse{Tenants: items})
}

// SetEmbedModel PATCH /tenant/embed-model — set-once: fails if embed_model already configured.
func (h *TenantHandler) SetEmbedModel(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenAdminOrOwner))
		return
	}
	var req struct {
		EmbedModel string `json:"embed_model" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.SetEmbedModel(c.Request.Context(), tenantID, roleStr, req.EmbedModel); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"embed_model": req.EmbedModel})
}

// DeleteSelf DELETE /tenant — tenant owner deletes their own tenant and all associated storage.
func (h *TenantHandler) DeleteSelf(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleStr, _ := c.Get("tenant_role")
	if roleStr != "owner" {
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, application.ErrForbiddenOwner))
		return
	}
	if h.adminSvc == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("admin service unavailable")))
		return
	}
	if err := h.adminSvc.DeleteTenant(c.Request.Context(), tenantID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant deleted"})
}
