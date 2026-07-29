package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/internal/platform/domain"
)

type dashboardOverviewService interface {
	Overview(ctx context.Context, tenantID string) (domain.DashboardOverview, error)
}

type DashboardHandler struct {
	service dashboardOverviewService
}

func NewDashboardHandler(service dashboardOverviewService) *DashboardHandler {
	return &DashboardHandler{service: service}
}

func (h *DashboardHandler) Overview(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	overview, err := h.service.Overview(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.DashboardOverviewResponse{
		Agents: overview.Agents, Skills: overview.Skills,
		KnowledgeWorkspaces: overview.KnowledgeWorkspaces, MCPServers: overview.MCPServers,
		ModelProviders: overview.ModelProviders, TenantMembers: overview.TenantMembers,
		Workflows: overview.Workflows, AgentUserMessages7d: overview.AgentUserMessages7d,
	})
}
