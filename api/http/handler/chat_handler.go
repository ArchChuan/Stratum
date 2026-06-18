package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ChatHandler handles conversation and message endpoints.
type ChatHandler struct {
	store  agent.ChatStore
	logger *zap.Logger
}

func NewChatHandler(store agent.ChatStore, logger *zap.Logger) *ChatHandler {
	return &ChatHandler{store: store, logger: logger}
}

// POST /agents/:id/conversations
func (h *ChatHandler) CreateConversation(c *gin.Context) {
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
	agentID := c.Param("id")

	var req struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&req)
	name := req.Name
	if name == "" {
		name = "新会话"
	}

	conv, err := h.store.CreateConversation(c.Request.Context(), tenantID, agentID, userID, name)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, conversationResponse(conv))
}

// GET /agents/:id/conversations
func (h *ChatHandler) ListConversations(c *gin.Context) {
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
	agentID := c.Param("id")

	convs, err := h.store.ListConversations(c.Request.Context(), tenantID, agentID, userID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]gin.H, 0, len(convs))
	for _, cv := range convs {
		out = append(out, conversationResponse(cv))
	}
	c.JSON(http.StatusOK, gin.H{"conversations": out})
}

// PATCH /conversations/:convID
func (h *ChatHandler) RenameConversation(c *gin.Context) {
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
	convID := c.Param("convID")

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("name 不能为空")))
		return
	}

	if err := h.store.RenameConversation(c.Request.Context(), tenantID, convID, userID, req.Name); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DELETE /conversations/:convID
func (h *ChatHandler) DeleteConversation(c *gin.Context) {
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
	convID := c.Param("convID")

	if err := h.store.DeleteConversation(c.Request.Context(), tenantID, convID, userID); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /conversations/:convID/messages
func (h *ChatHandler) ListMessages(c *gin.Context) {
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
	convID := c.Param("convID")

	msgs, err := h.store.ListMessages(c.Request.Context(), tenantID, convID, userID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]gin.H, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageResponse(m))
	}
	c.JSON(http.StatusOK, gin.H{"messages": out})
}

// POST /conversations/:convID/messages
func (h *ChatHandler) AddMessage(c *gin.Context) {
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
	convID := c.Param("convID")

	var req struct {
		Role    string `json:"role"    binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("role 和 content 必填")))
		return
	}
	if req.Role != "user" && req.Role != "agent" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("role 必须为 user 或 agent")))
		return
	}

	// Verify the conversation belongs to the calling user before writing.
	convs, err := h.store.ListMessages(c.Request.Context(), tenantID, convID, userID)
	_ = convs
	if err != nil {
		// ListMessages enforces ownership via JOIN; any DB error means forbidden-or-missing.
		h.logger.Error("add message: ownership check", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("会话不存在或无权操作")))
		return
	}

	msg := &agent.ChatMessage{
		ConversationID: convID,
		Role:           req.Role,
		Content:        req.Content,
	}
	if err := h.store.AddMessage(c.Request.Context(), tenantID, msg); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, messageResponse(msg))
}

// --- helpers ---

func conversationResponse(cv *agent.ChatConversation) gin.H {
	return gin.H{
		"id":         cv.ID,
		"agent_id":   cv.AgentID,
		"user_id":    cv.UserID,
		"name":       cv.Name,
		"created_at": cv.CreatedAt,
		"updated_at": cv.UpdatedAt,
		"expires_at": cv.ExpiresAt,
	}
}

func messageResponse(m *agent.ChatMessage) gin.H {
	steps := m.StepsJSON
	if steps == nil {
		steps = []byte("[]")
	}
	return gin.H{
		"id":              m.ID,
		"conversation_id": m.ConversationID,
		"role":            m.Role,
		"content":         m.Content,
		"steps":           steps,
		"is_error":        m.IsError,
		"created_at":      m.CreatedAt,
	}
}
