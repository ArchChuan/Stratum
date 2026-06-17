package handler

import (
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetEntities retrieves entities for a session.
func (h *MemoryHandler) GetEntities(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errUnauthorized))
		return
	}
	sessionID := c.Query("session_id")

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}

	ctx := c.Request.Context()
	entities, err := h.manager.GetEntities(ctx, sessionCtx)
	if err != nil {
		h.logger.Error("failed to get entities",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	responses := make([]dto.EntityResponse, 0, len(entities))
	for _, e := range entities {
		responses = append(responses, toEntityResponse(e))
	}

	c.JSON(http.StatusOK, gin.H{
		"entities": responses,
		"count":    len(responses),
	})
}

// ExtractEntities extracts entities from text.
func (h *MemoryHandler) ExtractEntities(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errUnauthorized))
		return
	}

	var req dto.ExtractEntitiesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid extract entities request",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: req.SessionID,
		Metadata:  req.Metadata,
	}

	ctx := c.Request.Context()
	entities, err := h.manager.ExtractEntities(ctx, req.Text, sessionCtx)
	if err != nil {
		h.logger.Error("failed to extract entities",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	responses := make([]dto.EntityResponse, 0, len(entities))
	for _, e := range entities {
		resp := toEntityResponse(e)
		resp.Relations = []dto.EntityRelationItem{}
		responses = append(responses, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"entities": responses,
		"count":    len(responses),
	})
}
