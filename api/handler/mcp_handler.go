// Package handler implements HTTP API request handlers.

package handler

import (
	"net/http"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MCPHandler 处理 MCP 相关的 HTTP 请求
type MCPHandler struct {
	skillRegistry *mcp.MCPSkillRegistry
	manager       *mcp.ClientManager
	logger        *zap.Logger
}

// NewMCPHandler 创建新的 MCP 处理器
func NewMCPHandler(skillRegistry *mcp.MCPSkillRegistry, manager *mcp.ClientManager, logger *zap.Logger) *MCPHandler {
	return &MCPHandler{
		skillRegistry: skillRegistry,
		manager:       manager,
		logger:        logger.Named("handler.mcp"),
	}
}

// ListServers 列出所有 MCP 服务器
// GET /api/v1/mcp/servers
func (h *MCPHandler) ListServers(c *gin.Context) {
	servers := h.manager.GetAllServerInfo()
	c.JSON(http.StatusOK, gin.H{
		"servers": servers,
		"count":   len(servers),
	})
}

// GetServer 获取 MCP 服务器详情
// GET /api/v1/mcp/servers/:id
func (h *MCPHandler) GetServer(c *gin.Context) {
	serverID := c.Param("id")
	server := h.manager.GetServerInfo(serverID)

	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "server not found",
		})
		return
	}

	c.JSON(http.StatusOK, server)
}

// ListTools 列出 MCP 服务器的工具
// GET /api/v1/mcp/servers/:id/tools
func (h *MCPHandler) ListTools(c *gin.Context) {
	serverID := c.Param("id")

	tools, err := h.manager.ListTools(c.Request.Context(), serverID)
	if err != nil {
		h.logger.Error("failed to list tools", zap.String("server_id", serverID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tools": tools,
		"count": len(tools),
	})
}

// ListResources 列出 MCP 服务器的资源
// GET /api/v1/mcp/servers/:id/resources
func (h *MCPHandler) ListResources(c *gin.Context) {
	serverID := c.Param("id")

	resources, err := h.manager.ListResources(c.Request.Context(), serverID)
	if err != nil {
		h.logger.Error("failed to list resources", zap.String("server_id", serverID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"resources": resources,
		"count":     len(resources),
	})
}

// ExecuteTool 执行 MCP 工具
// POST /api/v1/mcp/tools/:toolId/execute
func (h *MCPHandler) ExecuteTool(c *gin.Context) {
	toolID := c.Param("toolId")

	var input any
	if err := c.BindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid input",
		})
		return
	}

	result, err := h.skillRegistry.ExecuteSkill(toolID, input)
	if err != nil {
		h.logger.Error("failed to execute tool", zap.String("tool_id", toolID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result": result,
	})
}

// ListSkills 列出所有 MCP Skills
// GET /api/v1/mcp/skills
func (h *MCPHandler) ListSkills(c *gin.Context) {
	skills := h.skillRegistry.GetAllSkills()

	var skillInfos []gin.H
	for _, skill := range skills {
		skillInfos = append(skillInfos, gin.H{
			"id":          skill.GetID(),
			"name":        skill.GetName(),
			"description": skill.GetDescription(),
			"type":        skill.GetType(),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"skills": skillInfos,
		"count":  len(skillInfos),
	})
}

// GetSkill 获取 MCP Skill 详情
// GET /api/v1/mcp/skills/:id
func (h *MCPHandler) GetSkill(c *gin.Context) {
	skillID := c.Param("id")
	skill := h.skillRegistry.GetSkill(skillID)

	if skill == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "skill not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          skill.GetID(),
		"name":        skill.GetName(),
		"description": skill.GetDescription(),
		"type":        skill.GetType(),
	})
}

// RefreshSkills 刷新所有 MCP Skills
// POST /api/v1/mcp/skills/refresh
func (h *MCPHandler) RefreshSkills(c *gin.Context) {
	err := h.skillRegistry.RefreshSkills(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to refresh skills", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "skills refreshed successfully",
	})
}

// GetServerStatus 获取服务器状态
// GET /api/v1/mcp/status
func (h *MCPHandler) GetServerStatus(c *gin.Context) {
	servers := h.manager.GetAllServerInfo()

	var connected, disconnected, error int
	for _, server := range servers {
		switch server.Status {
		case "connected":
			connected++
		case "disconnected":
			disconnected++
		case "error":
			error++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":        len(servers),
		"connected":    connected,
		"disconnected": disconnected,
		"error":        error,
	})
}

// RegisterRoutes 注册 MCP 路由。writeMW 会附加到所有写操作路由（POST/DELETE）。
func (h *MCPHandler) RegisterRoutes(router *gin.Engine, writeMW ...gin.HandlerFunc) {
	v1 := router.Group("/api/v1/mcp")

	// 服务器相关
	v1.GET("/servers", h.ListServers)
	v1.GET("/servers/:id", h.GetServer)
	v1.GET("/servers/:id/tools", h.ListTools)
	v1.GET("/servers/:id/resources", h.ListResources)
	v1.POST("/servers", append(writeMW, h.ConnectServer)...)
	v1.DELETE("/servers/:id", append(writeMW, h.DisconnectServer)...)

	// 工具相关
	v1.POST("/tools/:toolId/execute", append(writeMW, h.ExecuteTool)...)

	// Skills 相关
	v1.GET("/skills", h.ListSkills)
	v1.GET("/skills/:id", h.GetSkill)
	v1.POST("/skills/refresh", append(writeMW, h.RefreshSkills)...)

	// 状态相关
	v1.GET("/status", h.GetServerStatus)
}

// ConnectServer connects a new MCP server
// POST /api/v1/mcp/servers
func (h *MCPHandler) ConnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}

	var cfg mcp.MCPServerConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.manager.Connect(c.Request.Context(), &cfg); err != nil {
		h.logger.Error("failed to connect MCP server", zap.String("server_id", cfg.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "connected", "server_id": cfg.ID})
}

// DisconnectServer disconnects an MCP server
// DELETE /api/v1/mcp/servers/:id
func (h *MCPHandler) DisconnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}

	serverID := c.Param("id")
	if err := h.manager.Disconnect(c.Request.Context(), serverID); err != nil {
		h.logger.Error("failed to disconnect MCP server", zap.String("server_id", serverID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "disconnected"})
}
