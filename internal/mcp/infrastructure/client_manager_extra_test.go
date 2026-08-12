package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeMCPClient 是可脚本化的 MCPClient stub。
type fakeMCPClient struct {
	connectErr      error
	disconnectErr   error
	tools           []*MCPTool
	resources       []*MCPResource
	callResult      any
	callErr         error
	listErr         error
	healthy         bool
	lastActivity    time.Time
	disconnectCalls int
	info            *MCPServerInfo
}

func (f *fakeMCPClient) Connect(context.Context) error { return f.connectErr }
func (f *fakeMCPClient) Disconnect(context.Context) error {
	f.disconnectCalls++
	return f.disconnectErr
}
func (f *fakeMCPClient) IsConnected() bool { return true }
func (f *fakeMCPClient) IsHealthy() bool   { return f.healthy }
func (f *fakeMCPClient) LastActivity() time.Time {
	if f.lastActivity.IsZero() {
		return time.Now()
	}
	return f.lastActivity
}
func (f *fakeMCPClient) CallTool(context.Context, string, any) (any, error) {
	return f.callResult, f.callErr
}
func (f *fakeMCPClient) ListTools(context.Context) ([]*MCPTool, error) { return f.tools, f.listErr }
func (f *fakeMCPClient) ListResources(context.Context) ([]*MCPResource, error) {
	return f.resources, nil
}
func (f *fakeMCPClient) GetServerInfo() *MCPServerInfo { return f.info }

func tenantCtx(t *testing.T, tenantID string) context.Context {
	t.Helper()
	return tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{TenantID: tenantID})
}

func newManagerWithFactory(t *testing.T, factory func(*MCPServerConfig, *zap.Logger) MCPClient) *ClientManager {
	t.Helper()
	m := NewClientManager(zap.NewNop(), nil, nil)
	m.clientFactory = factory
	return m
}

func managerWithClients(t *testing.T, tenantID string, clients map[string]MCPClient) *ClientManager {
	t.Helper()
	m := newManagerWithFactory(t, nil)
	for key, c := range clients {
		m.clients[key] = c
		m.configs[key] = &MCPServerConfig{ID: key[len(tenantID)+1:]}
	}
	return m
}

func TestClientManagerConnectRegistersClient(t *testing.T) {
	client := &fakeMCPClient{
		tools:     []*MCPTool{{Name: "search"}},
		resources: []*MCPResource{{URI: "r1"}},
		info:      &MCPServerInfo{ID: "s1"},
	}
	m := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return client })

	cfg := &MCPServerConfig{ID: "s1", Name: "Server 1", Transport: "stdio", Version: "v1"}
	err := m.Connect(tenantCtx(t, "t1"), cfg, nil, "", nil)
	require.NoError(t, err)
	require.Same(t, client, m.GetClient(tenantCtx(t, "t1"), "s1"))
	// 能力已缓存。
	_, ok := m.cache.GetTools("t1:s1")
	require.True(t, ok)

	// 极端情况：重复连接 → 明确报错。
	err = m.Connect(tenantCtx(t, "t1"), cfg, nil, "", nil)
	require.ErrorContains(t, err, "already connected")
}

func TestClientManagerConnectFailures(t *testing.T) {
	// 极端情况：client 连接失败 → 错误返回且不注册。
	client := &fakeMCPClient{connectErr: errors.New("refused")}
	m := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return client })
	err := m.Connect(tenantCtx(t, "t1"), &MCPServerConfig{ID: "s1"}, nil, "", nil)
	require.Error(t, err)
	require.Nil(t, m.GetClient(tenantCtx(t, "t1"), "s1"))

	// 极端情况：scanCapabilities 失败（ListTools 出错）→ 错误返回且不注册。
	badList := &fakeMCPClient{listErr: errors.New("discovery failed")}
	m2 := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return badList })
	err = m2.Connect(tenantCtx(t, "t1"), &MCPServerConfig{ID: "s2"}, nil, "", nil)
	require.ErrorContains(t, err, "discover MCP tools")
	require.Nil(t, m2.GetClient(tenantCtx(t, "t1"), "s2"))

	// 极端情况：checkConnectionLimits 拒绝（租户超限）→ 错误返回。
	m3 := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return client })
	m3.poolConfig.MaxPerTenant = 1
	m3.clients["t1:s3"] = &fakeMCPClient{}
	err = m3.Connect(tenantCtx(t, "t1"), &MCPServerConfig{ID: "s4"}, nil, "", nil)
	require.Error(t, err)
	require.Nil(t, m3.GetClient(tenantCtx(t, "t1"), "s4"))
}

