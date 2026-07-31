package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mcpport "github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// MCPForwardHandler bridges internal HTTP forwarding requests to the local
// server manager.
type MCPForwardHandler struct {
	manager mcpport.ServerManager
	logger  *zap.Logger
}

// NewMCPForwardHandler creates a forwarding handler backed by the given manager.
func NewMCPForwardHandler(manager mcpport.ServerManager, logger *zap.Logger) *MCPForwardHandler {
	return &MCPForwardHandler{manager: manager, logger: logger}
}

type forwardHTTPRequest struct {
	TenantID string         `json:"tenant_id" binding:"required"`
	ServerID string         `json:"server_id" binding:"required"`
	ToolName string         `json:"tool_name" binding:"required"`
	Args     map[string]any `json:"args"`
}

// ForwardToolCall handles POST /internal/mcp/tools/call.
func (h *MCPForwardHandler) ForwardToolCall(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, constants.MCPStdioMessageMaxBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}

	var req forwardHTTPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.TenantID == "" || req.ServerID == "" || req.ToolName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id, server_id, tool_name are required"})
		return
	}

	resp, err := h.manager.HandleForwardedToolCall(
		c.Request.Context(), req.TenantID, req.ServerID, req.ToolName, req.Args,
	)
	if err != nil {
		h.logger.Error("mcp forward: handle failed",
			zap.String("tenant_id", req.TenantID),
			zap.String("server_id", req.ServerID),
			zap.String("tool_name", req.ToolName),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}
