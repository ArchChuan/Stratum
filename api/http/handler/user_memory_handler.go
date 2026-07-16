package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
)

var errInvalidInput = errors.New("invalid user")

type userMemorySvc interface {
	ClearUserMemories(ctx context.Context, req *application.ClearUserMemoriesRequest) error
	CreateUserMemory(ctx context.Context, req *application.CreateUserMemoryRequest) (*application.UserMemory, error)
	GetUserMemory(ctx context.Context, req *application.GetUserMemoryRequest) (*application.UserMemory, error)
	ForgetUserMemory(ctx context.Context, req *application.ForgetMemoryRequest) error
}

type memoryMgrSvc interface {
	Add(ctx context.Context, entry *application.MemoryEntry) error
	Get(ctx context.Context, id string) (*application.MemoryEntry, error)
	Delete(ctx context.Context, id string) error
	Clear(ctx context.Context, sessionCtx *application.SessionContext) error
	GetStats(ctx context.Context, sessionCtx *application.SessionContext) (*application.MemoryStats, error)
	GetSummary(ctx context.Context, sessionCtx *application.SessionContext) (string, error)
}

type UserMemoryHandler struct {
	svc userMemorySvc
	mgr memoryMgrSvc
}

func NewUserMemoryHandler(svc userMemorySvc, mgr memoryMgrSvc) *UserMemoryHandler {
	return &UserMemoryHandler{svc: svc, mgr: mgr}
}

func (h *UserMemoryHandler) ClearMemories(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
		return
	}
	if err := h.svc.ClearUserMemories(c.Request.Context(), &application.ClearUserMemoriesRequest{
		TenantID: tenantID,
		UserID:   userID,
	}); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserMemoryHandler) AddMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
		return
	}
	var req dto.CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	memory, err := h.svc.CreateUserMemory(c.Request.Context(), &application.CreateUserMemoryRequest{
		TenantID: tenantID, UserID: userID, AgentID: req.AgentID, ConversationID: req.ConversationID,
		Content: req.Content, Importance: req.Importance, EntityNames: req.EntityNames,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, memoryFactResponse(memory))
}

func (h *UserMemoryHandler) GetMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
		return
	}
	memory, err := h.svc.GetUserMemory(c.Request.Context(), &application.GetUserMemoryRequest{
		TenantID: tenantID, UserID: userID, FactID: c.Param("id"),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, memoryFactResponse(memory))
}

func (h *UserMemoryHandler) ListSessions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"sessions": []any{}})
}

func (h *UserMemoryHandler) GetStats(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	stats, err := h.mgr.GetStats(c.Request.Context(), &application.SessionContext{TenantID: tenantID})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *UserMemoryHandler) GetSummary(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	summary, err := h.mgr.GetSummary(c.Request.Context(), &application.SessionContext{
		TenantID:  tenantID,
		SessionID: c.Param("session_id"),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"summary": summary})
}

func (h *UserMemoryHandler) DeleteMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errInvalidInput))
		return
	}
	if err := h.svc.ForgetUserMemory(c.Request.Context(), &application.ForgetMemoryRequest{
		TenantID: tenantID, UserID: userID, FactID: c.Param("id"),
	}); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

func memoryFactResponse(memory *application.UserMemory) dto.MemoryFactResponse {
	return dto.MemoryFactResponse{
		ID: memory.ID, AgentID: memory.AgentID, ConversationID: memory.ConversationID,
		Scope: memory.Scope, Content: memory.Content, Importance: memory.Importance,
		EntityNames: memory.EntityNames, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt,
	}
}

func (h *UserMemoryHandler) ClearSession(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if err := h.mgr.Clear(c.Request.Context(), &application.SessionContext{
		TenantID:  tenantID,
		SessionID: c.Param("session_id"),
	}); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
