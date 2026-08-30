// Package handler implements HTTP API request handlers.

package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// mcpRoleResolver 现查 actor 的租户角色（单事实源）。wiring 注入 tenantRoleAdapter；
// resolver 缺失时 currentRole fail closed（返回 "" → 403），不信任 JWT role claim。
type mcpRoleResolver interface {
	ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error)
}

// mcpApprovalService 创建审批请求（D5 member 配置写操作分流）。
// *agentapp.ToolApprovalService 满足该接口。
type mcpApprovalService interface {
	Request(ctx context.Context, payload agentapp.ToolApprovalPayload) (string, error)
}

// policyVersionMCPAction 标记 MCP 配置动作审批的策略版本（审批协议演进锚点；
// Digest 校验使其与创建时版本强绑定）。
const policyVersionMCPAction = "action-v1"

// MCPHandler 处理 MCP 相关的 HTTP 请求
type MCPHandler struct {
	svc       *mcpapp.MCPService
	approvals mcpApprovalService
	roles     mcpRoleResolver
	logger    *zap.Logger
}

// NewMCPHandler 创建新的 MCP 处理器。
func NewMCPHandler(svc *mcpapp.MCPService, logger *zap.Logger) *MCPHandler {
	return &MCPHandler{svc: svc, logger: logger.Named("handler.mcp")}
}

// WithApprovalService 注入审批服务（wiring 提供）。未注入时 member 审批路径 fail closed（503）。
func (h *MCPHandler) WithApprovalService(service mcpApprovalService) *MCPHandler {
	h.approvals = service
	return h
}

// WithRoleResolver 注入租户角色现查 resolver（wiring 提供 DB-backed adapter）。
// resolver 未注入时 currentRole fail closed（返回 "" → 403），不信任 JWT role claim。
func (h *MCPHandler) WithRoleResolver(roles mcpRoleResolver) *MCPHandler {
	h.roles = roles
	return h
}

// currentRole 现查 actor 的租户角色（单事实源，fail closed）。identity 缺失、resolver
// 未装配或解析失败一律返回 "" → 403，不信任 JWT role claim（claim 在签发时固化，无
// 法反映签发后降级；与 ToolApprovalService.resolveRole 的 fail-closed 语义一致）。
// SetToolPolicy 是唯一无 service 层角色门禁的 MCP 写方法，claim 回退会让被降级的
// 用户在 token 有效期内直接降低工具风险等级（评审 MEDIUM）。生产 wiring 必注入
// DB-backed resolver；resolver 缺失时分类 fail closed 而非默认放行。
func (h *MCPHandler) currentRole(c *gin.Context) string {
	if h.roles == nil {
		return ""
	}
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		return ""
	}
	actor, ok := userIDFromCtx(c)
	if !ok {
		return ""
	}
	role, err := h.roles.ResolveTenantRole(c.Request.Context(), tenantID, actor)
	if err != nil {
		return ""
	}
	return role
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
	// member 路径把 riskLevel 固化进审批 payload：此处先行校验枚举，避免审批
	// 通过后执行器终态失败（unknown_outcome，不可重试）把合法审批烧掉。
	if err := req.RiskLevel.Validate(); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	updatedBy := ""
	if tc, ok := postgres.FromContext(c.Request.Context()); ok {
		updatedBy = tc.UserID
	}
	serverID, toolName := c.Param("serverId"), c.Param("toolName")
	args := map[string]any{"operation": "set_tool_policy", "serverId": serverID, "toolName": toolName, "riskLevel": string(req.RiskLevel)}
	h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPPolicy, "mcp.set_tool_policy", args, http.StatusOK, func() (any, error) {
		if err := h.svc.SetToolPolicy(c.Request.Context(), mcpdomain.ToolPolicy{
			ServerID: serverID, ToolName: toolName, RiskLevel: req.RiskLevel, UpdatedBy: updatedBy,
		}); err != nil {
			return nil, middleware.NewHTTPError(http.StatusBadRequest, err)
		}
		return gin.H{"message": "updated"}, nil
	})
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

// GetQuota GET /mcp/quota
func (h *MCPHandler) GetQuota(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.GetQuota(c.Request.Context()))
}

// GetServerStatus GET /mcp/status
func (h *MCPHandler) GetServerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.ServerStatus(c.Request.Context()))
}

