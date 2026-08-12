package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/middleware"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// ModelMgmtHandler serves tenant-scoped model management endpoints.
type ModelMgmtHandler struct {
	svc *llmapp.ModelMgmtService
}

// NewModelMgmtHandler creates a ModelMgmtHandler.
func NewModelMgmtHandler(svc *llmapp.ModelMgmtService) *ModelMgmtHandler {
	return &ModelMgmtHandler{svc: svc}
}

// List GET /admin/models — returns models matching optional filter criteria.
func (h *ModelMgmtHandler) List(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var filter port.ModelFilter
	if cap := c.Query("capability"); cap != "" {
		filter.Capability = domain.ModelCapability(cap)
	}
	if pid := c.Query("providerId"); pid != "" {
		filter.ProviderID = pid
	}
	models, err := h.svc.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

// Get GET /admin/models/:id — returns a single model by ID.
func (h *ModelMgmtHandler) Get(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	m, err := h.svc.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, m)
}

// Update PUT /admin/models/:id — updates a model's display and pricing fields.
func (h *ModelMgmtHandler) Update(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var input llmapp.UpdateModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	input.ID = c.Param("id")
	m, err := h.svc.Update(c.Request.Context(), tenantID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, m)
}

// Toggle PATCH /admin/models/:id/toggle — enables or disables a model.
func (h *ModelMgmtHandler) Toggle(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.Toggle(c.Request.Context(), tenantID, c.Param("id"), req.Enabled); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已更新"})
}

// SetDefaultEmbedding PUT /admin/models/:id/default-embedding — 设置/取消默认嵌入模型。
func (h *ModelMgmtHandler) SetDefaultEmbedding(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.SetDefaultEmbedding(c.Request.Context(), tenantID, c.Param("id"), req.Enabled); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已更新"})
}

// Delete DELETE /admin/models/:id — removes a model.
func (h *ModelMgmtHandler) Delete(c *gin.Context) {
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
