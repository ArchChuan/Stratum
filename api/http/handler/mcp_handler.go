// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
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

func (h *MCPHandler) ListToolPolicies(c *gin.Context) {
	policies, err := h.svc.ListToolPolicies(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policies": policies})
}

func (h *MCPHandler) SetToolPolicy(c *gin.Context) {
	var req struct {
		RiskLevel mcpdomain.ToolRiskLevel `json:"riskLevel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	updatedBy := ""
	if tc, ok := postgres.FromContext(c.Request.Context()); ok {
		updatedBy = tc.UserID
	}
	err := h.svc.SetToolPolicy(c.Request.Context(), mcpdomain.ToolPolicy{
		ServerID: c.Param("serverId"), ToolName: c.Param("toolName"), RiskLevel: req.RiskLevel, UpdatedBy: updatedBy,
	})
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
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

// GetServerStatus GET /mcp/status
func (h *MCPHandler) GetServerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.ServerStatus(c.Request.Context()))
}

// RegisterRoutes 注册 MCP 路由。
// mw       — JWT + InjectTenantContext (+ member 底线)，挂在所有 /mcp 路由上（读写均要租户上下文）。
// writeMW  — member 可执行的运行时操作再追加（如 RequireActiveTenant）：工具执行。
// adminMW  — 管理类操作（连接/更新/断开/删除配置/重连/刷新技能）再追加，仅 admin+ 可用。
//
// 普通租户成员（member）只能读取非敏感服务器信息与执行工具。完整配置的脱敏视图仍只供管理员编辑使用，
// 连接、配置读取、更新、断开、删除和重连等管理动作均收归 admin。
func (h *MCPHandler) RegisterRoutes(router *gin.Engine, mw []gin.HandlerFunc, _ []gin.HandlerFunc, adminMW []gin.HandlerFunc) {
	// clone 避免多次 append 复用底层数组造成中间件串味。
	admin := func(handlers ...gin.HandlerFunc) []gin.HandlerFunc {
		out := make([]gin.HandlerFunc, 0, len(adminMW)+len(handlers))
		out = append(out, adminMW...)
		return append(out, handlers...)
	}

	v1 := router.Group("/mcp", mw...)
	v1.GET("/servers", h.ListServers)
	v1.GET("/servers/:id", h.GetServer)
	v1.GET("/servers/:id/tools", h.ListTools)
	v1.GET("/tool-policies", h.ListToolPolicies)
	v1.PUT("/tool-policies/:serverId/:toolName", admin(h.SetToolPolicy)...)
	v1.GET("/servers/:id/resources", h.ListResources)
	v1.POST("/servers", admin(h.ConnectServer)...)
	v1.PUT("/servers/:id", admin(h.UpdateServer)...)
	v1.GET("/servers/:id/config", admin(h.GetServerConfig)...)
	v1.DELETE("/servers/:id", admin(h.DisconnectServer)...)
	v1.DELETE("/servers/:id/config", admin(h.DeleteServerConfig)...)
	v1.POST("/servers/:id/reconnect", admin(h.ReconnectServer)...)
	v1.GET("/status", h.GetServerStatus)
}

// GetServerConfig GET /mcp/servers/:id/config
func (h *MCPHandler) GetServerConfig(c *gin.Context) {
	cfg, err := h.svc.GetServerConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.NewMCPServerConfigResponse(cfg))
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