func TestClientManagerDisconnect(t *testing.T) {
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": &fakeMCPClient{}})
	client := m.clients["t1:s1"].(*fakeMCPClient)

	err := m.Disconnect(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Equal(t, 1, client.disconnectCalls)
	require.Nil(t, m.GetClient(tenantCtx(t, "t1"), "s1"))

	// 极端情况：未连接 → 报错。
	err = m.Disconnect(tenantCtx(t, "t1"), "s1")
	require.ErrorContains(t, err, "client not found")
}

func TestClientManagerListToolsAndResourcesUseCache(t *testing.T) {
	client := &fakeMCPClient{
		tools:     []*MCPTool{{Name: "search"}},
		resources: []*MCPResource{{URI: "r1"}},
	}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})

	tools, err := m.ListTools(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	// 第二次走缓存：工具仍然返回，说明缓存命中。
	tools, err = m.ListTools(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Len(t, tools, 1)

	// ListResources 独立于 ListTools 的缓存条目测正常路径。
	m2 := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})
	resources, err := m2.ListResources(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	resources, err = m2.ListResources(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Len(t, resources, 1)
}

func TestClientManagerListResourcesAfterListToolsReturnsEmpty(t *testing.T) {
	// 极端情况（固化当前生产行为，见最终报告）：ListTools 先缓存 entry 后，
	// CapabilityCache.GetResources 只检查 entry 存在性而非 Resources 非空，
	// 导致 TTL 内 ListResources 返回空而非重新拉取。
	client := &fakeMCPClient{
		tools:     []*MCPTool{{Name: "search"}},
		resources: []*MCPResource{{URI: "r1"}},
	}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})
	_, err := m.ListTools(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	resources, err := m.ListResources(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Empty(t, resources)
}

func TestClientManagerGetServerInfoAndQuota(t *testing.T) {
	healthy := &fakeMCPClient{healthy: true, info: &MCPServerInfo{ID: "s1", Name: "A"}}
	dead := &fakeMCPClient{healthy: false, info: &MCPServerInfo{ID: "s2", Name: "B"}}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": healthy, "t1:s2": dead, "t9:s9": &fakeMCPClient{}})

	// 极端情况：未连接 → nil。
	require.Nil(t, m.GetServerInfo(tenantCtx(t, "t1"), "missing"))
	require.Equal(t, "A", m.GetServerInfo(tenantCtx(t, "t1"), "s1").Name)

	q := m.Quota(tenantCtx(t, "t1"))
	require.Equal(t, "t1", q.TenantID)
	require.Equal(t, 2, q.Used)
	require.Equal(t, 1, q.Healthy)
	require.Equal(t, 1, q.Dead)
	require.Equal(t, constants.MCPMaxConnectionsPerTenant, q.Limit)

	// GetAllServerInfo（内存模式）：只返回本租户。
	infos := m.GetAllServerInfo(tenantCtx(t, "t1"))
	require.Len(t, infos, 2)

	// GetAllClients：只返回本租户。
	clients := m.GetAllClients(tenantCtx(t, "t1"))
	require.Len(t, clients, 2)
}

func TestClientManagerGetServerConfigMemoryAndMiss(t *testing.T) {
	m := newManagerWithFactory(t, nil)
	m.configs["t1:s1"] = &MCPServerConfig{ID: "s1", Name: "In Memory"}

	cfg, err := m.GetServerConfig(tenantCtx(t, "t1"), "s1")
	require.NoError(t, err)
	require.Equal(t, "In Memory", cfg.Name)

	// 极端情况：内存 miss + pool nil → ErrServerNotFound。
	_, err = m.GetServerConfig(tenantCtx(t, "t1"), "missing")
	require.ErrorIs(t, err, mcpdomain.ErrServerNotFound)
}

func TestClientManagerUpdateServerSwapsClient(t *testing.T) {
	old := &fakeMCPClient{info: &MCPServerInfo{ID: "s1", Name: "Old"}}
	newClient := &fakeMCPClient{info: &MCPServerInfo{ID: "s1", Name: "New"}}
	var current = old
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": old})
	m.clientFactory = func(*MCPServerConfig, *zap.Logger) MCPClient { return current }

	// 换新 client：旧 client 必须被断开，新 client 注册。
	current = newClient
	err := m.UpdateServer(tenantCtx(t, "t1"), &MCPServerConfig{ID: "s1", Name: "New", Transport: "http"}, "", nil)
	require.NoError(t, err)
	require.Equal(t, 1, old.disconnectCalls)
	require.Same(t, newClient, m.GetClient(tenantCtx(t, "t1"), "s1"))
}

func TestClientManagerDeleteNoPool(t *testing.T) {
	client := &fakeMCPClient{}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})

	err := m.Delete(tenantCtx(t, "t1"), "s1", nil)
	require.NoError(t, err)
	require.Equal(t, 1, client.disconnectCalls)
	require.Nil(t, m.GetClient(tenantCtx(t, "t1"), "s1"))
}

