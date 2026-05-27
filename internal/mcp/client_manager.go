package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ClientManager 管理多个 MCP 客户端
type ClientManager struct {
	clients    map[string]MCPClient
	configs    map[string]*MCPServerConfig
	cache      *CapabilityCache
	mu         sync.RWMutex
	logger     *zap.Logger
	poolConfig *ConnectionPoolConfig
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewClientManager 创建新的客户端管理器
func NewClientManager(logger *zap.Logger, poolConfig *ConnectionPoolConfig) *ClientManager {
	if poolConfig == nil {
		poolConfig = &ConnectionPoolConfig{
			MaxConnections: 10,
			IdleTimeout:    5 * time.Minute,
			MaxRetries:     3,
			RetryBackoff:   1 * time.Second,
		}
	}

	return &ClientManager{
		clients:    make(map[string]MCPClient),
		configs:    make(map[string]*MCPServerConfig),
		cache:      NewCapabilityCache(1000, 1*time.Hour),
		logger:     logger.Named("mcp.client_manager"),
		poolConfig: poolConfig,
		stopCh:     make(chan struct{}),
	}
}

// Connect 连接到 MCP 服务器
func (m *ClientManager) Connect(ctx context.Context, config *MCPServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[config.ID]; exists {
		return fmt.Errorf("client already connected: %s", config.ID)
	}

	// 创建客户端
	client := NewBaseClient(config, m.logger)

	// 连接
	if err := client.Connect(ctx); err != nil {
		return err
	}

	// 发现能力
	tools, err := client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools", zap.String("server_id", config.ID), zap.Error(err))
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		m.logger.Warn("failed to list resources", zap.String("server_id", config.ID), zap.Error(err))
	}

	// 缓存能力
	m.cache.Store(config.ID, tools, resources)

	// 保存客户端和配置
	m.clients[config.ID] = client
	m.configs[config.ID] = config

	m.logger.Info("connected to MCP server",
		zap.String("server_id", config.ID),
		zap.Int("tools", len(tools)),
		zap.Int("resources", len(resources)))

	return nil
}

// Disconnect 断开连接
func (m *ClientManager) Disconnect(ctx context.Context, serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, exists := m.clients[serverID]
	if !exists {
		return fmt.Errorf("client not found: %s", serverID)
	}

	if err := client.Disconnect(ctx); err != nil {
		return err
	}

	delete(m.clients, serverID)
	delete(m.configs, serverID)
	m.cache.Delete(serverID)

	m.logger.Info("disconnected from MCP server", zap.String("server_id", serverID))
	return nil
}

// GetClient 获取客户端
func (m *ClientManager) GetClient(serverID string) MCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[serverID]
}

// GetAllClients 获取所有客户端
func (m *ClientManager) GetAllClients() map[string]MCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]MCPClient)
	for k, v := range m.clients {
		result[k] = v
	}
	return result
}

// CallTool 调用工具
func (m *ClientManager) CallTool(ctx context.Context, serverID, toolName string, input any) (any, error) {
	client := m.GetClient(serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	return client.CallTool(ctx, toolName, input)
}

// ListTools 列出工具
func (m *ClientManager) ListTools(ctx context.Context, serverID string) ([]*MCPTool, error) {
	// 先尝试从缓存获取
	if tools, ok := m.cache.GetTools(serverID); ok {
		return tools, nil
	}

	client := m.GetClient(serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	m.cache.StoreTools(serverID, tools)
	return tools, nil
}

// ListResources 列出资源
func (m *ClientManager) ListResources(ctx context.Context, serverID string) ([]*MCPResource, error) {
	// 先尝试从缓存获取
	if resources, ok := m.cache.GetResources(serverID); ok {
		return resources, nil
	}

	client := m.GetClient(serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		return nil, err
	}

	m.cache.StoreResources(serverID, resources)
	return resources, nil
}

// StartHealthCheck 启动健康检查
func (m *ClientManager) StartHealthCheck(interval time.Duration) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.performHealthCheck()
			}
		}
	}()
}

// performHealthCheck 执行健康检查
func (m *ClientManager) performHealthCheck() {
	m.mu.RLock()
	clients := make(map[string]MCPClient)
	for k, v := range m.clients {
		clients[k] = v
	}
	m.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for serverID, client := range clients {
		if !client.IsHealthy() {
			m.logger.Warn("client unhealthy, attempting reconnect",
				zap.String("server_id", serverID))

			// 尝试重新连接
			m.mu.RLock()
			config := m.configs[serverID]
			m.mu.RUnlock()

			if config != nil {
				if err := client.Connect(ctx); err != nil {
					m.logger.Error("reconnect failed",
						zap.String("server_id", serverID),
						zap.Error(err))
				}
			}
		}
	}
}

// Stop 停止管理器
func (m *ClientManager) Stop(ctx context.Context) error {
	close(m.stopCh)
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	for serverID, client := range m.clients {
		if err := client.Disconnect(ctx); err != nil {
			m.logger.Error("failed to disconnect",
				zap.String("server_id", serverID),
				zap.Error(err))
		}
	}

	m.clients = make(map[string]MCPClient)
	m.configs = make(map[string]*MCPServerConfig)

	return nil
}

// GetServerInfo 获取服务器信息
func (m *ClientManager) GetServerInfo(serverID string) *MCPServerInfo {
	client := m.GetClient(serverID)
	if client == nil {
		return nil
	}
	return client.GetServerInfo()
}

// GetAllServerInfo 获取所有服务器信息
func (m *ClientManager) GetAllServerInfo() []*MCPServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var infos []*MCPServerInfo
	for _, client := range m.clients {
		infos = append(infos, client.GetServerInfo())
	}
	return infos
}
