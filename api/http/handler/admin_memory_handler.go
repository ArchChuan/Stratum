package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var errInvalidInput = errors.New("invalid input")

// DiagnosticsServiceInterface defines the port for admin diagnostics.
type DiagnosticsServiceInterface interface {
	GetDiagnostics(ctx context.Context, tenantID string) (*application.Diagnostics, error)
}

// MemoryServiceInterface defines the port for memory operations.
type MemoryServiceInterface interface {
	ForgetMemory(ctx context.Context, req *application.ForgetMemoryRequest) error
}

// AdminMemoryHandler handles system_admin memory diagnostic endpoints.
type AdminMemoryHandler struct {
	diagSvc DiagnosticsServiceInterface
	memSvc  MemoryServiceInterface
	logger  *zap.Logger
}

// NewAdminMemoryHandler constructs an AdminMemoryHandler.
func NewAdminMemoryHandler(diagSvc DiagnosticsServiceInterface, memSvc MemoryServiceInterface, logger *zap.Logger) *AdminMemoryHandler {
	return &AdminMemoryHandler{
		diagSvc: diagSvc,
		memSvc:  memSvc,
		logger:  logger,
	}
}

// GetDiagnostics retrieves memory system diagnostics for a tenant.
// GET /api/admin/memory/diagnostics?tenant_id=xxx
func (h *AdminMemoryHandler) GetDiagnostics(c *gin.Context) {
	tenantID := c.Query("tenant_id")
	if tenantID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}

	diag, err := h.diagSvc.GetDiagnostics(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, diag)
}

// ForgetFact deletes a memory fact across tenants (admin override).
// POST /api/admin/memory/facts/:id/forget?tenant_id=xxx&user_id=xxx
func (h *AdminMemoryHandler) ForgetFact(c *gin.Context) {
	factID := c.Param("id")
	tenantID := c.Query("tenant_id")
	userID := c.Query("user_id")

	if tenantID == "" || userID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errInvalidInput))
		return
	}

	req := &application.ForgetMemoryRequest{
		TenantID: tenantID,
		UserID:   userID,
		FactID:   factID,
	}

	if err := h.memSvc.ForgetMemory(c.Request.Context(), req); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "fact forgotten"})
}

// ListTenants lists all tenants (stub implementation).
// GET /api/admin/memory/tenants
func (h *AdminMemoryHandler) ListTenants(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tenants": []interface{}{}})
}
