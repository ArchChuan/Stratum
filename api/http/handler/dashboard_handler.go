package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
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
	//nolint:gosec // 仪表盘计数不可能溢出 int32(proto 契约)
	c.JSON(http.StatusOK, gen.DashboardOverviewResponse{
		Agents: int32(overview.Agents), Skills: int32(overview.Skills),
		KnowledgeWorkspaces: int32(overview.KnowledgeWorkspaces), MCPServers: int32(overview.MCPServers),
		ModelProviders: int32(overview.ModelProviders), TenantMembers: int32(overview.TenantMembers),
		Workflows: int32(overview.Workflows), AgentUserMessages7d: int32(overview.AgentUserMessages7d),
	})
}
