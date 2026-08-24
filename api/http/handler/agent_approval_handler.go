package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
)

func (h *AgentHandler) ListToolApprovals(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	// 角色由 service 内 resolver 现查（单事实源），handler 不读 JWT role claim——避免
	// 72h TTL 内成员变动后越权访问审批列表。
	actor, _ := userIDFromCtx(c)
	rows, err := h.svc.ListPendingApprovals(c.Request.Context(), tenantID, actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"approvals": rows})
}

func (h *AgentHandler) DecideToolApproval(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actor, _ := userIDFromCtx(c)
	if err := h.svc.DecideToolApproval(c.Request.Context(), tenantID, c.Param("approvalID"), req.Decision, actor, req.Reason); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": req.Decision})
}

func (h *AgentHandler) ResumeToolApproval(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, _ := userIDFromCtx(c)
	result, _, err := h.svc.ResumeToolApproval(c.Request.Context(), tenantID, actor, c.Param("approvalID"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "completed", "output": result.Output, "steps": result.Steps, "tokensUsed": result.TokensUsed})
}

// ListApprovalHistory 分页查询租户审批历史。角色由 service 内 resolver 现查（单事实源）。
func (h *AgentHandler) ListApprovalHistory(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	actor, _ := userIDFromCtx(c)
	rows, total, err := h.svc.ListApprovalHistory(c.Request.Context(), tenantID, page, pageSize, actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"approvals": rows, "total": total, "page": page, "page_size": pageSize})
}

// GetApprovalDetail 返回单个审批的脱敏详情。角色由 service 内 resolver 现查。
func (h *AgentHandler) GetApprovalDetail(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actor, _ := userIDFromCtx(c)
	detail, err := h.svc.ApprovalDetail(c.Request.Context(), tenantID, c.Param("approvalID"), actor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

// ExecuteApproval 单次消费已批准审批并执行动作。执行者角色由 service 内 resolver
// 现查（单事实源，fail closed），不读 JWT role claim——避免 72h TTL 内成员变动后
// 仍以 admin 身份执行审批（review security）。
func (h *AgentHandler) ExecuteApproval(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.actionExecutor == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("approval executor not configured")))
		return
	}
	actor, _ := userIDFromCtx(c)
	output, err := h.svc.ExecuteApprovedAction(c.Request.Context(), tenantID, c.Param("approvalID"), actor, h.actionExecutor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "executed", "output": output})
}

// SetApprovalAssignee 指定审批人。角色由 service 内 resolver 现查，不读 JWT role claim。
func (h *AgentHandler) SetApprovalAssignee(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req struct {
		AssignedApprover string `json:"assignedApprover" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	actor, _ := userIDFromCtx(c)
	if err := h.svc.SetApprovalAssignee(c.Request.Context(), tenantID, c.Param("approvalID"), req.AssignedApprover, actor); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}
