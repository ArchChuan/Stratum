package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
)

var errInvalidInput = errors.New("invalid user")

type userMemorySvc interface {
	ClearUserMemories(ctx context.Context, req *application.ClearUserMemoriesRequest) error
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
	var entry application.MemoryEntry
	if err := c.ShouldBindJSON(&entry); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	entry.TenantID = tenantID
	ctx := application.WithTenantContext(c.Request.Context(), tenantID)
	if err := h.mgr.Add(ctx, &entry); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, entry)
}

func (h *UserMemoryHandler) GetMemory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	ctx := application.WithTenantContext(c.Request.Context(), tenantID)
	entry, err := h.mgr.Get(ctx, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, entry)
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
	ctx := application.WithTenantContext(c.Request.Context(), tenantID)
	if err := h.mgr.Delete(ctx, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
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
