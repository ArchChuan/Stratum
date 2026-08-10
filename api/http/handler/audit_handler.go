package handler

import (
	"net/http"
	"strconv"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuditHandler serves the audit event read endpoints.
type AuditHandler struct {
	query  auditport.AuditQueryService
	logger *zap.Logger
}

// NewAuditHandler creates an audit HTTP handler.
func NewAuditHandler(query auditport.AuditQueryService, logger *zap.Logger) *AuditHandler {
	return &AuditHandler{query: query, logger: logger}
}

// ListEvents returns a paginated list of audit events for the current tenant.
func (h *AuditHandler) ListEvents(c *gin.Context) {
	tenantID, ok := tenantIDFromGinKey(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	filter := parseAuditFilter(c, tenantID)
	// Count 与 Query 用同一 filter；count 失败时 fail closed，不返回部分结果。
	total, err := h.query.Count(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("audit: count events failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit events"})
		return
	}
	events, err := h.query.Query(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("audit: list events failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events), "total": total})
}

// parseAuditFilter 从 query string 解析审计筛选条件；无效时间戳静默忽略
// （与历史行为一致）。分页参数 page/page_size（默认 1/50，保持旧行为）。
func parseAuditFilter(c *gin.Context, tenantID string) auditdomain.AuditFilter {
	page, pageSize := parsePageQuery(c, 1, 50)
	filter := auditdomain.AuditFilter{
		TenantID: tenantID,
		Limit:    pageSize,
		Offset:   (page - 1) * pageSize,
	}
	filter.From = parseTimeQuery(c, "from")
	filter.To = parseTimeQuery(c, "to")
	filter.Action = c.Query("action")
	filter.RiskLevel = c.Query("risk_level")
	filter.Outcome = c.Query("outcome")
	filter.ResourceType = c.Query("resource_type")
	return filter
}

// parsePageQuery 解析 page/page_size；非法值回退到默认值（与历史行为一致）。
func parsePageQuery(c *gin.Context, defaultPage, defaultPageSize int) (int, int) {
	page := defaultPage
	if p := c.Query("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 {
			page = n
		}
	}
	pageSize := defaultPageSize
	if ps := c.Query("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 {
			pageSize = n
		}
	}
	return page, pageSize
}

// parseTimeQuery 解析 RFC3339 时间参数；缺失或非法时返回零值（静默忽略）。
func parseTimeQuery(c *gin.Context, key string) time.Time {
	raw := c.Query(key)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// GetEvent returns a single audit event by ID, scoped to the current tenant.
func (h *AuditHandler) GetEvent(c *gin.Context) {
	id := c.Param("id")
	tenantID, ok := tenantIDFromGinKey(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	event, err := h.query.GetByID(c.Request.Context(), tenantID, id)
	if err != nil {
		h.logger.Error("audit: get event failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get audit event"})
		return
	}
	if event == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit event not found"})
		return
	}
	c.JSON(http.StatusOK, event)
}

// tenantIDFromGinKey 读取 JWT 中间件写入的 auth.tenant_id gin key。
// auth.tenant_id 由 JWT middleware 保证存在，但缺 key 或类型异常时仍要
// fail closed（返回 401）而非对 type assertion panic——防御性修复。
func tenantIDFromGinKey(c *gin.Context) (string, bool) {
	val, exists := c.Get("auth.tenant_id")
	if !exists {
		return "", false
	}
	id, ok := val.(string)
	return id, ok
}
