package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestMCPAgentToolAdapterKeepsStableExposedIDAndRawToolName(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPToolRegistry(manager, logger)
	catalog := mcp.NewMCPToolCatalog("tenant-1", "orders", manager, logger)
	catalog.AddToolForTest(&mcp.MCPToolHandle{
		ID: "mcp:orders:get_order", Name: "get_order",
		Tool: &mcp.MCPTool{Name: "get_order", Description: "get"},
	})
	registry.RegisterCatalogForTest("tenant-1", "orders", catalog)

	tools := (mcpAgentToolAdapter{registry: registry}).ToolsForServer(context.Background(), "tenant-1", "orders")
	if len(tools) != 1 || tools[0].Name != "mcp:orders:get_order" || tools[0].CapabilityID != "get_order" {
		t.Fatalf("unexpected tool definition: %#v", tools)
	}
}

// TestMCPAgentToolAdapterFallsBackToLiveDiscoveryOnMissingCatalog 验证防御兜底：
// catalog 缺失（backend 重启后未重建，或从未经 admin 写入触发注册）时，
// ToolsForServer 实时走 EnsureCatalog 发现；发现失败（无 client 等）显式 warn
// 并返回 nil，不再静默丢工具。真正的成功兜底路径由 EnsureCatalog 幂等测试与
// 远端 E2E 覆盖。
func TestMCPAgentToolAdapterFallsBackToLiveDiscoveryOnMissingCatalog(t *testing.T) {
	manager := mcp.NewClientManager(zap.NewNop(), nil, nil)
	registry := mcp.NewMCPToolRegistry(manager, zap.NewNop())
	core, logs := observer.New(zapcore.WarnLevel)
	adapter := mcpAgentToolAdapter{registry: registry, logger: zap.New(core)}

	tools := adapter.ToolsForServer(context.Background(), "tenant-1", "ghost")

	require.Nil(t, tools)
	warns := logs.FilterMessage("MCP tool catalog missing; live discovery failed")
	require.Equal(t, 1, warns.Len(), "fallback failure must be logged explicitly, not silently dropped")
}

type stubMCPClientResolver struct {
	client mcp.MCPClient
}

func (r stubMCPClientResolver) GetClient(context.Context, string) mcp.MCPClient { return r.client }

func (r stubMCPClientResolver) GetOrRestoreClient(_ context.Context, _ string) (mcp.MCPClient, error) {
	if r.client == nil {
		return nil, errors.New("client not found")
	}
	return r.client, nil
}

type failingAgentMCPClient struct {
	err error
}

func (c failingAgentMCPClient) Connect(context.Context) error     { return nil }
func (c failingAgentMCPClient) Disconnect(context.Context) error  { return nil }
func (c failingAgentMCPClient) IsConnected() bool                 { return true }
func (c failingAgentMCPClient) IsHealthy() bool                   { return true }
func (c failingAgentMCPClient) HealthCheck(context.Context) error { return nil }
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
