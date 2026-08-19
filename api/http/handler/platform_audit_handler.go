package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
)

// PlatformAuditHandler exposes public-catalog audit events to global admins.
type PlatformAuditHandler struct {
	query auditport.PlatformResourceChangeAuditQuery
}

func NewPlatformAuditHandler(query auditport.PlatformResourceChangeAuditQuery) *PlatformAuditHandler {
	return &PlatformAuditHandler{query: query}
}

func (h *PlatformAuditHandler) List(c *gin.Context) {
	filter := auditport.ResourceChangeAuditFilter{
		ResourceKind: c.Query("resource_kind"),
		ActorName:    c.Query("actor_name"),
		Limit:        20,
	}
	if value, err := strconv.Atoi(c.DefaultQuery("page_size", "20")); err == nil {
		filter.Limit = value
	}
	if value, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && value > 0 {
		filter.Offset = (value - 1) * filter.Limit
	}
	rows, total, err := h.query.ListPlatform(c.Request.Context(), filter)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": rows, "total": total})
}

func (h *PlatformAuditHandler) Get(c *gin.Context) {
	row, err := h.query.GetPlatformByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if row == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit event not found"})
		return
	}
	c.JSON(http.StatusOK, row)
}
