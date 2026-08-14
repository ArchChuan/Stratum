package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuditHandler 提供租户资源变更审计的读取接口。租户取自 tenantIDFromCtx
// （空字符串返回 false），不再使用宽容版 tenantIDFromGinKey。
type AuditHandler struct {
	query  auditport.ResourceChangeAuditQuery
	logger *zap.Logger
}

func NewAuditHandler(query auditport.ResourceChangeAuditQuery, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{query: query, logger: logger}
}

// ListEvents 返回当前租户的资源变更审计分页列表。
func (h *AuditHandler) ListEvents(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "20"), 10, 32)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	filter := auditport.ResourceChangeAuditFilter{
		ResourceKind: c.Query("resource_kind"),
		ActorName:    c.Query("actor_name"),
		Limit:        int(pageSize),
		Offset:       int((page - 1) * pageSize),
	}
	filter = applyAuditTimeRange(filter, c.Query("from"), c.Query("to"))

	rows, total, err := h.query.List(c.Request.Context(), tenantID, filter)
	if err != nil {
		h.logger.Error("audit: list resource change audits failed", zap.Error(err))
		_ = c.Error(err)
		return
	}
	events := make([]gen.ResourceChangeAudit, 0, len(rows))
	for _, row := range rows {
		events = append(events, toResourceChangeAuditDTO(row))
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "total": total})
}

// GetEvent 返回单条资源变更审计详情。
func (h *AuditHandler) GetEvent(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	row, err := h.query.GetByID(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if row == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusNotFound, errors.New("audit event not found")))
		return
	}
	c.JSON(http.StatusOK, toResourceChangeAuditDTO(*row))
}

// applyAuditTimeRange 把 RFC3339 字符串解析进筛选范围；非法值静默忽略
// （页面端 RangePicker 已保证合法格式）。
func applyAuditTimeRange(filter auditport.ResourceChangeAuditFilter, from, to string) auditport.ResourceChangeAuditFilter {
	if from != "" {
		if parsed, err := time.Parse(time.RFC3339, from); err == nil {
			filter.From = &parsed
		}
	}
	if to != "" {
		if parsed, err := time.Parse(time.RFC3339, to); err == nil {
			filter.To = &parsed
		}
	}
	return filter
}

// toResourceChangeAuditDTO 把查询行映射为契约 DTO。before/after 为 JSONB
// 投影，解到 map 后赋值（生成 DTO 字段为 map[string]any，见 gen/audit.go）。
func toResourceChangeAuditDTO(row auditport.ResourceChangeAuditRow) gen.ResourceChangeAudit {
	dto := gen.ResourceChangeAudit{
		ID:           row.ID,
		ResourceKind: row.ResourceKind,
		ResourceID:   row.ResourceID,
		Operation:    row.Operation,
		ActorID:      row.ActorID,
		ActorName:    row.ActorName,
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
	}
	if len(row.Before) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(row.Before, &obj); err == nil {
			dto.Before = obj
		}
	}
	if len(row.After) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(row.After, &obj); err == nil {
			dto.After = obj
		}
	}
	return dto
}
