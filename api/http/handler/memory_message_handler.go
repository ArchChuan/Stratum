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

// GetMemory retrieves a memory entry by ID.
func (h *MemoryHandler) GetMemory(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	ctx := c.Request.Context()
	entry, err := h.manager.Get(ctx, id)
	if err != nil {
		h.logger.Warn("memory entry lookup failed",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("id", id),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, toMemoryEntryResponse(entry))
}

// SearchMemory searches memory entries.
func (h *MemoryHandler) SearchMemory(c *gin.Context) {
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

	var req dto.SearchMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid search memory request",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	memoryTypes := make([]memory.MemoryType, 0, len(req.Types))
	for _, t := range req.Types {
		memoryTypes = append(memoryTypes, memory.MemoryType(t))
	}

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: req.SessionID,
	}

	searchReq := &memory.MemorySearchRequest{
		Query:    req.Query,
		Context:  sessionCtx,
		Types:    memoryTypes,
		Limit:    req.Limit,
		MinScore: req.MinScore,
		Filters:  req.Filters,
	}

	if searchReq.Limit <= 0 {
		searchReq.Limit = 10
	}

	ctx := c.Request.Context()
	results, err := h.manager.Search(ctx, searchReq)
	if err != nil {
		h.logger.Error("failed to search memory",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	items := make([]*dto.MemorySearchResultItem, 0, len(results))
	for _, r := range results {
		entry := toMemoryEntryResponse(r.Entry)
		items = append(items, &dto.MemorySearchResultItem{
			Entry:    &entry,
			Score:    r.Score,
			Distance: r.Distance,
		})
	}

	c.JSON(http.StatusOK, dto.SearchMemoryResponse{
		Results: items,
		Count:   len(items),
	})
}

// DeleteMemory deletes a memory entry.
func (h *MemoryHandler) DeleteMemory(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	ctx := c.Request.Context()
	if _, err := h.manager.Get(ctx, id); err != nil {
		_ = c.Error(err)
		return
	}

	if err := h.manager.Delete(ctx, id); err != nil {
		h.logger.Warn("failed to delete memory entry",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("id", id),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "memory entry deleted successfully"})
}

// AddMemory adds a new memory entry.
func (h *MemoryHandler) AddMemory(c *gin.Context) {
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

	var req dto.AddMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	entry := &memory.MemoryEntry{
		ID:         uuid.New().String(),
		Type:       memory.MemoryType(req.Type),
		Role:       req.Role,
		Content:    req.Content,
		TenantID:   tenantID,
		UserID:     userID,
		SessionID:  req.SessionID,
		AgentID:    req.AgentID,
		Importance: req.Importance,
		Tags:       req.Tags,
		Metadata:   req.Metadata,
		Timestamp:  time.Now(),
	}

	ctx := c.Request.Context()
	if err := h.manager.Add(ctx, entry); err != nil {
		h.logger.Error("failed to add memory entry",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusCreated, toMemoryEntryResponse(entry))
}

// CreateSession creates a new memory session and returns its ID.
func (h *MemoryHandler) CreateSession(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	if _, ok := userIDFromCtx(c); !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errUnauthorized))
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session_id": uuid.New().String()})
}

// DeleteSession clears all memory for a session.
func (h *MemoryHandler) DeleteSession(c *gin.Context) {
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
		h.logger.Warn("failed to clear memory session",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("session_id", sessionID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session cleared"})
}
