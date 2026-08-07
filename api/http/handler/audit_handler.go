package handler

import (
	"net/http"
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
	events, err := h.query.Query(c.Request.Context(), parseAuditFilter(c, tenantID))
	if err != nil {
		h.logger.Error("audit: list events failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

// parseAuditFilter 从 query string 解析审计筛选条件；无效时间戳静默忽略
// （与历史行为一致）。
func parseAuditFilter(c *gin.Context, tenantID string) auditdomain.AuditFilter {
	filter := auditdomain.AuditFilter{
		TenantID: tenantID,
		Limit:    50,
	}
	if from := c.Query("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			filter.From = t
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			filter.To = t
		}
	}
	if action := c.Query("action"); action != "" {
		filter.Action = action
	}
	if risk := c.Query("risk_level"); risk != "" {
		filter.RiskLevel = risk
	}
	if outcome := c.Query("outcome"); outcome != "" {
		filter.Outcome = outcome
	}
	if resourceType := c.Query("resource_type"); resourceType != "" {
		filter.ResourceType = resourceType
	}
	return filter
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
