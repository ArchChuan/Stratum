package wiring

import (
	"context"
	"errors"
	"testing"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/stretchr/testify/require"
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

type stubMCPClientResolver struct {
	client mcp.MCPClient
}

func (r stubMCPClientResolver) GetClient(context.Context, string) mcp.MCPClient { return r.client }

type failingAgentMCPClient struct {
	err error
}

func (c failingAgentMCPClient) Connect(context.Context) error    { return nil }
func (c failingAgentMCPClient) Disconnect(context.Context) error { return nil }
func (c failingAgentMCPClient) IsConnected() bool                { return true }
func (c failingAgentMCPClient) IsHealthy() bool                  { return true }
func (c failingAgentMCPClient) CallTool(context.Context, string, interface{}) (interface{}, error) {
	return nil, c.err
}
func (c failingAgentMCPClient) ListTools(context.Context) ([]*mcp.MCPTool, error) { return nil, nil }
func (c failingAgentMCPClient) ListResources(context.Context) ([]*mcp.MCPResource, error) {
	return nil, nil
}
func (c failingAgentMCPClient) GetServerInfo() *mcp.MCPServerInfo { return &mcp.MCPServerInfo{} }

func TestAgentMCPExecutorClassifiesMissingClientAsNotSent(t *testing.T) {
	_, err := (agentMCPExecutor{clients: stubMCPClientResolver{}}).ExecuteMCPTool(
		context.Background(), "missing", "delete", map[string]any{},
	)

	var executionErr *agentport.MCPToolExecutionError
	require.ErrorAs(t, err, &executionErr)
	require.Equal(t, agentport.ToolExecutionOutcomeNotSent, executionErr.Outcome)
}

func TestAgentMCPExecutorClassifiesClientErrorAsUnknown(t *testing.T) {
	transportErr := errors.New("response timeout")
	_, err := (agentMCPExecutor{clients: stubMCPClientResolver{
		client: failingAgentMCPClient{err: transportErr},
	}}).ExecuteMCPTool(context.Background(), "orders", "delete", map[string]any{})

	var executionErr *agentport.MCPToolExecutionError
	require.ErrorAs(t, err, &executionErr)
	require.ErrorIs(t, err, transportErr)
	require.Equal(t, agentport.ToolExecutionOutcomeUnknown, executionErr.Outcome)
}