// RegisterRoutes 注册 MCP 路由。
// mw       — JWT + InjectTenantContext (+ member 底线)，挂在所有 /mcp 路由上（读写均要租户上下文）。
// writeMW  — 配置写操作再追加（RequireActiveTenant）：D5 5 个角色分流写方法 + 工具执行。
//
//	挂载后 suspended tenant 的 admin/owner 不能直接改配置、member 不能创建审批。
//
// adminMW  — 运行时管理操作（断开/重连/读取完整配置）再追加，仅 admin+ 可用。
//
// D5：连接/更新/删除配置/设置编辑者/设置工具策略 5 个写操作从 adminMW 移出，改 handler 内
// 角色分流（admin/owner 直接执行；member 创建 mcp_policy/mcp_server 审批返回 202 pending）。
// 普通租户成员只能读取非敏感服务器信息、执行工具，以及发起需审批的配置变更。
func (h *MCPHandler) RegisterRoutes(router *gin.Engine, mw []gin.HandlerFunc, writeMW []gin.HandlerFunc, adminMW []gin.HandlerFunc) {
	// clone 避免多次 append 复用底层数组造成中间件串味。
	admin := func(handlers ...gin.HandlerFunc) []gin.HandlerFunc {
		out := make([]gin.HandlerFunc, 0, len(adminMW)+len(handlers))
		out = append(out, adminMW...)
		return append(out, handlers...)
	}
	write := func(handlers ...gin.HandlerFunc) []gin.HandlerFunc {
		out := make([]gin.HandlerFunc, 0, len(writeMW)+len(handlers))
		out = append(out, writeMW...)
		return append(out, handlers...)
	}

	v1 := router.Group("/mcp", mw...)
	v1.GET("/servers", h.ListServers)
	v1.GET("/servers/:id", h.GetServer)
	v1.GET("/servers/:id/tools", h.ListTools)
	v1.GET("/tool-policies", h.ListToolPolicies)
	v1.PUT("/tool-policies/:serverId/:toolName", write(h.SetToolPolicy)...)
	v1.GET("/servers/:id/resources", h.ListResources)
	v1.POST("/servers", write(h.ConnectServer)...)
	v1.PUT("/servers/:id", write(h.UpdateServer)...)
	v1.PUT("/servers/:id/editors", write(h.SetMCPServerEditors)...)
	v1.GET("/servers/:id/config", write(h.GetServerConfig)...)
	v1.DELETE("/servers/:id", admin(h.DisconnectServer)...)
	v1.DELETE("/servers/:id/config", write(h.DeleteServerConfig)...)
	v1.POST("/servers/:id/reconnect", admin(h.ReconnectServer)...)
	v1.GET("/status", h.GetServerStatus)
	v1.GET("/quota", h.GetQuota)
}

// mcpApprovalPayload 构造 MCP 配置动作审批 payload（D5：member 发起 → 审批）。
// tenant/user 缺失时返回 401（身份上下文必须齐备，否则无法归属审批请求）。
func mcpApprovalPayload(c *gin.Context, subjectKind, toolName string, args map[string]any) (agentapp.ToolApprovalPayload, error) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		return agentapp.ToolApprovalPayload{}, middleware.NewHTTPError(http.StatusUnauthorized, errMissingTenant)
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		return agentapp.ToolApprovalPayload{}, middleware.NewHTTPError(http.StatusUnauthorized, errMissingUser)
	}
	return agentapp.ToolApprovalPayload{
		TenantID: tenantID, UserID: userID,
		ExecutionID: uuid.NewString(), ToolCallID: uuid.NewString(),
		ToolName: toolName, SubjectKind: subjectKind,
		RiskLevel: agentdomain.ToolRiskUnclassified, Arguments: args, PolicyVersion: policyVersionMCPAction,
	}, nil
}

// requestApprovalForMember 创建审批并写 202 响应；返回 true 表示响应已由审批路径消化。
func (h *MCPHandler) requestApprovalForMember(c *gin.Context, subjectKind, toolName string, args map[string]any) (bool, error) {
	if h.approvals == nil {
		return false, middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("approval service unavailable"))
	}
	payload, err := mcpApprovalPayload(c, subjectKind, toolName, args)
	if err != nil {
		return false, err
	}
	id, err := h.approvals.Request(c.Request.Context(), payload)
	if err != nil {
		return false, err
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "pending_approval", "approval_id": id})
	return true, nil
}