func TestClientManagerRevisionClients(t *testing.T) {
	client := &fakeMCPClient{callResult: map[string]any{"ok": true}, tools: []*MCPTool{{Name: "t1"}}}
	m := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return client })
	cfg := &MCPServerConfig{ID: "rev-1", Transport: "http", URL: "http://x"}

	// CallToolWithConfig：走独立 revision client，不污染 manager 状态。
	result, err := m.CallToolWithConfig(tenantCtx(t, "t1"), cfg, "t1", map[string]any{})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"ok": true}, result)
	require.Nil(t, m.GetClient(tenantCtx(t, "t1"), "rev-1"))

	// ListToolsWithConfig。
	tools, err := m.ListToolsWithConfig(tenantCtx(t, "t1"), cfg)
	require.NoError(t, err)
	require.Len(t, tools, 1)

	// 极端情况：config 不可用 → fail closed。
	// 注：nil config 会触发 CallToolWithConfig 内 config.ID 的 nil 解引用 panic
	// （生产缺陷，见最终报告），此处用空 ID config 验证同一 fail-closed 分支。
	emptyCfg := &MCPServerConfig{}
	_, err = m.CallToolWithConfig(tenantCtx(t, "t1"), emptyCfg, "t1", nil)
	require.ErrorContains(t, err, "configuration unavailable")
	_, err = m.ListToolsWithConfig(tenantCtx(t, "t1"), emptyCfg)
	require.ErrorContains(t, err, "configuration unavailable")

	// 极端情况：client connect 失败 → 错误传播。
	bad := &fakeMCPClient{connectErr: errors.New("down")}
	m2 := newManagerWithFactory(t, func(*MCPServerConfig, *zap.Logger) MCPClient { return bad })
	_, err = m2.CallToolWithConfig(tenantCtx(t, "t1"), cfg, "t1", nil)
	require.ErrorContains(t, err, "connect")
}

func TestMCPToolCatalogAddGetAndDiscover(t *testing.T) {
	logger := zap.NewNop()
	client := &fakeMCPClient{tools: []*MCPTool{{Name: "search", Description: "Search"}}}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})
	catalog := NewMCPToolCatalog("s1", m, logger)

	// AddToolForTest：直接注入。
	w := &MCPToolHandle{ID: "mcp:s1:direct", Name: "direct", Type: "mcp"}
	catalog.AddToolForTest(w)
	require.Same(t, w, catalog.GetRegisteredTool("mcp:s1:direct"))
	require.Nil(t, catalog.GetRegisteredTool("missing"))
	require.Len(t, catalog.GetAllTools(), 1)

	// DiscoverTools：走 manager.ListTools 缓存命中路径。
	handles, err := catalog.DiscoverTools(tenantCtx(t, "t1"))
	require.NoError(t, err)
	require.Len(t, handles, 1)
	require.Equal(t, "mcp:s1:search", handles[0].ID)
	require.Same(t, handles[0], catalog.GetRegisteredTool("mcp:s1:search"))
}

