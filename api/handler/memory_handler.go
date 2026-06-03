// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/memory"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type MemoryHandler struct {
	manager *memory.MemoryManager
	logger  *zap.Logger
}

type CreateSessionRequest struct {
	TenantID string                 `json:"tenant_id" binding:"required"`
	UserID   string                 `json:"user_id" binding:"required"`
	AgentID  string                 `json:"agent_id"`
	Metadata map[string]interface{} `json:"metadata"`
}

type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
	TenantID  string `json:"tenant_id"`
	UserID    string `json:"user_id"`
	AgentID   string `json:"agent_id"`
	StartTime string `json:"start_time"`
}

type AddMemoryRequest struct {
	Role       string                 `json:"role" binding:"required,oneof=user assistant system"`
	Content    string                 `json:"content" binding:"required"`
	SessionID  string                 `json:"session_id" binding:"required"`
	TenantID   string                 `json:"tenant_id" binding:"required"`
	UserID     string                 `json:"user_id" binding:"required"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Tags       []string               `json:"tags"`
	Importance float64                `json:"importance"`
	ExpiresAt  string                 `json:"expires_at"`
}

type MemoryEntryResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Content    string                 `json:"content"`
	Timestamp  string                 `json:"timestamp"`
	TenantID   string                 `json:"tenant_id"`
	UserID     string                 `json:"user_id"`
	SessionID  string                 `json:"session_id"`
	AgentID    string                 `json:"agent_id"`
	Metadata   map[string]interface{} `json:"metadata"`
	Tags       []string               `json:"tags"`
	Importance float64                `json:"importance"`
	ExpiresAt  string                 `json:"expires_at,omitempty"`
}

type SearchMemoryRequest struct {
	Query     string                 `json:"query"`
	SessionID string                 `json:"session_id"`
	TenantID  string                 `json:"tenant_id"`
	UserID    string                 `json:"user_id"`
	Types     []string               `json:"types"`
	Limit     int                    `json:"limit"`
	MinScore  float64                `json:"min_score"`
	Filters   map[string]interface{} `json:"filters"`
}

type SearchMemoryResponse struct {
	Results []*MemorySearchResultItem `json:"results"`
	Count   int                       `json:"count"`
}

type MemorySearchResultItem struct {
	Entry    *MemoryEntryResponse `json:"entry"`
	Score    float64              `json:"score"`
	Distance float64              `json:"distance,omitempty"`
}

type MemoryStatsResponse struct {
	TotalEntries     int64  `json:"total_entries"`
	ShortTermCount   int64  `json:"short_term_count"`
	LongTermCount    int64  `json:"long_term_count"`
	EntityCount      int64  `json:"entity_count"`
	SessionsCount    int64  `json:"sessions_count"`
	ActiveUsers      int64  `json:"active_users"`
	VectorCount      int64  `json:"vector_count"`
	LastAccessTime   string `json:"last_access_time"`
	StorageSizeBytes int64  `json:"storage_size_bytes"`
}

type EntityResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Confidence float64                `json:"confidence"`
	FirstSeen  string                 `json:"first_seen"`
	LastSeen   string                 `json:"last_seen"`
	Attributes map[string]interface{} `json:"attributes"`
	Relations  []EntityRelationItem   `json:"relations"`
}

type EntityRelationItem struct {
	FromEntityID string                 `json:"from_entity_id"`
	ToEntityID   string                 `json:"to_entity_id"`
	RelationType string                 `json:"relation_type"`
	Confidence   float64                `json:"confidence"`
	LastSeen     string                 `json:"last_seen"`
	Metadata     map[string]interface{} `json:"metadata"`
}

func NewMemoryHandler(manager *memory.MemoryManager, logger *zap.Logger) *MemoryHandler {
	return &MemoryHandler{
		manager: manager,
		logger:  logger,
	}
}

// CreateSession creates a new conversation session
func (h *MemoryHandler) CreateSession(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid create session request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	sessionID := uuid.New().String()
	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    req.UserID,
		SessionID: sessionID,
		AgentID:   req.AgentID,
		StartTime: time.Now(),
		Metadata:  req.Metadata,
	}

	ctx := c.Request.Context()
	if _, err := h.manager.GetStats(ctx, sessionCtx); err != nil {
		h.logger.Error("failed to initialize session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to create session",
		})
		return
	}

	h.logger.Info("session created",
		zap.String("session_id", sessionID),
		zap.String("tenant_id", req.TenantID),
		zap.String("user_id", req.UserID))

	c.JSON(http.StatusCreated, CreateSessionResponse{
		SessionID: sessionID,
		TenantID:  req.TenantID,
		UserID:    req.UserID,
		AgentID:   req.AgentID,
		StartTime: sessionCtx.StartTime.Format(time.RFC3339),
	})
}

// AddMemory adds a memory entry
func (h *MemoryHandler) AddMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	var req AddMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid add memory request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	entry := &memory.MemoryEntry{
		ID:         uuid.New().String(),
		Role:       req.Role,
		Content:    req.Content,
		Timestamp:  time.Now(),
		TenantID:   tenantID,
		UserID:     req.UserID,
		SessionID:  req.SessionID,
		AgentID:    req.AgentID,
		Metadata:   req.Metadata,
		Tags:       req.Tags,
		Importance: req.Importance,
	}

	if req.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpiresAt); err == nil {
			entry.ExpiresAt = t
		}
	}

	ctx := c.Request.Context()
	if err := h.manager.Add(ctx, entry); err != nil {
		h.logger.Error("failed to add memory entry", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to add memory entry",
		})
		return
	}

	h.logger.Info("memory entry added",
		zap.String("id", entry.ID),
		zap.String("session_id", req.SessionID))

	c.JSON(http.StatusCreated, MemoryEntryResponse{
		ID:         entry.ID,
		Type:       string(entry.Type),
		Role:       entry.Role,
		Content:    entry.Content,
		Timestamp:  entry.Timestamp.Format(time.RFC3339),
		TenantID:   entry.TenantID,
		UserID:     entry.UserID,
		SessionID:  entry.SessionID,
		AgentID:    entry.AgentID,
		Metadata:   entry.Metadata,
		Tags:       entry.Tags,
		Importance: entry.Importance,
		ExpiresAt:  entry.ExpiresAt.Format(time.RFC3339),
	})
}

// GetMemory retrieves a memory entry by ID
func (h *MemoryHandler) GetMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	ctx := c.Request.Context()
	entry, err := h.manager.Get(ctx, id)
	if err != nil {
		h.logger.Warn("memory entry not found", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "memory entry not found",
		})
		return
	}

	if entry.TenantID != tenantID {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "memory entry not found",
		})
		return
	}

	c.JSON(http.StatusOK, MemoryEntryResponse{
		ID:         entry.ID,
		Type:       string(entry.Type),
		Role:       entry.Role,
		Content:    entry.Content,
		Timestamp:  entry.Timestamp.Format(time.RFC3339),
		TenantID:   entry.TenantID,
		UserID:     entry.UserID,
		SessionID:  entry.SessionID,
		AgentID:    entry.AgentID,
		Metadata:   entry.Metadata,
		Tags:       entry.Tags,
		Importance: entry.Importance,
		ExpiresAt:  entry.ExpiresAt.Format(time.RFC3339),
	})
}

// SearchMemory searches memory entries
func (h *MemoryHandler) SearchMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	var req SearchMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid search memory request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	// Convert types
	memoryTypes := make([]memory.MemoryType, 0, len(req.Types))
	for _, t := range req.Types {
		memoryTypes = append(memoryTypes, memory.MemoryType(t))
	}

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    req.UserID,
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
		h.logger.Error("failed to search memory", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to search memory",
		})
		return
	}

	items := make([]*MemorySearchResultItem, 0, len(results))
	for _, r := range results {
		items = append(items, &MemorySearchResultItem{
			Entry: &MemoryEntryResponse{
				ID:         r.Entry.ID,
				Type:       string(r.Entry.Type),
				Role:       r.Entry.Role,
				Content:    r.Entry.Content,
				Timestamp:  r.Entry.Timestamp.Format(time.RFC3339),
				TenantID:   r.Entry.TenantID,
				UserID:     r.Entry.UserID,
				SessionID:  r.Entry.SessionID,
				AgentID:    r.Entry.AgentID,
				Metadata:   r.Entry.Metadata,
				Tags:       r.Entry.Tags,
				Importance: r.Entry.Importance,
				ExpiresAt:  r.Entry.ExpiresAt.Format(time.RFC3339),
			},
			Score:    r.Score,
			Distance: r.Distance,
		})
	}

	h.logger.Info("memory search completed",
		zap.String("query", req.Query),
		zap.Int("results", len(items)))

	c.JSON(http.StatusOK, SearchMemoryResponse{
		Results: items,
		Count:   len(items),
	})
}

// DeleteMemory deletes a memory entry
func (h *MemoryHandler) DeleteMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")

	ctx := c.Request.Context()
	entry, err := h.manager.Get(ctx, id)
	if err != nil || entry.TenantID != tenantID {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "memory entry not found",
		})
		return
	}

	if err := h.manager.Delete(ctx, id); err != nil {
		h.logger.Warn("failed to delete memory entry", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Code:    http.StatusNotFound,
			Message: "memory entry not found",
		})
		return
	}

	h.logger.Info("memory entry deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{
		"message": "memory entry deleted successfully",
	})
}

// GetStats retrieves memory statistics
func (h *MemoryHandler) GetStats(c *gin.Context) {
	tenantID, _ := tenantIDFromCtx(c)
	var sessionCtx *memory.SessionContext
	if tenantID != "" {
		sessionCtx = &memory.SessionContext{
			TenantID:  tenantID,
			UserID:    c.Query("user_id"),
			SessionID: c.Query("session_id"),
		}
	}

	ctx := c.Request.Context()
	stats, err := h.manager.GetStats(ctx, sessionCtx)
	if err != nil {
		h.logger.Error("failed to get memory stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to get memory stats",
		})
		return
	}

	c.JSON(http.StatusOK, MemoryStatsResponse{
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

// ClearSession clears all memory entries for a session
func (h *MemoryHandler) ClearSession(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	sessionID := c.Param("session_id")
	userID := c.Query("user_id")

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}

	ctx := c.Request.Context()
	if err := h.manager.Clear(ctx, sessionCtx); err != nil {
		h.logger.Error("failed to clear session", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to clear session",
		})
		return
	}

	h.logger.Info("session cleared", zap.String("session_id", sessionID))
	c.JSON(http.StatusOK, gin.H{
		"message": "session cleared successfully",
	})
}

// GetEntities retrieves entities for a session
func (h *MemoryHandler) GetEntities(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID := c.Query("user_id")
	sessionID := c.Query("session_id")

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}

	ctx := c.Request.Context()
	entities, err := h.manager.GetEntities(ctx, sessionCtx)
	if err != nil {
		h.logger.Error("failed to get entities", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to get entities",
		})
		return
	}

	responses := make([]EntityResponse, 0, len(entities))
	for _, e := range entities {
		relations := make([]EntityRelationItem, 0, len(e.Relations))
		for _, r := range e.Relations {
			relations = append(relations, EntityRelationItem{
				FromEntityID: r.FromEntityID,
				ToEntityID:   r.ToEntityID,
				RelationType: r.RelationType,
				Confidence:   r.Confidence,
				LastSeen:     r.LastSeen.Format(time.RFC3339),
				Metadata:     r.Metadata,
			})
		}

		responses = append(responses, EntityResponse{
			ID:         e.ID,
			Name:       e.Name,
			Type:       e.Type,
			Confidence: e.Confidence,
			FirstSeen:  e.FirstSeen.Format(time.RFC3339),
			LastSeen:   e.LastSeen.Format(time.RFC3339),
			Attributes: e.Attributes,
			Relations:  relations,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"entities": responses,
		"count":    len(responses),
	})
}

// ExtractEntities extracts entities from text
func (h *MemoryHandler) ExtractEntities(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}

	var req struct {
		Text      string                 `json:"text" binding:"required"`
		SessionID string                 `json:"session_id" binding:"required"`
		UserID    string                 `json:"user_id" binding:"required"`
		Metadata  map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid extract entities request", zap.Error(err))
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		})
		return
	}

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Metadata:  req.Metadata,
	}

	ctx := c.Request.Context()
	entities, err := h.manager.ExtractEntities(ctx, req.Text, sessionCtx)
	if err != nil {
		h.logger.Error("failed to extract entities", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to extract entities",
		})
		return
	}

	responses := make([]EntityResponse, 0, len(entities))
	for _, e := range entities {
		responses = append(responses, EntityResponse{
			ID:         e.ID,
			Name:       e.Name,
			Type:       e.Type,
			Confidence: e.Confidence,
			FirstSeen:  e.FirstSeen.Format(time.RFC3339),
			LastSeen:   e.LastSeen.Format(time.RFC3339),
			Attributes: e.Attributes,
			Relations:  []EntityRelationItem{},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"entities": responses,
		"count":    len(responses),
	})
}

// GetSummary retrieves conversation summary
func (h *MemoryHandler) GetSummary(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	sessionID := c.Param("session_id")
	userID := c.Query("user_id")

	sessionCtx := &memory.SessionContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}

	ctx := c.Request.Context()
	summary, err := h.manager.GetSummary(ctx, sessionCtx)
	if err != nil {
		h.logger.Error("failed to get summary", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Code:    http.StatusInternalServerError,
			Message: "failed to get summary",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"summary":    summary,
	})
}
