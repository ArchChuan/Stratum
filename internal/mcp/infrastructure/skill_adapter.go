// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// MCPToolHandle 将 MCP 工具包装为 Tool
type MCPToolHandle struct {
	ID          string
	Name        string
	Description string
	Type        string
	Tool        *MCPTool
	ServerID    string
	Manager     *ClientManager
	logger      *zap.Logger
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

// Execute 执行工具
func (w *MCPToolHandle) Execute(ctx context.Context, input any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := w.Manager.CallTool(ctx, w.ServerID, w.Tool.Name, input)
	if err != nil {
		w.logger.Error("failed to execute MCP tool",
			zap.String("tool", w.Tool.Name),
			zap.String("server_id", w.ServerID),
			zap.Error(err))
		return nil, err
	}

	return result, nil
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
			ServerID:    a.serverID,
			Manager:     a.manager,
			logger:      a.logger,
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

// GetCatalogForServer returns the adapter for a specific server, or nil if not registered.
func (r *MCPToolRegistry) GetCatalogForServer(tenantID, serverID string) *MCPToolCatalog {
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

// GetRegisteredTool 获取 Tool
func (r *MCPToolRegistry) GetRegisteredTool(toolID string) *MCPToolHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, adapter := range r.adapters {
		if s := adapter.GetRegisteredTool(toolID); s != nil {
			return s
		}
	}
	return nil
}

// GetAllTools 获取所有 Tools
func (r *MCPToolRegistry) GetAllTools() []*MCPToolHandle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []*MCPToolHandle
	for _, adapter := range r.adapters {
		tools = append(tools, adapter.GetAllTools()...)
	}
	return tools
}

// ExecuteToolByID 执行 Tool
func (r *MCPToolRegistry) ExecuteToolByID(toolID string, input any) (any, error) {
	s := r.GetRegisteredTool(toolID)
	if s == nil {
		return nil, fmt.Errorf("skill not found: %s", toolID)
	}
	return s.Execute(context.Background(), input)
}

// RefreshTools 刷新 Tools
func (r *MCPToolRegistry) RefreshTools(ctx context.Context) error {
	r.mu.RLock()
	adapters := make(map[string]*MCPToolCatalog)
	for k, v := range r.adapters {
		adapters[k] = v
	}
	r.mu.RUnlock()

	for _, adapter := range adapters {
		_, err := adapter.DiscoverTools(ctx)
		if err != nil {
			r.logger.Warn("failed to refresh tools",
				zap.String("tenant_id", adapter.tenantID),
				zap.String("server_id", adapter.serverID),
				zap.Error(err))
		}
	}

	return nil
}

// GetServerInfo 获取服务器信息
func (r *MCPToolRegistry) GetServerInfo(ctx context.Context, serverID string) any {
	return r.manager.GetServerInfo(ctx, serverID)
}

// GetAllServerInfo 获取所有服务器信息
func (r *MCPToolRegistry) GetAllServerInfo(ctx context.Context) []*MCPServerInfo {
	return r.manager.GetAllServerInfo(ctx)
}
