package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/middleware"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
)

// ProviderHandler serves LLM provider CRUD and management endpoints.
type ProviderHandler struct {
	svc *llmapp.ProviderService
}

// NewProviderHandler creates a ProviderHandler.
func NewProviderHandler(svc *llmapp.ProviderService) *ProviderHandler {
	return &ProviderHandler{svc: svc}
}

// List GET /admin/providers — returns all providers for the tenant.
func (h *ProviderHandler) List(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	providers, err := h.svc.List(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

// Create POST /admin/providers — creates a new provider.
func (h *ProviderHandler) Create(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var input llmapp.CreateProviderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	provider, err := h.svc.Create(c.Request.Context(), tenantID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, provider)
}

// Update PUT /admin/providers/:id — updates an existing provider.
func (h *ProviderHandler) Update(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var input llmapp.UpdateProviderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	input.ID = c.Param("id")
	provider, err := h.svc.Update(c.Request.Context(), tenantID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, provider)
}

// Delete DELETE /admin/providers/:id — removes a provider.
func (h *ProviderHandler) Delete(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

// Discover POST /admin/providers/:id/discover — triggers model discovery for a provider.
func (h *ProviderHandler) Discover(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	models, err := h.svc.DiscoverModels(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "count": len(models)})
}

// HealthCheck POST /admin/providers/:id/health — checks provider connectivity.
func (h *ProviderHandler) HealthCheck(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if err := h.svc.HealthCheck(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}
