package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
)

type platformAuditDTO struct {
	ID           string          `json:"id"`
	ResourceKind string          `json:"resource_kind"`
	ResourceID   string          `json:"resource_id"`
	Operation    string          `json:"operation"`
	ActorID      string          `json:"actor_id"`
	ActorName    string          `json:"actor_name"`
	CreatedAt    string          `json:"created_at"`
	Before       json.RawMessage `json:"before"`
	After        json.RawMessage `json:"after"`
}

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
	events := make([]platformAuditDTO, 0, len(rows))
	for _, row := range rows {
		events = append(events, platformAuditDTO{
			ID: row.ID, ResourceKind: row.ResourceKind, ResourceID: row.ResourceID,
			Operation: row.Operation, ActorID: row.ActorID, ActorName: row.ActorName,
			CreatedAt: row.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
			Before:    row.Before, After: row.After,
		})
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "total": total})
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
	c.JSON(http.StatusOK, platformAuditDTO{
		ID: row.ID, ResourceKind: row.ResourceKind, ResourceID: row.ResourceID,
		Operation: row.Operation, ActorID: row.ActorID, ActorName: row.ActorName,
		CreatedAt: row.CreatedAt.UTC().Format("2006-01-02T15:04:05.999Z07:00"),
		Before:    row.Before, After: row.After,
	})
}
