package handler

import (
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
)

func (h *AgentHandler) ListToolApprovals(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	rows, err := h.svc.ListPendingApprovals(c.Request.Context(), tenantID)
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
	result, _, err := h.svc.ResumeToolApproval(c.Request.Context(), tenantID, c.Param("approvalID"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "completed", "output": result.Output, "steps": result.Steps, "tokensUsed": result.TokensUsed})
}
