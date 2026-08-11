package handler

import (
	"encoding/json"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	paramapp "github.com/byteBuilderX/stratum/internal/parameters/application"
	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ParameterHandler exposes the unified parameter registry under /admin/parameters.
type ParameterHandler struct {
	svc    *paramapp.Service
	logger *zap.Logger
}

func NewParameterHandler(svc *paramapp.Service, logger *zap.Logger) *ParameterHandler {
	return &ParameterHandler{svc: svc, logger: logger}
}

// Schema GET /admin/parameters/schema — all definitions for schema-driven
// frontend rendering (value layer stays separate).
func (h *ParameterHandler) Schema(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Schema())
}

// List GET /admin/parameters — current effective platform-layer values.
func (h *ParameterHandler) List(c *gin.Context) {
	values, err := h.svc.PlatformValues(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, values)
}

// Update PUT /admin/parameters — merge-write platform values. Only keys
// present in the body are touched; every key must be platform-scope and pass
// its definition validation.
func (h *ParameterHandler) Update(c *gin.Context) {
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return
	}

	values := make(map[string]any, len(body))
	for key, raw := range body {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid value for " + key})
			return
		}
		values[key] = value
	}

	updatedBy := c.GetString(middleware.ContextKeySub)
	if err := h.svc.SetPlatformValues(c.Request.Context(), values, updatedBy); err != nil {
		var bad *domain.ErrInvalidParameter
		if ok := domain.AsInvalidParameter(err, &bad); ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": bad.Error()})
			return
		}
		_ = c.Error(err)
		return
	}
	updated, err := h.svc.PlatformValues(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, updated)
}
