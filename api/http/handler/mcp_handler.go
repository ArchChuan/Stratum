// Package handler implements HTTP API request handlers.

package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MCPHandler 处理 MCP 相关的 HTTP 请求
type MCPHandler struct {
	svc    *mcpapp.MCPService
	logger *zap.Logger
}

// NewMCPHandler 创建新的 MCP 处理器
func NewMCPHandler(svc *mcpapp.MCPService, logger *zap.Logger) *MCPHandler {
	return &MCPHandler{svc: svc, logger: logger.Named("handler.mcp")}
}

// ListServers GET /mcp/servers
func (h *MCPHandler) ListServers(c *gin.Context) {
	servers := h.svc.ListServers(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"servers": servers, "count": len(servers)})
}

// GetServer GET /mcp/servers/:id
func (h *MCPHandler) GetServer(c *gin.Context) {
	server, err := h.svc.GetServer(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Error(err) //nolint:errcheck
		return
	}
	c.JSON(http.StatusOK, server)
}

// ListTools GET /mcp/servers/:id/tools
func (h *MCPHandler) ListTools(c *gin.Context) {
	serverID := c.Param("id")
	tools, err := h.svc.ListTools(c.Request.Context(), serverID)
	if err != nil {
		h.logger.Error("failed to list tools",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tools": tools, "count": len(tools)})
}

// ListResources GET /mcp/servers/:id/resources
func (h *MCPHandler) ListResources(c *gin.Context) {
	serverID := c.Param("id")
	resources, err := h.svc.ListResources(c.Request.Context(), serverID)
	if err != nil {
		h.logger.Error("failed to list resources",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resources": resources, "count": len(resources)})
}

// ExecuteTool POST /mcp/tools/:toolId/execute
func (h *MCPHandler) ExecuteTool(c *gin.Context) {
	toolID := c.Param("toolId")
	var input any
	if err := c.BindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("invalid input")))
		return
	}
	result, err := h.svc.ExecuteTool(toolID, input)
	if err != nil {
		h.logger.Error("failed to execute tool",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("tool_id", toolID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}

// ListSkills GET /mcp/skills
func (h *MCPHandler) ListSkills(c *gin.Context) {
	skills := h.svc.ListSkills()
	c.JSON(http.StatusOK, gin.H{"skills": skills, "count": len(skills)})
}

// GetSkill GET /mcp/skills/:id
func (h *MCPHandler) GetSkill(c *gin.Context) {
	skill, err := h.svc.GetSkill(c.Param("id"))
	if err != nil {
		c.Error(err) //nolint:errcheck
		return
	}
	c.JSON(http.StatusOK, skill)
}

// RefreshSkills POST /mcp/skills/refresh
func (h *MCPHandler) RefreshSkills(c *gin.Context) {
	if err := h.svc.RefreshSkills(c.Request.Context()); err != nil {
		h.logger.Error("failed to refresh skills",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "skills refreshed successfully"})
}

// GetServerStatus GET /mcp/status
func (h *MCPHandler) GetServerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.ServerStatus(c.Request.Context()))
}

// RegisterRoutes 注册 MCP 路由。
// mw       — JWT + InjectTenantContext，挂在所有 /mcp 路由上（读写均要租户上下文）。
// writeMW  — 仅写操作再追加（如 RequireActiveTenant）。
func (h *MCPHandler) RegisterRoutes(router *gin.Engine, mw []gin.HandlerFunc, writeMW ...gin.HandlerFunc) {
	v1 := router.Group("/mcp", mw...)
	v1.GET("/servers", h.ListServers)
	v1.GET("/servers/:id", h.GetServer)
	v1.GET("/servers/:id/tools", h.ListTools)
	v1.GET("/servers/:id/resources", h.ListResources)
	v1.POST("/servers", append(writeMW, h.ConnectServer)...)
	v1.PUT("/servers/:id", append(writeMW, h.UpdateServer)...)
	v1.GET("/servers/:id/config", h.GetServerConfig)
	v1.DELETE("/servers/:id", append(writeMW, h.DisconnectServer)...)
	v1.DELETE("/servers/:id/config", append(writeMW, h.DeleteServerConfig)...)
	v1.POST("/servers/:id/reconnect", append(writeMW, h.ReconnectServer)...)
	v1.POST("/tools/:toolId/execute", append(writeMW, h.ExecuteTool)...)
	v1.GET("/skills", h.ListSkills)
	v1.GET("/skills/:id", h.GetSkill)
	v1.POST("/skills/refresh", append(writeMW, h.RefreshSkills)...)
	v1.GET("/status", h.GetServerStatus)
}

// GetServerConfig GET /mcp/servers/:id/config
func (h *MCPHandler) GetServerConfig(c *gin.Context) {
	cfg, err := h.svc.GetServerConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// UpdateServer PUT /mcp/servers/:id
func (h *MCPHandler) UpdateServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var cfg mcpdomain.ServerConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	cfg.ID = c.Param("id")
	if err := h.svc.UpdateServer(c.Request.Context(), &cfg); err != nil {
		h.logger.Error("failed to update MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", cfg.ID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated", "server_id": cfg.ID})
}

// ConnectServer POST /mcp/servers
func (h *MCPHandler) ConnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var cfg mcpdomain.ServerConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.ConnectServer(c.Request.Context(), &cfg); err != nil {
		h.logger.Error("failed to connect MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", cfg.ID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "connected", "server_id": cfg.ID})
}

// DisconnectServer DELETE /mcp/servers/:id
func (h *MCPHandler) DisconnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	serverID := c.Param("id")
	if err := h.svc.DisconnectServer(c.Request.Context(), serverID); err != nil {
		h.logger.Error("failed to disconnect MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "disconnected"})
}

// DeleteServerConfig DELETE /mcp/servers/:id/config
func (h *MCPHandler) DeleteServerConfig(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	serverID := c.Param("id")
	if err := h.svc.DeleteServer(c.Request.Context(), serverID); err != nil {
		h.logger.Error("failed to delete MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ReconnectServer POST /mcp/servers/:id/reconnect
func (h *MCPHandler) ReconnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	serverID := c.Param("id")
	if err := h.svc.ReconnectServer(c.Request.Context(), serverID); err != nil {
		h.logger.Error("failed to reconnect MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "connected", "server_id": serverID})
}
