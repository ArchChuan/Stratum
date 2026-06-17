// Package mcp provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool       *pgxpool.Pool
}

// NewClientManager 创建新的客户端管理器
func NewClientManager(logger *zap.Logger, poolConfig *ConnectionPoolConfig, pool *pgxpool.Pool) *ClientManager {
	if poolConfig == nil {
		poolConfig = &ConnectionPoolConfig{
			MaxConnections: 10,
			IdleTimeout:    constants.MCPIdleTimeout,
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
		pool:       pool,
	}
}

// ErrNameConflict is returned by Connect when an MCP server with the same name already exists in the tenant.
var ErrNameConflict = errors.New("mcp server name already exists")

func (m *ClientManager) persistConnect(ctx context.Context, cfg *MCPServerConfig) error {
	if m.pool == nil {
		return nil
	}
	argsJSON, _ := json.Marshal(cfg.Args)
	envJSON, _ := json.Marshal(cfg.Env)
	capsJSON, _ := json.Marshal(cfg.Capabilities)
	hdrsJSON, _ := json.Marshal(cfg.Headers)
	authJSON, _ := json.Marshal(cfg.Auth)
	retryJSON, _ := json.Marshal(cfg.Retry)
	timeoutSec := int(cfg.Timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO mcp_configs
				(id, name, transport, command, url, args, env, capabilities, timeout_sec,
				 enabled, version, headers, auth_config, retry_config, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $12, $13, NOW())
			ON CONFLICT (id) DO UPDATE SET
				name=$2, transport=$3, command=$4, url=$5,
				args=$6, env=$7, capabilities=$8, timeout_sec=$9,
				enabled=true, version=$10, headers=$11, auth_config=$12, retry_config=$13,
				updated_at=NOW()`,
			cfg.ID, cfg.Name, cfg.Transport, cfg.Command, cfg.URL,
			argsJSON, envJSON, capsJSON, timeoutSec,
			cfg.Version, hdrsJSON, authJSON, retryJSON)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: mcp server name %q", ErrNameConflict, cfg.Name)
		}
		return fmt.Errorf("persist mcp config %s: %w", cfg.ID, err)
	}
	return nil
}

func (m *ClientManager) persistDisconnect(ctx context.Context, serverID string) {
	if m.pool == nil {
		return
	}
	_ = tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE mcp_configs SET enabled=false WHERE id=$1`, serverID)
		return err
	})
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

	// 持久化到 DB（先落库，名称冲突则不写内存）
	if err := m.persistConnect(ctx, config); err != nil {
		m.cache.Delete(config.ID)
		return err
	}

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

	m.persistDisconnect(ctx, serverID)

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

// RestoreFromDB 遍历所有租户，从各自 schema 读取 enabled=true 的 MCP 配置并重建连接。
// 连接失败只记 warn，不返回错误，不阻塞启动。
func (m *ClientManager) RestoreFromDB(ctx context.Context) error {
	if m.pool == nil {
		return nil
	}

	// mcp_configs 是 per-tenant 表，必须逐租户注入 TenantContext 后查询。
	schemas, err := tenantdb.ListTenantSchemas(ctx, m.pool)
	if err != nil {
		return fmt.Errorf("RestoreFromDB: list tenants: %w", err)
	}

	type row struct {
		id          string
		name        string
		transport   string
		command     string
		url         string
		version     string
		args        []byte
		env         []byte
		caps        []byte
		headers     []byte
		authConfig  []byte
		retryConfig []byte
		timeoutSec  int
	}

	for _, schema := range schemas {
		// schema 格式为 "tenant_<id>"
		tenantID := strings.TrimPrefix(schema, "tenant_")
		tctx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
			TenantID: tenantID,
			Role:     tenantdb.RoleTenantAdmin,
		})

		var rows []row
		queryErr := tenantdb.ExecTenant(tctx, m.pool, func(qctx context.Context, tx pgx.Tx) error {
			pgRows, err := tx.Query(qctx, `
				SELECT id, name, transport, command, url, version,
				       args, env, capabilities, headers, auth_config, retry_config, timeout_sec
				FROM mcp_configs WHERE enabled = true`)
			if err != nil {
				return fmt.Errorf("restore mcp_configs query: %w", err)
			}
			defer pgRows.Close()
			for pgRows.Next() {
				var r row
				if err := pgRows.Scan(&r.id, &r.name, &r.transport, &r.command, &r.url, &r.version,
					&r.args, &r.env, &r.caps, &r.headers, &r.authConfig, &r.retryConfig, &r.timeoutSec); err != nil {
					return fmt.Errorf("restore mcp_configs scan: %w", err)
				}
				rows = append(rows, r)
			}
			return pgRows.Err()
		})
		if queryErr != nil {
			m.logger.Warn("RestoreFromDB: failed to query tenant",
				zap.String("tenant_id", tenantID),
				zap.Error(queryErr))
			continue
		}

		for _, r := range rows {
			var args []string
			var env map[string]string
			var caps []string
			var headers map[string]string
			var auth *MCPAuthConfig
			var retry *MCPRetryConfig
			_ = json.Unmarshal(r.args, &args)
			_ = json.Unmarshal(r.env, &env)
			_ = json.Unmarshal(r.caps, &caps)
			_ = json.Unmarshal(r.headers, &headers)
			_ = json.Unmarshal(r.authConfig, &auth)
			_ = json.Unmarshal(r.retryConfig, &retry)

			cfg := &MCPServerConfig{
				ID:           r.id,
				Name:         r.name,
				Transport:    r.transport,
				Command:      r.command,
				URL:          r.url,
				Version:      r.version,
				Args:         args,
				Env:          env,
				Capabilities: caps,
				Headers:      headers,
				Auth:         auth,
				Retry:        retry,
				Timeout:      time.Duration(r.timeoutSec) * time.Second,
			}

			connectCtx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
				TenantID: tenantID,
				Role:     tenantdb.RoleTenantAdmin,
			})
			if err := m.Connect(connectCtx, cfg); err != nil {
				m.logger.Warn("RestoreFromDB: failed to reconnect MCP server",
					zap.String("tenant_id", tenantID),
					zap.String("server_id", cfg.ID),
					zap.Error(err))
			} else {
				m.logger.Info("RestoreFromDB: reconnected MCP server",
					zap.String("tenant_id", tenantID),
					zap.String("server_id", cfg.ID))
			}
		}
	}

	return nil
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
