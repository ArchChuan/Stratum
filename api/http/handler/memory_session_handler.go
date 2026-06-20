package handler

import (
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetStats retrieves memory statistics.
func (h *MemoryHandler) GetStats(c *gin.Context) {
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
	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: c.Query("session_id"),
	}

	ctx := c.Request.Context()
	stats, err := h.manager.GetStats(ctx, sessionCtx)
	if err != nil {
		h.logger.Error("failed to get memory stats",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, dto.MemoryStatsResponse{
		TotalEntries:     stats.TotalEntries,
		ShortTermCount:   stats.ShortTermCount,
		LongTermCount:    stats.LongTermCount,
		EntityCount:      stats.EntityCount,
		SessionsCount:    stats.SessionsCount,
		ActiveUsers:      stats.ActiveUsers,
		VectorCount:      stats.VectorCount,
		LastAccessTime:   stats.LastAccessTime.Format(time.RFC3339),
		StorageSizeBytes: stats.StorageSizeBytes,
	})
}
