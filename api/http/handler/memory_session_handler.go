package handler

import (
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CreateSession creates a new conversation session.
func (h *MemoryHandler) CreateSession(c *gin.Context) {
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

	var req dto.CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid create session request",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	sessionID := uuid.New().String()
	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
		AgentID:   req.AgentID,
		StartTime: time.Now(),
		Metadata:  req.Metadata,
	}

	ctx := c.Request.Context()
	if _, err := h.manager.GetStats(ctx, sessionCtx); err != nil {
		h.logger.Error("failed to initialize session",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	h.logger.Info("session created",
		zap.String("trace_id", middleware.GetTraceID(c)),
		zap.String("session_id", sessionID),
		zap.String("tenant_id", tenantID),
		zap.String("user_id", userID))

	c.JSON(http.StatusCreated, dto.CreateSessionResponse{
		SessionID: sessionID,
		TenantID:  tenantID,
		UserID:    userID,
		AgentID:   req.AgentID,
		StartTime: sessionCtx.StartTime.Format(time.RFC3339),
	})
}

// ClearSession clears all memory entries for a session.
func (h *MemoryHandler) ClearSession(c *gin.Context) {
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
	if err := h.manager.Clear(ctx, sessionCtx); err != nil {
		h.logger.Error("failed to clear session",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	h.logger.Info("session cleared",
		zap.String("trace_id", middleware.GetTraceID(c)),
		zap.String("session_id", sessionID))
	c.JSON(http.StatusOK, gin.H{"message": "session cleared successfully"})
}

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
