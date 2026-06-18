// Package handler implements HTTP API request handlers.

package handler

import (
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	"go.uber.org/zap"
)

// errUnauthorized is the sentinel passed to middleware.NewHTTPError for missing auth.
var errUnauthorized = errors.New("unauthorized")

// MemoryHandler exposes /memory/* endpoints. Endpoint methods are split
// across memory_session_handler.go / memory_message_handler.go /
// memory_entity_handler.go / memory_summary_handler.go.
type MemoryHandler struct {
	manager *memory.MemoryManager
	logger  *zap.Logger
}

// NewMemoryHandler constructs a MemoryHandler.
func NewMemoryHandler(manager *memory.MemoryManager, logger *zap.Logger) *MemoryHandler {
	return &MemoryHandler{
		manager: manager,
		logger:  logger,
	}
}

func toMemoryEntryResponse(e *memory.MemoryEntry) dto.MemoryEntryResponse {
	resp := dto.MemoryEntryResponse{
		ID:         e.ID,
		Type:       string(e.Type),
		Role:       e.Role,
		Content:    e.Content,
		Timestamp:  e.Timestamp.Format(time.RFC3339),
		TenantID:   e.TenantID,
		UserID:     e.UserID,
		SessionID:  e.SessionID,
		AgentID:    e.AgentID,
		Metadata:   e.Metadata,
		Tags:       e.Tags,
		Importance: e.Importance,
	}
	if !e.ExpiresAt.IsZero() {
		resp.ExpiresAt = e.ExpiresAt.Format(time.RFC3339)
	}
	return resp
}

func toEntityResponse(e *memory.Entity) dto.EntityResponse {
	relations := make([]dto.EntityRelationItem, 0, len(e.Relations))
	for _, r := range e.Relations {
		relations = append(relations, dto.EntityRelationItem{
			FromEntityID: r.FromEntityID,
			ToEntityID:   r.ToEntityID,
			RelationType: r.RelationType,
			Confidence:   r.Confidence,
			LastSeen:     r.LastSeen.Format(time.RFC3339),
			Metadata:     r.Metadata,
		})
	}
	return dto.EntityResponse{
		ID:         e.ID,
		Name:       e.Name,
		Type:       e.Type,
		Confidence: e.Confidence,
		FirstSeen:  e.FirstSeen.Format(time.RFC3339),
		LastSeen:   e.LastSeen.Format(time.RFC3339),
		Attributes: e.Attributes,
		Relations:  relations,
	}
}
