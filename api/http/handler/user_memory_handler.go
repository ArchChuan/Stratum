package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

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
	ListUserFactsFiltered(ctx context.Context, req *application.ListUserFactsFilteredRequest) ([]*application.UserFactDetail, int, error)
	GetUserFact(ctx context.Context, tenantID, userID, factID string) (*application.UserFactDetail, error)
	UpdateUserFact(ctx context.Context, tenantID, userID, factID string, patch *application.UpdateUserFactPatch) (*application.UserFactDetail, bool, error)
	DeleteUserFact(ctx context.Context, tenantID, userID, factID string) error
	DeleteUserEntity(ctx context.Context, tenantID, userID, entityID string) error
	ListUserSummaries(ctx context.Context, tenantID, userID string, limit, offset int) ([]*application.UserSummary, int, error)
	DeleteUserSummary(ctx context.Context, tenantID, userID, summaryID string) error
	ListUserSnapshots(ctx context.Context, tenantID, userID string) ([]*application.UserSnapshot, error)
	UpdateUserSnapshot(ctx context.Context, tenantID, userID, agentID string, patch *application.UpdateUserSnapshotPatch) (*application.UserSnapshot, error)
	DeleteUserSnapshot(ctx context.Context, tenantID, userID, agentID string) error
	ListUserEntries(ctx context.Context, tenantID, userID string, limit, offset int, query string) ([]*application.UserEntry, int, error)
	DeleteUserEntry(ctx context.Context, tenantID, userID, entryID string) error
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

// parsePageParams 解析 page/page_size，非法值返回 error（handler 统一 400）。
func parsePageParams(c *gin.Context) (page, pageSize int, err error) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	return page, pageSize, nil
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
		Confidence: memory.Confidence, Category: memory.Category, Source: memory.Source, Status: memory.Status,
	}
}

