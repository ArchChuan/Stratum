// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AdminHandler exposes platform-admin tenant CRUD endpoints.
type AdminHandler struct {
	svc    *iamapp.AdminService
	logger *zap.Logger
}

// NewAdminHandler wires the admin service.
func NewAdminHandler(svc *iamapp.AdminService, logger *zap.Logger) *AdminHandler {
	return &AdminHandler{svc: svc, logger: logger}
}

// ListTenants GET /admin/tenants?status=active&page=1&page_size=20
func (h *AdminHandler) ListTenants(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	filter := iamdomain.TenantFilter{
		Status:   c.Query("status"),
		Page:     page,
		PageSize: pageSize,
	}
	result, err := h.svc.ListTenants(c.Request.Context(), filter)
	if err != nil {
		_ = c.Error(err)
		return
	}
	tenants := make([]gen.TenantResponse, 0, len(result.Tenants))
	for _, t := range result.Tenants {
		tenants = append(tenants, tenantToDTO(t))
	}
	//nolint:gosec // total/page/pageSize 来自分页查询,不可能溢出 int32(proto 契约)
	c.JSON(http.StatusOK, gen.ListTenantsResponse{
		Tenants: tenants, Total: int32(result.Total), Page: int32(result.Page), PageSize: int32(result.PageSize),
	})
}

// GetTenant GET /admin/tenants/:id
func (h *AdminHandler) GetTenant(c *gin.Context) {
	t, err := h.svc.GetTenant(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, tenantToDTO(*t))
}

// CreateTenant POST /admin/tenants
func (h *AdminHandler) CreateTenant(c *gin.Context) {
	var req gen.CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	t, err := h.svc.CreateTenant(c.Request.Context(), req.Name, req.Slug, req.Plan, req.Status)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, tenantToDTO(*t))
}

// UpdateTenant PATCH /admin/tenants/:id
func (h *AdminHandler) UpdateTenant(c *gin.Context) {
	id := c.Param("id")
	var req gen.UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.UpdateTenant(c.Request.Context(), id, iamdomain.TenantPatch{
		Plan:   req.Plan,
		Status: req.Status,
	}); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant updated"})
}

// DeleteTenant DELETE /admin/tenants/:id — soft delete
func (h *AdminHandler) DeleteTenant(c *gin.Context) {
	if err := h.svc.DeleteTenant(c.Request.Context(), c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant deleted"})
}

// SearchUsers GET /admin/users?query=&limit= — 平台管理员候选用户搜索。
func (h *AdminHandler) SearchUsers(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit < 1 || limit > constants.MaxPageSize {
		limit = constants.DefaultPageSize
	}
	users, err := h.svc.SearchUsers(c.Request.Context(), c.Query("query"), limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.AdminUserResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, adminUserToDTO(u))
	}
	c.JSON(http.StatusOK, gen.SearchUsersResponse{Users: resp})
}

// ListAdmins GET /admin/admins — 全部平台管理员列表。
func (h *AdminHandler) ListAdmins(c *gin.Context) {
	admins, err := h.svc.ListAdmins(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.AdminUserResponse, 0, len(admins))
	for _, u := range admins {
		resp = append(resp, adminUserToDTO(u))
	}
	c.JSON(http.StatusOK, gen.ListAdminsResponse{Admins: resp})
}

// SetAdminRole POST /admin/admins {user_id} — 提升为普通平台管理员。
func (h *AdminHandler) SetAdminRole(c *gin.Context) {
	var req gen.SetAdminRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID := c.GetString(middleware.ContextKeySub)
	if err := h.svc.SetAdminRole(c.Request.Context(), actorID, req.UserID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "admin role set"})
}

// RemoveAdminRole DELETE /admin/admins/:user_id — 移除普通平台管理员。
func (h *AdminHandler) RemoveAdminRole(c *gin.Context) {
	actorID := c.GetString(middleware.ContextKeySub)
	if err := h.svc.RemoveAdminRole(c.Request.Context(), actorID, c.Param("user_id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "admin role removed"})
}

func adminUserToDTO(u iamport.AdminUser) gen.AdminUserResponse {
	return gen.AdminUserResponse{
		UserID:      u.UserID,
		Username:    u.Username,
		GitHubLogin: u.GitHubLogin,
		AvatarURL:   &u.AvatarURL,
		GlobalRole:  string(u.GlobalRole),
	}
}

func tenantToDTO(t iamdomain.Tenant) gen.TenantResponse {
	return gen.TenantResponse{
		ID:        t.ID,
		Name:      t.Name,
		Slug:      t.Slug,
		Plan:      t.Plan,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
		DeletedAt: t.DeletedAt,
		//nolint:gosec // 成员数不可能溢出 int32(proto 契约)
		MemberCount: int32(t.MemberCount),
		IsDefault:   t.IsDefault,
	}
}
