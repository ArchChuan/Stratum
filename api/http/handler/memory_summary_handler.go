package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	memdomain "github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetSummary retrieves the conversation summary for a session.
func (h *MemoryHandler) GetSummary(c *gin.Context) {
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
	sessionID := c.Param("session_id")

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}

	ctx := c.Request.Context()
	summary, err := h.manager.GetSummary(ctx, sessionCtx)
	if err != nil {
		// Empty summary is the contracted shape when no entries exist for
		// the session — the absence of a summary is not a 404 to clients.
		if errors.Is(err, memdomain.ErrSessionNotFound) {
			c.JSON(http.StatusOK, gin.H{"session_id": sessionID, "summary": ""})
			return
		}
		h.logger.Error("failed to get summary",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"summary":    summary,
	})
}
