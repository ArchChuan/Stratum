// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
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
	tenants := make([]dto.TenantResponse, 0, len(result.Tenants))
	for _, t := range result.Tenants {
		tenants = append(tenants, tenantToDTO(t))
	}
	c.JSON(http.StatusOK, dto.ListTenantsResponse{
		Tenants: tenants, Total: result.Total, Page: result.Page, PageSize: result.PageSize,
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
	var req dto.CreateTenantRequest
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
	var req dto.UpdateTenantRequest
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

func tenantToDTO(t iamdomain.Tenant) dto.TenantResponse {
	return dto.TenantResponse{
		ID:          t.ID,
		Name:        t.Name,
		Slug:        t.Slug,
		Plan:        t.Plan,
		Status:      t.Status,
		CreatedAt:   t.CreatedAt,
		DeletedAt:   t.DeletedAt,
		MemberCount: t.MemberCount,
		IsDefault:   t.IsDefault,
	}
}
