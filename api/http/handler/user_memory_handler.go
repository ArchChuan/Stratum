package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
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
	ListUserMemories(ctx context.Context, req *application.ListUserMemoriesRequest) ([]*application.UserMemory, int, error)
}

type memoryMgrSvc interface {
	Add(ctx context.Context, entry *application.MemoryEntry) error
	Get(ctx context.Context, id string) (*application.MemoryEntry, error)
	Delete(ctx context.Context, id string) error
	Clear(ctx context.Context, sessionCtx *application.SessionContext) error
	GetStats(ctx context.Context, sessionCtx *application.SessionContext) (*application.MemoryStats, error)
	GetSummary(ctx context.Context, sessionCtx *application.SessionContext) (string, error)
}

// DefaultEmbedModelResolver resolves the tenant's default embedding model name;
// implemented by llmgateway.ModelRegistry and injected via wiring. nil-safe:
// 解析失败或无可用模型时 GetStats 返回 embed_model_configured=false。
type DefaultEmbedModelResolver interface {
	ResolveDefaultEmbeddingModel(ctx context.Context, tenantID string) (string, error)
}

type UserMemoryHandler struct {
	svc      userMemorySvc
	mgr      memoryMgrSvc
	embedSvc DefaultEmbedModelResolver
}

func NewUserMemoryHandler(svc userMemorySvc, mgr memoryMgrSvc, embedSvc DefaultEmbedModelResolver) *UserMemoryHandler {
	return &UserMemoryHandler{svc: svc, mgr: mgr, embedSvc: embedSvc}
}

// embedModelConfigured reports whether the tenant has a usable default
// embedding model; resolver nil or error → false（fail-closed，永不 panic）。
func (h *UserMemoryHandler) embedModelConfigured(ctx context.Context, tenantID string) bool {
	if h.embedSvc == nil {
		return false
	}
	model, err := h.embedSvc.ResolveDefaultEmbeddingModel(ctx, tenantID)
	if err != nil {
		return false
	}
	return model != ""
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
	var req gen.CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	memory, err := h.svc.CreateUserMemory(c.Request.Context(), &application.CreateUserMemoryRequest{
		TenantID: tenantID, UserID: userID, Content: req.Content, Importance: req.Importance,
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
	c.JSON(http.StatusOK, gen.MemorySessionsResponse{Sessions: []string{}})
}

// ListMemories godoc
// GET /memory?page=&page_size=
// Returns the authenticated user's active memories, newest first (member).
func (h *UserMemoryHandler) ListMemories(c *gin.Context) {
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
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	memories, total, err := h.svc.ListUserMemories(c.Request.Context(), &application.ListUserMemoriesRequest{
		TenantID: tenantID, UserID: userID, Limit: pageSize, Offset: (page - 1) * pageSize,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemoryFactResponse, 0, len(memories))
	for _, memory := range memories {
		resp = append(resp, memoryFactResponse(memory))
	}
	c.JSON(http.StatusOK, gin.H{"memories": resp, "total": total})
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
	c.JSON(http.StatusOK, gen.MemoryStatsResponse{
		TotalEntries: stats.TotalEntries, ShortTermCount: stats.ShortTermCount,
		LongTermCount: stats.LongTermCount, EntityCount: stats.EntityCount,
		SessionsCount: stats.SessionsCount, ActiveUsers: stats.ActiveUsers,
		VectorCount: stats.VectorCount, LastAccessTime: stats.LastAccessTime,
		StorageSizeBytes:     stats.StorageSizeBytes,
		EmbedModelConfigured: h.embedModelConfigured(c.Request.Context(), tenantID),
	})
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
	c.JSON(http.StatusOK, gen.MemorySummaryResponse{Summary: summary})
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

func memoryFactResponse(memory *application.UserMemory) gen.MemoryFactResponse {
	return gen.MemoryFactResponse{
		ID: memory.ID, Scope: memory.Scope, Content: memory.Content,
		Importance: memory.Importance, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt,
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
