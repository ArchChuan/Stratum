package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/internal/platform/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

type dashboardServiceFake struct {
	tenantID string
}

func (f *dashboardServiceFake) Overview(_ context.Context, tenantID string) (domain.DashboardOverview, error) {
	f.tenantID = tenantID
	return domain.DashboardOverview{
		Agents: 1, Skills: 2, KnowledgeWorkspaces: 3, MCPServers: 4,
		ModelProviders: 5, TenantMembers: 6, Workflows: 7, AgentUserMessages7d: 8,
	}, nil
}

func TestDashboardHandlerOverview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &dashboardServiceFake{}
	router := gin.New()
	router.GET("/dashboard/overview", func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
	}, NewDashboardHandler(service).Overview)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dashboard/overview", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.tenantID != "tenant-1" {
		t.Fatalf("tenantID=%q", service.tenantID)
	}
	var got map[string]int
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["agents"] != 1 || got["model_providers"] != 5 || got["agent_user_messages_7d"] != 8 {
		t.Fatalf("response=%v", got)
	}
}
