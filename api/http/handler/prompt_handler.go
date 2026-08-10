package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/prompt/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// PromptHandler serves the prompt registry REST endpoints.
type PromptHandler struct {
	registry *application.RegistryService
	ab       *application.ABService
	logger   *zap.Logger
}

// NewPromptHandler creates a prompt HTTP handler.
func NewPromptHandler(registry *application.RegistryService, ab *application.ABService, logger *zap.Logger) *PromptHandler {
	return &PromptHandler{registry: registry, ab: ab, logger: logger}
}

// CreatePrompt godoc
// POST /v1/prompts
func (h *PromptHandler) CreatePrompt(c *gin.Context) {
	var req struct {
		Key     string `json:"key" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	userID, _ := c.Get("auth.sub")
	tenantID, _ := c.Get("auth.tenant_id")
	var tid *string
	if v, ok := tenantID.(string); ok && v != "" {
		tid = &v
	}
	tmpl, err := h.registry.CreateTemplate(c.Request.Context(), req.Key, tid, req.Content, "user:"+userID.(string))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, tmpl)
}

// ListPrompts godoc
// GET /v1/prompts?page=&page_size=
// Returns the latest version of every prompt key (admin).
func (h *PromptHandler) ListPrompts(c *gin.Context) {
	tenantID, _ := c.Get("auth.tenant_id")
	var tid *string
	if v, ok := tenantID.(string); ok && v != "" {
		tid = &v
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	tmpls, total, err := h.registry.ListTemplates(c.Request.Context(), tid, page, pageSize)
	if err != nil {
		_ = c.Error(err)
		return
	}
	prompts := make([]gin.H, 0, len(tmpls))
	for _, t := range tmpls {
		prompts = append(prompts, gin.H{
			"key":            t.Key,
			"latest_version": t.Version,
			"latest_status":  t.Status,
			"created_at":     t.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"prompts": prompts, "total": total})
}

// ListVersions godoc
// GET /v1/prompts/:key/versions
func (h *PromptHandler) ListVersions(c *gin.Context) {
	key := c.Param("key")
	tenantID, _ := c.Get("auth.tenant_id")
	var tid *string
	if v, ok := tenantID.(string); ok && v != "" {
		tid = &v
	}
	versions, err := h.registry.GetVersions(c.Request.Context(), key, tid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// PublishVersion godoc
// POST /v1/prompts/:key/versions/:version/publish
func (h *PromptHandler) PublishVersion(c *gin.Context) {
	key := c.Param("key")
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("invalid version")))
		return
	}
	tenantID, _ := c.Get("auth.tenant_id")
	var tid *string
	if v, ok := tenantID.(string); ok && v != "" {
		tid = &v
	}
	if err := h.registry.PublishVersion(c.Request.Context(), key, version, tid); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "published"})
}

// ListBindings godoc
// GET /v1/prompts/bindings
// Returns all A/B bindings (admin).
func (h *PromptHandler) ListBindings(c *gin.Context) {
	bindings, err := h.ab.ListBindings(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bindings": bindings})
}

// UpsertBinding godoc
// PUT /v1/prompts/bindings
func (h *PromptHandler) UpsertBinding(c *gin.Context) {
	var req struct {
		Key             string `json:"key" binding:"required"`
		Scope           string `json:"scope" binding:"required"`
		StableVersionID string `json:"stable_version_id" binding:"required"`
		CanaryVersionID string `json:"canary_version_id"`
		TrafficPercent  int    `json:"traffic_percent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.ab.BindExperiment(c.Request.Context(),
		req.Key, req.Scope, req.StableVersionID, req.CanaryVersionID, req.TrafficPercent,
	); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// DeleteBinding godoc
// DELETE /v1/prompts/bindings/:key/:scope
func (h *PromptHandler) DeleteBinding(c *gin.Context) {
	key := c.Param("key")
	scope := c.Param("scope")
	if err := h.ab.ClearExperiment(c.Request.Context(), key, scope); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
