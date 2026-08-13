// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// MCPToolHandle 将 MCP 工具包装为 Tool。agent 生产路径只消费
// GetID/GetName/GetDescription/GetType 与 Tool 元数据；工具执行经
// agentMCPExecutor 走 ClientManager（tenant 作用域），不经 handle。
type MCPToolHandle struct {
	ID          string
	Name        string
	Description string
	Type        string
	Tool        *MCPTool
}

// GetID 获取 ID
func (w *MCPToolHandle) GetID() string {
	return w.ID
}

// GetName 获取名称
func (w *MCPToolHandle) GetName() string {
	return w.Name
}

// GetDescription 获取描述
func (w *MCPToolHandle) GetDescription() string {
	return w.Description
}

// GetType 获取类型
func (w *MCPToolHandle) GetType() string {
	return w.Type
}

// MCPToolCatalog 适配器，管理 MCP Tools
type MCPToolCatalog struct {
	tenantID string
	serverID string
	manager  *ClientManager
	tools    map[string]*MCPToolHandle
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewMCPToolCatalog 创建新的适配器
func NewMCPToolCatalog(tenantID, serverID string, manager *ClientManager, logger *zap.Logger) *MCPToolCatalog {
	return &MCPToolCatalog{
		tenantID: tenantID,
		serverID: serverID,
		manager:  manager,
		tools:    make(map[string]*MCPToolHandle),
		logger:   logger.Named("mcp.tool_catalog").With(zap.String("tenant_id", tenantID), zap.String("server_id", serverID)),
	}
}

// DiscoverTools 发现并创建 Tools
func (a *MCPToolCatalog) DiscoverTools(ctx context.Context) ([]*MCPToolHandle, error) {
	discovered, err := a.manager.ListTools(ctx, a.serverID)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	handles := make([]*MCPToolHandle, 0, len(discovered))
	for _, tool := range discovered {
		toolID := fmt.Sprintf("mcp:%s:%s", a.serverID, tool.Name)

		wrapper := &MCPToolHandle{
			ID:          toolID,
			Name:        tool.Name,
			Description: tool.Description,
			Type:        "mcp",
			Tool:        tool,
		}

		a.tools[toolID] = wrapper
		handles = append(handles, wrapper)
	}

	a.logger.Info("discovered MCP tools", zap.Int("count", len(handles)))
	return handles, nil
}

// AddToolForTest injects a wrapper directly into the adapter without MCP discovery.
// Intended for unit tests only.
func (a *MCPToolCatalog) AddToolForTest(w *MCPToolHandle) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools[w.ID] = w
}

// GetRegisteredTool 获取 Tool
func (a *MCPToolCatalog) GetRegisteredTool(toolID string) *MCPToolHandle {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if wrapper, exists := a.tools[toolID]; exists {
		return wrapper
	}
	return nil
}

// GetAllTools 获取所有 Tools
func (a *MCPToolCatalog) GetAllTools() []*MCPToolHandle {
	a.mu.RLock()
	defer a.mu.RUnlock()

	tools := make([]*MCPToolHandle, 0, len(a.tools))
	for _, wrapper := range a.tools {
		tools = append(tools, wrapper)
	}
	return tools
}

// MCPToolRegistry 管理所有 MCP Tools
type MCPToolRegistry struct {
	adapters map[string]*MCPToolCatalog
	manager  *ClientManager
	mu       sync.RWMutex
	logger   *zap.Logger
}

// registryKey scopes registry entries to one tenant. mcp_configs.id is only
// unique within a tenant schema, so two tenants may name their server the same;
// keying by tenantID:serverID prevents cross-tenant overwrite/leak.
func registryKey(tenantID, serverID string) string {
	return tenantID + ":" + serverID
}

// GetCatalogForServer returns the adapter for a specific server, or nil if not
// registered. An empty tenantID fails closed: without a tenant the registry key
// would collapse into a shared "":serverID bucket and cross-tenant lookups
// could resolve another tenant's catalog.
func (r *MCPToolRegistry) GetCatalogForServer(tenantID, serverID string) *MCPToolCatalog {
	if tenantID == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[registryKey(tenantID, serverID)]
}

// RegisterCatalogForTest injects a pre-built adapter directly, bypassing DiscoverTools.
// Intended for unit tests only.
func (r *MCPToolRegistry) RegisterCatalogForTest(tenantID, serverID string, adapter *MCPToolCatalog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[registryKey(tenantID, serverID)] = adapter
}

// NewMCPToolRegistry 创建新的注册表
func NewMCPToolRegistry(manager *ClientManager, logger *zap.Logger) *MCPToolRegistry {
	return &MCPToolRegistry{
		adapters: make(map[string]*MCPToolCatalog),
		manager:  manager,
		logger:   logger.Named("mcp.tool_registry"),
	}
}

// RegisterServer 注册 MCP 服务器
func (r *MCPToolRegistry) RegisterServer(ctx context.Context, tenantID, serverID string) error {
	if tenantID == "" {
		// fail closed: without a tenant the entry would land in the shared
		// "":serverID bucket and be indistinguishable across tenants.
		return errors.New("mcp registry: tenantID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := registryKey(tenantID, serverID)
	if _, exists := r.adapters[key]; exists {
		return fmt.Errorf("server already registered: %s", serverID)
	}

	adapter := NewMCPToolCatalog(tenantID, serverID, r.manager, r.logger)

	// 发现 Tools
	_, err := adapter.DiscoverTools(ctx)
	if err != nil {
		return err
	}

	r.adapters[key] = adapter
	r.logger.Info("registered MCP server",
		zap.String("tenant_id", tenantID), zap.String("server_id", serverID))

	return nil
}

// UnregisterServer 注销 MCP 服务器
func (r *MCPToolRegistry) UnregisterServer(tenantID, serverID string) error {
	if tenantID == "" {
		// fail closed: never touch the shared "":serverID bucket.
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := registryKey(tenantID, serverID)
	if _, exists := r.adapters[key]; !exists {
		return nil
	}

	delete(r.adapters, key)
	r.logger.Info("unregistered MCP server",
		zap.String("tenant_id", tenantID), zap.String("server_id", serverID))

	return nil
}
