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
	tid, _ := c.Get("auth.tenant_id")
	filter := auditdomain.AuditFilter{
		TenantID: tid.(string),
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

	events, err := h.query.Query(c.Request.Context(), filter)
	if err != nil {
		h.logger.Error("audit: list events failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query audit events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

// GetEvent returns a single audit event by ID.
func (h *AuditHandler) GetEvent(c *gin.Context) {
	id := c.Param("id")
	event, err := h.query.GetByID(c.Request.Context(), id)
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
