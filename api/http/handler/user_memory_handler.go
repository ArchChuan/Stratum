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
	ListUserMemories(ctx context.Context, req *application.ListUserMemoriesRequest) ([]*application.UserMemory, int, error)
	UserStats(ctx context.Context, tenantID, userID string) (memoryCount, entityCount int, err error)
	ListUserEntities(ctx context.Context, req *application.ListUserEntitiesRequest) ([]*application.UserMemoryEntity, int, error)
}

type memoryMgrSvc interface {
	Clear(ctx context.Context, sessionCtx *application.SessionContext) error
	GetSummary(ctx context.Context, sessionCtx *application.SessionContext) (string, error)
}

// MemoryEmbeddingModelResolver resolves the configured memory embedding model
// name（平台参数 memory.embedding_model，全局）;
// implemented by wiring.tenantEmbeddingModelResolver and injected via wiring.
// nil-safe: 未配置/解析失败时 GetStats 返回 embed_model_configured=false。
type MemoryEmbeddingModelResolver interface {
	ResolveMemoryEmbeddingModel(ctx context.Context, tenantID string) (string, error)
}

type UserMemoryHandler struct {
	svc      userMemorySvc
	mgr      memoryMgrSvc
	embedSvc MemoryEmbeddingModelResolver
}

func NewUserMemoryHandler(svc userMemorySvc, mgr memoryMgrSvc, embedSvc MemoryEmbeddingModelResolver) *UserMemoryHandler {
	return &UserMemoryHandler{svc: svc, mgr: mgr, embedSvc: embedSvc}
}

// embedModelConfigured reports whether the tenant has an explicitly configured
// memory embedding model; resolver nil or error → false（fail-closed，永不 panic）。
func (h *UserMemoryHandler) embedModelConfigured(ctx context.Context, tenantID string) bool {
	if h.embedSvc == nil {
		return false
	}
	model, err := h.embedSvc.ResolveMemoryEmbeddingModel(ctx, tenantID)
	return err == nil && model != ""
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

// GetEntities godoc
// GET /memory/entities?page=&page_size=
// Returns the authenticated user's active entities as lightweight topic tags (member).
func (h *UserMemoryHandler) GetEntities(c *gin.Context) {
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
	entities, total, err := h.svc.ListUserEntities(c.Request.Context(), &application.ListUserEntitiesRequest{
		TenantID: tenantID, UserID: userID, Limit: pageSize, Offset: (page - 1) * pageSize,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemoryEntityResponse, 0, len(entities))
	for _, e := range entities {
		resp = append(resp, gen.MemoryEntityResponse{
			ID: e.ID, Name: e.Name, EntityType: e.EntityType,
			FactCount: int64(e.FactCount), LastSeenAt: e.LastSeenAt,
		})
	}
	c.JSON(http.StatusOK, gen.ListMemoryEntitiesResponse{Entities: resp, Total: int64(total)})
}

// GetStats godoc
// GET /memory/stats
// Returns the authenticated user's active memory and entity counts (member).
func (h *UserMemoryHandler) GetStats(c *gin.Context) {
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
	memoryCount, entityCount, err := h.svc.UserStats(c.Request.Context(), tenantID, userID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.MemoryStatsResponse{
		MemoryCount:          int64(memoryCount),
		EntityCount:          int64(entityCount),
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
