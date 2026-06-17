// Package handler implements HTTP API request handlers.

package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TenantHandler handles /tenant/* endpoints; it delegates business logic to TenantService.
type TenantHandler struct {
	svc    *application.TenantService
	logger *zap.Logger
}

// NewTenantHandler returns a TenantHandler bound to the given service.
func NewTenantHandler(svc *application.TenantService, logger *zap.Logger) *TenantHandler {
	return &TenantHandler{svc: svc, logger: logger}
}

// ListMembers GET /tenant/members?page=1&page_size=20
func (h *TenantHandler) ListMembers(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(constants.DefaultPageSize)))

	members, total, normalizedPage, normalizedPageSize, err := h.svc.ListMembers(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		h.logger.Error("list members failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "database error"})
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

// InviteMember POST /tenant/members/invite
func (h *TenantHandler) InviteMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}

	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}

	var req dto.InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	inviterID, _ := c.Get("auth.sub")
	inviterIDStr, _ := inviterID.(string)

	invitationID, invitationURL, expiresAt, err := h.svc.InviteMember(c.Request.Context(), tenantID, inviterIDStr, req.Email, req.Role)
	if err != nil {
		if errors.Is(err, application.ErrInviterMissing) {
			c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "inviter identity missing"})
			return
		}
		h.logger.Error("insert invitation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "invitation creation failed"})
		return
	}
	c.JSON(http.StatusCreated, dto.InviteMemberResponse{
		InvitationID:  invitationID,
		Email:         req.Email,
		Role:          req.Role,
		InvitationURL: invitationURL,
		ExpiresAt:     expiresAt,
	})
}

// UpdateMemberRole PATCH /tenant/members/:user_id/role
func (h *TenantHandler) UpdateMemberRole(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	roleVal, _ := c.Get("auth.role")
	callerRole, _ := roleVal.(string)
	if callerRole != "owner" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "owner role required"})
		return
	}
	callerID, _ := c.Get("auth.sub")
	callerIDStr, _ := callerID.(string)
	userID := c.Param("user_id")
	if callerIDStr == userID {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "cannot change your own role"})
		return
	}

	// Match original handler ordering: probe target role before body bind.
	targetRole, err := h.svc.GetMemberRole(c.Request.Context(), tenantID, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	if targetRole == "owner" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "cannot change owner's role"})
		return
	}

	var req dto.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	err = h.svc.UpdateMemberRole(c.Request.Context(), tenantID, callerIDStr, callerRole, userID, req.Role)
	switch {
	case errors.Is(err, application.ErrForbiddenOwnerRole):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "cannot change owner's role"})
	case errors.Is(err, domain.ErrMemberNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "member not found"})
	case err != nil:
		h.logger.Error("update member role failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "update failed"})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "role updated"})
	}
}

// RemoveMember DELETE /tenant/members/:user_id
func (h *TenantHandler) RemoveMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	roleVal, _ := c.Get("auth.role")
	callerRole, _ := roleVal.(string)
	callerID, _ := c.Get("auth.sub")
	callerIDStr, _ := callerID.(string)
	userID := c.Param("user_id")

	err := h.svc.RemoveMember(c.Request.Context(), tenantID, callerIDStr, callerRole, userID)
	switch {
	case errors.Is(err, application.ErrForbiddenAdminOrOwner):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
	case errors.Is(err, application.ErrForbiddenSelfModify):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "cannot remove yourself"})
	case errors.Is(err, application.ErrForbiddenRemoveOwner):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "cannot remove owner"})
	case errors.Is(err, application.ErrForbiddenAdminRemove):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin cannot remove another admin"})
	case errors.Is(err, domain.ErrMemberNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "member not found"})
	case err != nil:
		h.logger.Error("remove member failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "remove failed"})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "member removed"})
	}
}

// GetSettings GET /tenant/settings
func (h *TenantHandler) GetSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	name, settings, err := h.svc.GetSettings(c.Request.Context(), tenantID)
	switch {
	case errors.Is(err, domain.ErrTenantNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	case err != nil:
		h.logger.Error("get settings failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "settings parse error"})
		return
	}
	c.JSON(http.StatusOK, dto.SettingsResponse{TenantID: tenantID, TenantName: name, Settings: settings})
}

// UpdateSettings PATCH /tenant/settings
func (h *TenantHandler) UpdateSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}

	var req dto.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	err := h.svc.UpdateSettings(c.Request.Context(), tenantID, roleStr, application.UpdateSettingsInput{
		Name:     req.Name,
		Settings: req.Settings,
	})
	switch {
	case errors.Is(err, application.ErrForbiddenAdminOrOwner):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
	case errors.Is(err, domain.ErrTenantNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "tenant not found"})
	case errors.Is(err, application.ErrInvalidSettings):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: "invalid settings"})
	case err != nil:
		h.logger.Error("update settings failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "update failed"})
	default:
		c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
	}
}

// ListUserTenants GET /tenant/list — all tenants the current user belongs to.
func (h *TenantHandler) ListUserTenants(c *gin.Context) {
	userID, ok := c.Get("auth.sub")
	userIDStr, _ := userID.(string)
	if !ok || userIDStr == "" {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "unauthorized"})
		return
	}
	tenants, err := h.svc.ListUserTenants(c.Request.Context(), userIDStr)
	if err != nil {
		h.logger.Error("list user tenants", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "database error"})
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
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}
	var req struct {
		EmbedModel string `json:"embed_model" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	err := h.svc.SetEmbedModel(c.Request.Context(), tenantID, roleStr, req.EmbedModel)
	switch {
	case errors.Is(err, application.ErrForbiddenAdminOrOwner):
		c.JSON(http.StatusForbidden, dto.ErrorResponse{Code: 403, Message: "admin or owner role required"})
	case errors.Is(err, application.ErrEmbedModelAlreadySet):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: 400, Message: "embed_model already set and cannot be changed"})
	case errors.Is(err, domain.ErrTenantNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: 404, Message: "tenant not found"})
	case err != nil:
		h.logger.Error("set embed_model failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: 500, Message: "update failed"})
	default:
		c.JSON(http.StatusOK, gin.H{"embed_model": req.EmbedModel})
	}
}
