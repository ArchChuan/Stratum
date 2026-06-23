package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
)

var errInvalidInput = errors.New("invalid user")

type userMemorySvc interface {
	ClearUserMemories(ctx context.Context, req *application.ClearUserMemoriesRequest) error
}

// UserMemoryHandler handles user-scoped memory operations.
type UserMemoryHandler struct {
	svc userMemorySvc
}

func NewUserMemoryHandler(svc userMemorySvc) *UserMemoryHandler {
	return &UserMemoryHandler{svc: svc}
}

// ClearMemories soft-deletes all memories for the authenticated user.
// DELETE /api/memory/clear
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
