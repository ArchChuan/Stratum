package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/gin-gonic/gin"
)

// memoryMigrationSvc 是 P5 确认制切换的应用服务面，由 wiring.MemoryMigrationService
// 实现。租户边界在 service 内显式校验（port 方法全带 tenantID）。
type memoryMigrationSvc interface {
	StartMigration(ctx context.Context, tenantID, fromModel, toModel string) (*domain.MemoryMigration, error)
	CancelMigration(ctx context.Context, tenantID string, id int64) error
	RetryMigration(ctx context.Context, tenantID string, id int64) error
	GetCurrent(ctx context.Context, tenantID string) (*domain.MemoryMigration, error)
	CostPreview(ctx context.Context, tenantID string) (*application.MigrationCost, error)
}

// MemoryMigrationHandler 暴露记忆嵌入模型平滑迁移的管理端点：管理员确认制切换
// （展示成本预览 → 启动 → 进度查询 → 取消/重试）。
type MemoryMigrationHandler struct {
	svc      memoryMigrationSvc
	embedSvc MemoryEmbeddingModelResolver
}

func NewMemoryMigrationHandler(svc memoryMigrationSvc, embedSvc MemoryEmbeddingModelResolver) *MemoryMigrationHandler {
	return &MemoryMigrationHandler{svc: svc, embedSvc: embedSvc}
}

// GetCurrent godoc
// GET /tenant/memory/migrations/current
// 返回租户最近一条迁移（任意状态）；从未迁移过时返回 JSON null（前端据此隐藏迁移区）。
func (h *MemoryMigrationHandler) GetCurrent(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	m, err := h.svc.GetCurrent(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if m == nil {
		c.JSON(http.StatusOK, nil)
		return
	}
	c.JSON(http.StatusOK, memoryMigrationResponse(m))
}

// GetCost godoc
// GET /tenant/memory/migrations/cost
// 返回迁移成本预览：存量已提取事实条数 + 预计回填时长（秒），供管理员确认前展示。
func (h *MemoryMigrationHandler) GetCost(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	cost, err := h.svc.CostPreview(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gen.MemoryMigrationCostResponse{
		FactCount:        int64(cost.FactCount),
		EstimatedSeconds: cost.EstimatedSeconds,
	})
}

// Start godoc
// POST /tenant/memory/migrations  body: {"to_model": "..."}
// 确认制启动 A→B 迁移：from 取租户当前生效模型（未配置则无法迁移，fail-closed）。
// 登记迁移记录后立即切换生效模型，存量事实由后台回填 worker 渐进 re-embed。
func (h *MemoryMigrationHandler) Start(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req gen.StartMemoryMigrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(err)
		return
	}
	toModel := strings.TrimSpace(req.ToModel)
	if toModel == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("to_model is required")))
		return
	}
	fromModel, err := h.resolveFromModel(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	m, err := h.svc.StartMigration(c.Request.Context(), tenantID, fromModel, toModel)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, memoryMigrationResponse(m))
}

// Cancel godoc
// POST /tenant/memory/migrations/:id/cancel
// 取消进行中的迁移（保留进度，可重试续传）。生效模型不随取消回退（回滚是显式反向迁移）。
func (h *MemoryMigrationHandler) Cancel(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id, err := parseMigrationID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := h.svc.CancelMigration(c.Request.Context(), tenantID, id); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Retry godoc
// POST /tenant/memory/migrations/:id/retry
// 把 failed/canceled 迁移重置为 migrating，从既有进度断点续传。
func (h *MemoryMigrationHandler) Retry(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id, err := parseMigrationID(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := h.svc.RetryMigration(c.Request.Context(), tenantID, id); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// resolveFromModel 解析租户当前生效的记忆嵌入模型作为迁移起点；未配置/解析失败
// 返回 400（fail-closed：从空模型启动迁移会产生语义污染的 collection）。
func (h *MemoryMigrationHandler) resolveFromModel(ctx context.Context, tenantID string) (string, error) {
	if h.embedSvc == nil {
		return "", middleware.NewHTTPError(http.StatusBadRequest,
			errors.New("current memory embedding model not configured; cannot start migration"))
	}
	model, err := h.embedSvc.ResolveMemoryEmbeddingModel(ctx, tenantID)
	if err != nil || model == "" {
		return "", middleware.NewHTTPError(http.StatusBadRequest,
			errors.New("current memory embedding model not configured; cannot start migration"))
	}
	return model, nil
}

func parseMigrationID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, middleware.NewHTTPError(http.StatusBadRequest, errors.New("invalid migration id"))
	}
	return id, nil
}

func memoryMigrationResponse(m *domain.MemoryMigration) gen.MemoryMigrationResponse {
	return gen.MemoryMigrationResponse{
		ID:         m.ID,
		FromModel:  m.FromModel,
		ToModel:    m.ToModel,
		Status:     string(m.Status),
		Progress:   int32(m.Progress),   //nolint:gosec // progress 是已回填事实游标,不可能溢出 int32(proto 契约)
		TotalFacts: int32(m.TotalFacts), //nolint:gosec // total_facts 是 memory_facts 行数快照,不可能溢出 int32(proto 契约)
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}
