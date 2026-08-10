// Package handler implements HTTP API request handlers.

package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TenantHandler handles /tenant/* endpoints; it delegates business logic to TenantService.
type TenantHandler struct {
	svc       *application.TenantService
	inviteSvc *application.InvitationService
	adminSvc  *application.AdminService
	logger    *zap.Logger
}

// NewTenantHandler returns a TenantHandler bound to the given service.
func NewTenantHandler(
	svc *application.TenantService,
	inviteSvc *application.InvitationService,
	adminSvc *application.AdminService,
	logger *zap.Logger,
) *TenantHandler {
	return &TenantHandler{svc: svc, inviteSvc: inviteSvc, adminSvc: adminSvc, logger: logger}
}

// InviteMember POST /tenant/members/invite.
func (h *TenantHandler) InviteMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	callerRole, _ := c.Get("auth.role")
	callerID, _ := c.Get("auth.sub")
	var req gen.InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	code, err := h.inviteSvc.Create(
		c.Request.Context(), tenantID, callerID.(string), callerRole.(string), req.Email, req.Role,
	)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gen.InviteMemberResponse{
		InvitationCode: code, Email: req.Email, Role: req.Role,
	})
}

// JoinTenant POST /tenant/join.
func (h *TenantHandler) JoinTenant(c *gin.Context) {
	userValue, ok := c.Get("auth.sub")
	userID, valid := userValue.(string)
	if !ok || !valid || userID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("authenticated user required")))
		return
	}
	var req struct {
		InvitationCode string `json:"invitation_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	result, err := h.inviteSvc.JoinExisting(c.Request.Context(), req.InvitationCode, userID)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid or expired invitation")))
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenant_id": result.TenantID})
}

// ListMembers GET /tenant/members?page=1&page_size=20
// An optional role filter (?role=admin,owner) returns every member holding
// one of the given roles — the candidate set for resource editors. The
// service rejects any role outside {admin, owner}.
func (h *TenantHandler) ListMembers(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(constants.DefaultPageSize)))

	var members []domain.Member
	total := 0
	if rolesParam := c.Query("role"); rolesParam != "" {
		rawRoles := strings.Split(rolesParam, ",")
		roles := make([]string, 0, len(rawRoles))
		for _, r := range rawRoles {
			if r = strings.TrimSpace(r); r != "" {
				roles = append(roles, r)
			}
		}
		roleMembers, err := h.svc.ListMembersByRole(c.Request.Context(), tenantID, roles)
		if err != nil {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
			return
		}
		members = roleMembers
		total = len(members)
	} else {
		var err error
		var normalizedPage, normalizedPageSize int
		members, total, normalizedPage, normalizedPageSize, err = h.svc.ListMembers(c.Request.Context(), tenantID, page, pageSize)
		if err != nil {
			_ = c.Error(err)
			return
		}
		page, pageSize = normalizedPage, normalizedPageSize
	}

	resp := gen.ListMembersResponse{
		Members: make([]gen.MemberResponse, 0, len(members)),
		//nolint:gosec // total 是 COUNT(*) 结果,不可能溢出 int32(proto 契约)
		Total: int32(total),
		//nolint:gosec // page 是分页参数,不可能溢出 int32(proto 契约)
		Page: int32(page),
		//nolint:gosec // pageSize 是分页参数,不可能溢出 int32(proto 契约)
		PageSize: int32(pageSize),
	}
	for _, m := range members {
		resp.Members = append(resp.Members, gen.MemberResponse{
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

	var req gen.UpdateMemberRoleRequest
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
	c.JSON(http.StatusOK, gen.SettingsResponse{TenantID: tenantID, TenantName: name, IsDefault: isDefault, Settings: settings})
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

	var req gen.UpdateSettingsRequest
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
	items := make([]gen.TenantListItem, 0, len(tenants))
	for _, t := range tenants {
		items = append(items, gen.TenantListItem{TenantID: t.TenantID, Name: t.Name, IsDefault: t.IsDefault})
	}
	c.JSON(http.StatusOK, gen.TenantListResponse{Tenants: items})
}

// DeleteSelf DELETE /tenant — tenant owner deletes their own tenant and all associated storage.
func (h *TenantHandler) DeleteSelf(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	roleStr, _ := c.Get(middleware.ContextKeyRole)
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
