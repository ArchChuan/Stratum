package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type invocationSignerFake struct {
	claims platformmcp.InvocationClaims
}

func (f *invocationSignerFake) SignInvocation(claims platformmcp.InvocationClaims, _ time.Duration) (string, error) {
	f.claims = claims
	return "signed", nil
}

func TestPlatformMCPInvocationCredentialsBindExecutionIdentity(t *testing.T) {
	signer := &invocationSignerFake{}
	provider := platformMCPInvocationCredentials{tokens: signer}
	ctx := platformmcp.WithInvocationContext(t.Context(), platformmcp.InvocationContext{
		TenantID: "tenant-1", UserID: "user-1", AgentID: platformmcp.SystemAssistantID,
		ExecutionID: "execution-1", ApprovalID: "approval-1",
	})

	authorization, err := provider.Authorization(
		ctx, platformmcp.SystemServerID, "stratum_propose_resource_change",
	)

	require.NoError(t, err)
	require.Equal(t, "Bearer signed", authorization)
	require.Equal(t, "tenant-1", signer.claims.TenantID)
	require.Equal(t, "execution-1", signer.claims.ExecutionID)
	require.Equal(t, "approval-1", signer.claims.ApprovalID)
	require.NotEmpty(t, signer.claims.ID)
}

func TestPlatformMCPInvocationCredentialsFailClosedWithoutExecutionIdentity(t *testing.T) {
	provider := platformMCPInvocationCredentials{tokens: &invocationSignerFake{}}

	_, err := provider.Authorization(
		t.Context(), platformmcp.SystemServerID, "stratum_diagnose_tenant",
	)

	require.Error(t, err)
}

func TestMCPAgentToolAdapterKeepsStableExposedIDAndRawToolName(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil, "")
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
func (failingAgentMCPClient) LastActivity() time.Time             { return time.Now() }

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

type mcpRuntimeRevisionLoaderFake struct {
	revision mcpRuntimeRevision
	tenantID string
}

func (f *mcpRuntimeRevisionLoaderFake) LoadMCPRuntimeRevision(
	_ context.Context, tenantID, _, _ string,
) (mcpRuntimeRevision, error) {
	f.tenantID = tenantID
	return f.revision, nil
}

type revisionRuntimeFake struct {
	calls  int
	config *mcpdomain.ServerConfig
	err    error
}

func (f *revisionRuntimeFake) ListToolsWithConfig(
	context.Context, *mcpdomain.ServerConfig,
) ([]*mcpdomain.Tool, error) {
	return nil, nil
}

func (f *revisionRuntimeFake) CallToolWithConfig(
	_ context.Context, config *mcpdomain.ServerConfig, _ string, _ any,
) (any, error) {
	f.calls++
	f.config = config
	if f.err != nil {
		return nil, f.err
	}
	return map[string]any{"status": "ok"}, nil
}

func TestAgentMCPExecutorUsesExactRuntimeRevision(t *testing.T) {
	loader := &mcpRuntimeRevisionLoaderFake{revision: mcpRuntimeRevision{
		Config: &mcpdomain.ServerConfig{ID: "server-1"}, EnabledTools: []string{"lookup"},
		Timeout: 1500 * time.Millisecond,
	}}
	runtime := &revisionRuntimeFake{}
	executor := agentMCPExecutor{revisionRuntime: runtime, revisions: loader}
	ctx := postgres.WithTenant(context.Background(), &postgres.TenantContext{
		TenantID: "tenant-1", UserID: "user-1", Role: postgres.RoleTenantUser,
	})

	result, err := executor.ExecuteMCPToolRevision(
		ctx, "server-1", "lookup", "revision-1", agentport.ToolRiskRead, map[string]any{},
	)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"status": "ok"}, result.StructuredContent)
	require.Equal(t, "tenant-1", loader.tenantID)
	require.Same(t, loader.revision.Config, runtime.config)
}

func TestAgentMCPExecutorRejectsToolDisabledByRevisionBeforeSending(t *testing.T) {
	loader := &mcpRuntimeRevisionLoaderFake{revision: mcpRuntimeRevision{
		Config: &mcpdomain.ServerConfig{ID: "server-1"}, EnabledTools: []string{}, Timeout: time.Second,
	}}
	runtime := &revisionRuntimeFake{}
	executor := agentMCPExecutor{revisionRuntime: runtime, revisions: loader}
	ctx := postgres.WithTenant(context.Background(), &postgres.TenantContext{TenantID: "tenant-1"})

	_, err := executor.ExecuteMCPToolRevision(
		ctx, "server-1", "lookup", "revision-1", agentport.ToolRiskRead, map[string]any{},
	)
	require.Error(t, err)
	require.Zero(t, runtime.calls)
}

func TestAgentMCPExecutorRetriesOnlyReadTools(t *testing.T) {
	for _, tc := range []struct {
		name      string
		risk      agentport.ToolRiskLevel
		wantCalls int
	}{
		{name: "read", risk: agentport.ToolRiskRead, wantCalls: 3},
		{name: "destructive", risk: agentport.ToolRiskDestructive, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := &mcpRuntimeRevisionLoaderFake{revision: mcpRuntimeRevision{
				Config: &mcpdomain.ServerConfig{ID: "server-1"}, EnabledTools: []string{"lookup"},
				Timeout: time.Second, MaxRetries: 2,
			}}
			runtime := &revisionRuntimeFake{err: errors.New("dependency unavailable")}
			executor := agentMCPExecutor{revisionRuntime: runtime, revisions: loader}
			ctx := postgres.WithTenant(context.Background(), &postgres.TenantContext{TenantID: "tenant-1"})

			_, err := executor.ExecuteMCPToolRevision(
				ctx, "server-1", "lookup", "revision-1", tc.risk, map[string]any{},
			)
			require.Error(t, err)
			require.Equal(t, tc.wantCalls, runtime.calls)
		})
	}
}