func TestMCPToolRegistryRegisterUnregister(t *testing.T) {
	logger := zap.NewNop()
	client := &fakeMCPClient{tools: []*MCPTool{{Name: "search"}}}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})
	r := NewMCPToolRegistry(m, logger)

	// 预注入 adapter 后重复注册 → 明确报错。
	adapter := NewMCPToolCatalog("s1", m, logger)
	r.RegisterCatalogForTest("s1", adapter)
	require.Same(t, adapter, r.GetCatalogForServer("s1"))
	require.Nil(t, r.GetCatalogForServer("missing"))
	err := r.RegisterServer(tenantCtx(t, "t1"), "s1")
	require.ErrorContains(t, err, "already registered")

	// 未注册 server 的 RegisterServer → DiscoverTools 失败（无 client）→ 错误传播。
	err = r.RegisterServer(tenantCtx(t, "t1"), "ghost")
	require.Error(t, err)

	// UnregisterServer：已注册 → 删除；未注册 → nil。
	require.NoError(t, r.UnregisterServer("s1"))
	require.Nil(t, r.GetCatalogForServer("s1"))
	require.NoError(t, r.UnregisterServer("s1"))

	// GetRegisteredTool / GetAllTools 跨 adapter 汇总。
	r.RegisterCatalogForTest("s2", NewMCPToolCatalog("s2", m, logger))
	r.GetCatalogForServer("s2").AddToolForTest(&MCPToolHandle{ID: "mcp:s2:x"})
	require.NotNil(t, r.GetRegisteredTool("mcp:s2:x"))
	require.Nil(t, r.GetRegisteredTool("mcp:s2:missing"))
	require.Len(t, r.GetAllTools(), 1)

	// ExecuteToolByID：未注册 → 报错；已注册（Manager nil 走 panic 前先报错？）→ 未注册错误。
	_, err = r.ExecuteToolByID("mcp:s2:missing", nil)
	require.ErrorContains(t, err, "skill not found")
}

func TestMCPToolRegistryRefreshAndServerInfo(t *testing.T) {
	logger := zap.NewNop()
	client := &fakeMCPClient{tools: []*MCPTool{{Name: "search"}}, info: &MCPServerInfo{ID: "s1", Name: "A"}}
	m := managerWithClients(t, "t1", map[string]MCPClient{"t1:s1": client})
	r := NewMCPToolRegistry(m, logger)

	// RefreshTools：adapter 刷新失败（无 client 的 ghost server）→ warn 不返回错误。
	r.RegisterCatalogForTest("ghost", NewMCPToolCatalog("ghost", m, logger))
	require.NoError(t, r.RefreshTools(tenantCtx(t, "t1")))

	// GetServerInfo / GetAllServerInfo 转发到 manager。
	require.Nil(t, r.GetServerInfo(tenantCtx(t, "t1"), "missing"))
	got, ok := r.GetServerInfo(tenantCtx(t, "t1"), "s1").(*MCPServerInfo)
	require.True(t, ok)
	require.Equal(t, "A", got.Name)
	infos := r.GetAllServerInfo(tenantCtx(t, "t1"))
	require.Len(t, infos, 1)
}

func TestToolRegistryAsPortAdapter(t *testing.T) {
	logger := zap.NewNop()
	m := newManagerWithFactory(t, nil)
	r := NewMCPToolRegistry(m, logger)
	adapter := ToolRegistryAsPort(r)

	// UnregisterServer：未注册 → nil error。
	require.NoError(t, adapter.UnregisterServer("missing"))
	// RegisterServer：注册不存在的 server → 发现失败错误传播。
	require.Error(t, adapter.RegisterServer(tenantCtx(t, "t1"), "ghost"))
}