func memoryFactResponseFromDetail(fact *application.UserFactDetail) gen.MemoryFactResponse {
	return gen.MemoryFactResponse{
		ID: fact.ID, Scope: fact.Scope, Content: fact.Content,
		Importance: fact.Importance, CreatedAt: fact.CreatedAt, UpdatedAt: fact.UpdatedAt,
		Confidence: fact.Confidence, Category: fact.Category, Source: fact.Source, Status: fact.Status,
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

// ListFacts godoc
// GET /memory/facts?page=&page_size=&q=&importance_min=&importance_max=&category=
// 事实管理列表（搜索 + 重要度/分类筛选 + 分页）。
func (h *UserMemoryHandler) ListFacts(c *gin.Context) {
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
	page, pageSize, err := parsePageParams(c)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	var importanceMin, importanceMax *float64
	for key, dst := range map[string]**float64{
		"importance_min": &importanceMin,
		"importance_max": &importanceMax,
	} {
		raw := c.Query(key)
		if raw == "" {
			continue
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
			return
		}
		*dst = &value
	}
	facts, total, err := h.svc.ListUserFactsFiltered(c.Request.Context(), &application.ListUserFactsFilteredRequest{
		TenantID: tenantID, UserID: userID, Query: c.Query("q"), Category: c.Query("category"),
		ImportanceMin: importanceMin, ImportanceMax: importanceMax,
		Limit: pageSize, Offset: (page - 1) * pageSize,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemoryFactResponse, 0, len(facts))
	for _, f := range facts {
		resp = append(resp, memoryFactResponseFromDetail(f))
	}
	c.JSON(http.StatusOK, gen.ListMemoryFactsResponse{Facts: resp, Total: int64(total)})
}

// GetFact godoc
// GET /memory/facts/:id 事实详情（编辑预填）。
func (h *UserMemoryHandler) GetFact(c *gin.Context) {
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
	fact, err := h.svc.GetUserFact(c.Request.Context(), tenantID, userID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, memoryFactResponseFromDetail(fact))
}

// UpdateFact godoc
// PATCH /memory/facts/:id body {content?, importance?, category?}
// 内容/重要度/分类至少一项；向量同步失败仍返回 200 + vector_sync_failed=true。
func (h *UserMemoryHandler) UpdateFact(c *gin.Context) {
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
	var req gen.UpdateMemoryFactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	if req.Content == nil && req.Importance == nil && req.Category == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	fact, vectorSyncFailed, err := h.svc.UpdateUserFact(c.Request.Context(), tenantID, userID, c.Param("id"),
		&application.UpdateUserFactPatch{Content: req.Content, Importance: req.Importance, Category: req.Category})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fact": memoryFactResponseFromDetail(fact), "vector_sync_failed": vectorSyncFailed})
}

// DeleteFact godoc
// DELETE /memory/facts/:id 硬删 + 向量清理（best-effort）。
func (h *UserMemoryHandler) DeleteFact(c *gin.Context) {
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
	if err := h.svc.DeleteUserFact(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteEntity godoc
// DELETE /memory/entities/:id 单条实体删除。
func (h *UserMemoryHandler) DeleteEntity(c *gin.Context) {
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
	if err := h.svc.DeleteUserEntity(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListSummaries godoc
// GET /memory/summaries?page=&page_size= 历史摘要分页列表。
func (h *UserMemoryHandler) ListSummaries(c *gin.Context) {
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
	page, pageSize, err := parsePageParams(c)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	summaries, total, err := h.svc.ListUserSummaries(c.Request.Context(), tenantID, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemorySummaryItemResponse, 0, len(summaries))
	for _, s := range summaries {
		resp = append(resp, gen.MemorySummaryItemResponse{
			ID: s.ID, Summary: s.Summary, Tier: s.Tier, Importance: s.Importance,
			ConversationID: s.ConversationID, PeriodEnd: s.PeriodEnd, CreatedAt: s.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gen.ListMemorySummariesResponse{Summaries: resp, Total: int64(total)})
}

// DeleteSummary godoc
// DELETE /memory/summaries/:id 历史摘要删除。
func (h *UserMemoryHandler) DeleteSummary(c *gin.Context) {
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
	if err := h.svc.DeleteUserSummary(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListSnapshots godoc
// GET /memory/snapshots 该用户全部 (user,agent) 快照（含过期，供管理/清空）。
func (h *UserMemoryHandler) ListSnapshots(c *gin.Context) {
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
	snapshots, err := h.svc.ListUserSnapshots(c.Request.Context(), tenantID, userID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemorySnapshotResponse, 0, len(snapshots))
	for _, s := range snapshots {
		resp = append(resp, gen.MemorySnapshotResponse{
			AgentID: s.AgentID, WorkContext: s.WorkContext, PersonalContext: s.PersonalContext,
			TopOfMind: s.TopOfMind, ExpiresAt: s.ExpiresAt, UpdatedAt: s.UpdatedAt, Status: s.Status,
		})
	}
	c.JSON(http.StatusOK, gen.ListMemorySnapshotsResponse{Snapshots: resp})
}

// UpdateSnapshot godoc
// PATCH /memory/snapshots/:agent_id body {work_context, personal_context, top_of_mind}
func (h *UserMemoryHandler) UpdateSnapshot(c *gin.Context) {
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
	var req gen.UpdateMemorySnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	snapshot, err := h.svc.UpdateUserSnapshot(c.Request.Context(), tenantID, userID, c.Param("agent_id"),
		&application.UpdateUserSnapshotPatch{
			WorkContext: req.WorkContext, PersonalContext: req.PersonalContext, TopOfMind: req.TopOfMind,
		})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.MemorySnapshotResponse{
		AgentID: snapshot.AgentID, WorkContext: snapshot.WorkContext, PersonalContext: snapshot.PersonalContext,
		TopOfMind: snapshot.TopOfMind, ExpiresAt: snapshot.ExpiresAt, UpdatedAt: snapshot.UpdatedAt,
		Status: snapshot.Status,
	})
}

// DeleteSnapshot godoc
// DELETE /memory/snapshots/:agent_id 清空快照。
func (h *UserMemoryHandler) DeleteSnapshot(c *gin.Context) {
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
	if err := h.svc.DeleteUserSnapshot(c.Request.Context(), tenantID, userID, c.Param("agent_id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListEntries godoc
// GET /memory/entries?page=&page_size=&q= 原始条目分页列表。
func (h *UserMemoryHandler) ListEntries(c *gin.Context) {
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
	page, pageSize, err := parsePageParams(c)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}
	entries, total, err := h.svc.ListUserEntries(c.Request.Context(), tenantID, userID, pageSize, (page-1)*pageSize, c.Query("q"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := make([]gen.MemoryEntryItemResponse, 0, len(entries))
	for _, e := range entries {
		var expiresAt time.Time
		if e.ExpiresAt != nil {
			expiresAt = *e.ExpiresAt
		}
		resp = append(resp, gen.MemoryEntryItemResponse{
			ID: e.ID, Role: e.Role, Content: e.Content, Type: e.Type, Scope: e.Scope,
			Importance: e.Importance, CreatedAt: e.CreatedAt, ExpiresAt: expiresAt,
		})
	}
	c.JSON(http.StatusOK, gen.ListMemoryEntriesResponse{Entries: resp, Total: int64(total)})
}

// DeleteEntry godoc
// DELETE /memory/entries/:id 硬删 + 向量清理（best-effort）。
func (h *UserMemoryHandler) DeleteEntry(c *gin.Context) {
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
	if err := h.svc.DeleteUserEntry(c.Request.Context(), tenantID, userID, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