// requireApprovalOrExecuteMCP 角色分流：admin/owner 直接执行；member 创建审批；
// 角色未知/resolver 缺失 fail closed（拒绝）。角色由 resolver 现查（单事实源，
// 不信任 JWT claim 的陈旧窗口）。execute 成功时以 successStatus 写响应。
func (h *MCPHandler) requireApprovalOrExecuteMCP(c *gin.Context, subjectKind, toolName string, args map[string]any, successStatus int, execute func() (any, error)) {
	roleClass := h.currentRole(c)
	if roleClass != "admin" && roleClass != "owner" {
		if roleClass == "member" {
			handled, err := h.requestApprovalForMember(c, subjectKind, toolName, args)
			if err != nil {
				_ = c.Error(err)
				return
			}
			if handled {
				return
			}
		}
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("insufficient tenant role")))
		return
	}
	result, err := execute()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(successStatus, result)
}

// GetServerConfig GET /mcp/servers/:id/config
func (h *MCPHandler) GetServerConfig(c *gin.Context) {
	cfg, err := h.svc.GetServerConfig(c.Request.Context(), c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	response := gen.NewMCPServerConfigResponse(cfg)
	if tenantID, ok := tenantIDFromCtx(c); ok {
		editors, listErr := h.svc.ListEditors(c.Request.Context(), tenantID, c.Param("id"))
		if listErr != nil {
			h.logger.Error("failed to list MCP server editors",
				zap.String("trace_id", middleware.GetTraceID(c)),
				zap.String("server_id", c.Param("id")),
				zap.Error(listErr))
			_ = c.Error(listErr)
			return
		}
		response.Editors = editors
	}
	c.JSON(http.StatusOK, response)
}

// UpdateServer PUT /mcp/servers/:id
func (h *MCPHandler) UpdateServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var req gen.MCPServerConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	cfg, err := req.ServerConfig()
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	cfg.ID = c.Param("id")
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	args := map[string]any{"operation": "update_server", "config": req, "serverId": cfg.ID}
	h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPServer, "mcp.update_server", args, http.StatusOK, func() (any, error) {
		if err := h.svc.UpdateServer(c.Request.Context(), cfg, actorID); err != nil {
			return nil, err
		}
		return gin.H{"message": "updated", "server_id": cfg.ID}, nil
	})
}

// ConnectServer POST /mcp/servers
func (h *MCPHandler) ConnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var req gen.MCPServerConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	cfg, err := req.ServerConfig()
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	args := map[string]any{"operation": "connect_server", "config": req, "editors": req.Editors}
	h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPServer, "mcp.connect_server", args, http.StatusCreated, func() (any, error) {
		if err := h.svc.ConnectServer(context.WithoutCancel(c.Request.Context()), cfg, req.Editors, actorID); err != nil {
			return nil, err
		}
		return gin.H{"message": "connected", "server_id": cfg.ID}, nil
	})
}

// DisconnectServer DELETE /mcp/servers/:id
func (h *MCPHandler) DisconnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	serverID := c.Param("id")
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	if err := h.svc.DisconnectServer(c.Request.Context(), serverID, actorID); err != nil {
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
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	args := map[string]any{"operation": "delete_server", "serverId": serverID}
	h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPServer, "mcp.delete_server", args, http.StatusOK, func() (any, error) {
		if err := h.svc.DeleteServer(c.Request.Context(), serverID, actorID); err != nil {
			return nil, err
		}
		return gin.H{"message": "deleted"}, nil
	})
}

// SetMCPServerEditors PUT /mcp/servers/:id/editors
func (h *MCPHandler) SetMCPServerEditors(c *gin.Context) {
	var req struct {
		EditorIDs []string `json:"editorIds" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	serverID := c.Param("id")
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	args := map[string]any{"operation": "set_editors", "serverId": serverID, "editorIds": req.EditorIDs}
	h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPServer, "mcp.set_editors", args, http.StatusOK, func() (any, error) {
		if err := h.svc.SetEditors(c.Request.Context(), serverID, actorID, req.EditorIDs); err != nil {
			return nil, err
		}
		return gin.H{"message": "editors updated"}, nil
	})
}

// ReconnectServer POST /mcp/servers/:id/reconnect
func (h *MCPHandler) ReconnectServer(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	serverID := c.Param("id")
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	if err := h.svc.ReconnectServer(context.WithoutCancel(c.Request.Context()), serverID, actorID); err != nil {
		h.logger.Error("failed to reconnect MCP server",
			zap.String("trace_id", middleware.GetTraceID(c)),
			zap.String("server_id", serverID),
			zap.Error(err))
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "connected", "server_id": serverID})
}
