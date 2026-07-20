package wiring

import (
	"context"
	"testing"

	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"go.uber.org/zap"
)

func TestMCPAgentToolAdapterKeepsStableExposedIDAndRawToolName(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPToolRegistry(manager, logger)
	catalog := mcp.NewMCPToolCatalog("orders", manager, logger)
	catalog.AddToolForTest(&mcp.MCPToolHandle{
		ID: "mcp:orders:get_order", Name: "get_order",
		Tool:     &mcp.MCPTool{Name: "get_order", Description: "get"},
		ServerID: "orders", Manager: manager,
	})
	registry.RegisterCatalogForTest("orders", catalog)

	tools := (mcpAgentToolAdapter{registry: registry}).ToolsForServer(context.Background(), "orders")
	if len(tools) != 1 || tools[0].Name != "mcp:orders:get_order" || tools[0].CapabilityID != "get_order" {
		t.Fatalf("unexpected tool definition: %#v", tools)
	}
}
