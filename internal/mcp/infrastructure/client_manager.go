// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const revisionClientCleanupTimeout = 5 * time.Second

// ClientManager 管理多个 MCP 客户端
type ClientManager struct {
	clients    map[string]MCPClient
	configs    map[string]*MCPServerConfig
	connecting map[string]struct{}
	cache      *CapabilityCache
	mu         sync.RWMutex
	logger     *zap.Logger
	poolConfig *ConnectionPoolConfig
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	pool       *pgxpool.Pool

	clientFactory func(*MCPServerConfig, *zap.Logger) MCPClient
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
		clients:       make(map[string]MCPClient),
		configs:       make(map[string]*MCPServerConfig),
		connecting:    make(map[string]struct{}),
		cache:         NewCapabilityCache(1000, 1*time.Hour),
		logger:        logger.Named("mcp.client_manager"),
		poolConfig:    poolConfig,
		stopCh:        make(chan struct{}),
		pool:          pool,
		clientFactory: func(cfg *MCPServerConfig, logger *zap.Logger) MCPClient { return NewBaseClient(cfg, logger) },
	}
}

// ErrNameConflict is the canonical sentinel for an MCP server name collision.
// Kept here as an alias so existing consumers remain source-compatible.
var ErrNameConflict = mcpdomain.ErrNameConflict

func tenantKey(tenantID, serverID string) string { return tenantID + ":" + serverID }

func tenantIDFromCtx(ctx context.Context) string {
	if tc, ok := tenantdb.FromContext(ctx); ok {
		return tc.TenantID
	}
	return ""
}

func (m *ClientManager) persistConnect(ctx context.Context, cfg *MCPServerConfig) error {
	if m.pool == nil {
		return nil
	}
	argsB, _ := json.Marshal(cfg.Args)
	envB, _ := json.Marshal(cfg.Env)
	capsB, _ := json.Marshal(cfg.Capabilities)
	hdrsB, _ := json.Marshal(cfg.Headers)
	authB, _ := json.Marshal(cfg.Auth)
	retryB, _ := json.Marshal(cfg.Retry)
	argsJSON, envJSON, capsJSON, hdrsJSON, authJSON, retryJSON :=
		string(argsB), string(envB), string(capsB), string(hdrsB), string(authB), string(retryB)
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
	key := tenantKey(tenantIDFromCtx(ctx), config.ID)
	if _, exists := m.clients[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf("client already connected: %s", config.ID)
	}
	if _, exists := m.connecting[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf("client already connected: %s", config.ID)
	}
	m.connecting[key] = struct{}{}
	m.mu.Unlock()

	client := m.clientFactory(config, m.logger)
	finish := func() {
		m.mu.Lock()
		delete(m.connecting, key)
		m.mu.Unlock()
	}

	if err := client.Connect(ctx); err != nil {
		finish()
		return err
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools", zap.String("server_id", config.ID), zap.Error(err))
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		m.logger.Warn("failed to list resources", zap.String("server_id", config.ID), zap.Error(err))
	}

	m.cache.Store(key, tools, resources)

	if err := m.persistConnect(ctx, config); err != nil {
		m.cache.Delete(key)
		_ = client.Disconnect(ctx)
		finish()
		return err
	}

	m.mu.Lock()
	delete(m.connecting, key)
	if _, exists := m.clients[key]; exists {
		m.mu.Unlock()
		m.cache.Delete(key)
		_ = client.Disconnect(ctx)
		return fmt.Errorf("client already connected: %s", config.ID)
	}
	m.clients[key] = client
	m.configs[key] = config
	m.mu.Unlock()

	m.logger.Info("connected to MCP server",
		zap.String("server_id", config.ID),
		zap.Int("tools", len(tools)),
		zap.Int("resources", len(resources)))

	return nil
}

// Disconnect 断开连接
func (m *ClientManager) Disconnect(ctx context.Context, serverID string) error {
	m.mu.Lock()
	key := tenantKey(tenantIDFromCtx(ctx), serverID)
	client, exists := m.clients[key]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("client not found: %s", serverID)
	}

	delete(m.clients, key)
	delete(m.configs, key)
	m.cache.Delete(key)
	m.mu.Unlock()

	if err := client.Disconnect(ctx); err != nil {
		return err
	}

	m.persistDisconnect(ctx, serverID)

	m.logger.Info("disconnected from MCP server", zap.String("server_id", serverID))
	return nil
}

// GetClient 获取客户端
func (m *ClientManager) GetClient(ctx context.Context, serverID string) MCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[tenantKey(tenantIDFromCtx(ctx), serverID)]
}

// GetAllClients 获取当前租户所有客户端
func (m *ClientManager) GetAllClients(ctx context.Context) map[string]MCPClient {
	tenantID := tenantIDFromCtx(ctx)
	prefix := tenantID + ":"
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]MCPClient)
	for k, v := range m.clients {
		if strings.HasPrefix(k, prefix) {
			result[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return result
}

// CallTool 调用工具
func (m *ClientManager) CallTool(ctx context.Context, serverID, toolName string, input any) (any, error) {
	client := m.GetClient(ctx, serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	return client.CallTool(ctx, toolName, input)
}

// CallToolWithConfig uses an isolated client built from an immutable revision
// config. It never registers or persists the client in the mutable manager.
func (m *ClientManager) CallToolWithConfig(
	ctx context.Context, config *MCPServerConfig, toolName string, input any,
) (result any, resultErr error) {
	return m.withRevisionClient(ctx, config, func(client MCPClient) (any, error) {
		return client.CallTool(ctx, toolName, input)
	})
}

// ListToolsWithConfig discovers the contract through the same immutable
// revision config used for execution.
func (m *ClientManager) ListToolsWithConfig(
	ctx context.Context, config *MCPServerConfig,
) (tools []*MCPTool, resultErr error) {
	result, err := m.withRevisionClient(ctx, config, func(client MCPClient) (any, error) {
		return client.ListTools(ctx)
	})
	if err != nil {
		return nil, err
	}
	tools, ok := result.([]*MCPTool)
	if !ok {
		return nil, errors.New("MCP revision client: invalid tool response")
	}
	return tools, nil
}

func (m *ClientManager) withRevisionClient(
	ctx context.Context, config *MCPServerConfig, operation func(MCPClient) (any, error),
) (result any, resultErr error) {
	if m == nil || m.clientFactory == nil || config == nil || strings.TrimSpace(config.ID) == "" {
		return nil, errors.New("MCP revision client: configuration unavailable")
	}
	client := m.clientFactory(config, m.logger)
	if client == nil {
		return nil, errors.New("MCP revision client: construction failed")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), revisionClientCleanupTimeout)
		defer cancel()
		if err := client.Disconnect(cleanupCtx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("MCP revision client: disconnect: %w", err))
		}
	}()
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("MCP revision client: connect: %w", err)
	}
	return operation(client)
}

// ListTools 列出工具
func (m *ClientManager) ListTools(ctx context.Context, serverID string) ([]*MCPTool, error) {
	key := tenantKey(tenantIDFromCtx(ctx), serverID)
	if tools, ok := m.cache.GetTools(key); ok {
		return tools, nil
	}

	client := m.GetClient(ctx, serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	m.cache.StoreTools(key, tools)
	return tools, nil
}

// ListResources 列出资源
func (m *ClientManager) ListResources(ctx context.Context, serverID string) ([]*MCPResource, error) {
	key := tenantKey(tenantIDFromCtx(ctx), serverID)
	if resources, ok := m.cache.GetResources(key); ok {
		return resources, nil
	}

	client := m.GetClient(ctx, serverID)
	if client == nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	resources, err := client.ListResources(ctx)
	if err != nil {
		return nil, err
	}

	m.cache.StoreResources(key, resources)
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
	type reconnectCandidate struct {
		config *MCPServerConfig
		client MCPClient
	}
	m.mu.RLock()
	unhealthy := make(map[string]reconnectCandidate)
	for k, v := range m.clients {
		if !v.IsHealthy() {
			unhealthy[k] = reconnectCandidate{config: m.configs[k], client: v}
		}
	}
	m.mu.RUnlock()

	if len(unhealthy) == 0 {
		return
	}

	sem := make(chan struct{}, m.poolConfig.MaxConnections)
	var wg sync.WaitGroup
	for key, candidate := range unhealthy {
		if candidate.config == nil {
			continue
		}
		key, candidate := key, candidate
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error("reconnect panic", zap.String("key", key), zap.Any("panic", r))
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m.logger.Warn("client unhealthy, attempting reconnect", zap.String("key", key))
			fresh := m.clientFactory(candidate.config, m.logger)
			if err := fresh.Connect(ctx); err != nil {
				m.logger.Error("reconnect failed", zap.String("key", key), zap.Error(err))
				return
			}
			m.mu.Lock()
			current := m.clients[key]
			if current == candidate.client {
				m.clients[key] = fresh
			}
			m.mu.Unlock()
			if current != candidate.client {
				_ = fresh.Disconnect(ctx)
				return
			}
			if err := candidate.client.Disconnect(ctx); err != nil {
				m.logger.Warn("displaced client disconnect failed", zap.String("key", key), zap.Error(err))
			}
		}()
	}
	wg.Wait()
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
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.wg.Wait()

	m.mu.Lock()
	clients := m.clients
	m.clients = make(map[string]MCPClient)
	m.configs = make(map[string]*MCPServerConfig)
	m.connecting = make(map[string]struct{})
	m.mu.Unlock()

	for serverID, client := range clients {
		if err := client.Disconnect(ctx); err != nil {
			m.logger.Error("failed to disconnect",
				zap.String("server_id", serverID),
				zap.Error(err))
		}
	}

	return nil
}

// GetServerInfo 获取服务器信息
func (m *ClientManager) GetServerInfo(ctx context.Context, serverID string) *MCPServerInfo {
	client := m.GetClient(ctx, serverID)
	if client == nil {
		return nil
	}
	return client.GetServerInfo()
}

// GetAllServerInfo 获取当前租户所有服务器信息（从 DB 读取配置，以内存连接状态覆盖）
func (m *ClientManager) GetAllServerInfo(ctx context.Context) []*MCPServerInfo {
	tenantID := tenantIDFromCtx(ctx)

	if m.pool == nil {
		prefix := tenantID + ":"
		m.mu.RLock()
		defer m.mu.RUnlock()
		var infos []*MCPServerInfo
		for key, client := range m.clients {
			if strings.HasPrefix(key, prefix) {
				infos = append(infos, client.GetServerInfo())
			}
		}
		return infos
	}

	type dbRow struct {
		id, name, version, transport string
	}
	var rows []dbRow
	_ = tenantdb.ExecTenant(ctx, m.pool, func(qctx context.Context, tx pgx.Tx) error {
		pgRows, err := tx.Query(qctx,
			`SELECT id, name, version, transport FROM mcp_configs`)
		if err != nil {
			return err
		}
		defer pgRows.Close()
		for pgRows.Next() {
			var r dbRow
			if err := pgRows.Scan(&r.id, &r.name, &r.version, &r.transport); err != nil {
				return err
			}
			rows = append(rows, r)
		}
		return pgRows.Err()
	})

	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]*MCPServerInfo, 0, len(rows))
	for _, r := range rows {
		if client, ok := m.clients[tenantKey(tenantID, r.id)]; ok {
			infos = append(infos, client.GetServerInfo())
		} else {
			infos = append(infos, &MCPServerInfo{
				ID:        r.id,
				Name:      r.name,
				Version:   r.version,
				Transport: r.transport,
				Status:    "disconnected",
			})
		}
	}
	return infos
}

// UpdateServer disconnects the existing connection and reconnects with the new config.
func (m *ClientManager) UpdateServer(ctx context.Context, cfg *MCPServerConfig) error {
	key := tenantKey(tenantIDFromCtx(ctx), cfg.ID)
	m.mu.Lock()
	var old MCPClient
	if client, exists := m.clients[key]; exists {
		old = client
		delete(m.clients, key)
		delete(m.configs, key)
		m.cache.Delete(key)
	}
	m.mu.Unlock()
	if old != nil {
		_ = old.Disconnect(ctx)
	}
	return m.Connect(ctx, cfg)
}

// Reconnect reads the saved config for serverID from DB and reconnects.
func (m *ClientManager) Reconnect(ctx context.Context, serverID string) error {
	cfg, err := m.GetServerConfig(ctx, serverID)
	if err != nil {
		return fmt.Errorf("reconnect %s: %w", serverID, err)
	}
	return m.Connect(ctx, cfg)
}

// Delete disconnects the server if connected and hard-deletes its config from DB.
// agent_mcp_servers is removed via ON DELETE CASCADE.
func (m *ClientManager) Delete(ctx context.Context, serverID string) error {
	m.mu.Lock()
	key := tenantKey(tenantIDFromCtx(ctx), serverID)
	var old MCPClient
	if client, exists := m.clients[key]; exists {
		old = client
		delete(m.clients, key)
		delete(m.configs, key)
		m.cache.Delete(key)
	}
	m.mu.Unlock()
	if old != nil {
		_ = old.Disconnect(ctx)
	}

	if m.pool == nil {
		return nil
	}
	return tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM mcp_configs WHERE id=$1`, serverID)
		return err
	})
}

// GetServerConfig returns the full config for serverID, checking memory then DB.
func (m *ClientManager) GetServerConfig(ctx context.Context, serverID string) (*MCPServerConfig, error) {
	key := tenantKey(tenantIDFromCtx(ctx), serverID)
	m.mu.RLock()
	cfg := m.configs[key]
	m.mu.RUnlock()
	if cfg != nil {
		return cfg, nil
	}
	if m.pool == nil {
		return nil, mcpdomain.ErrServerNotFound
	}
	var out MCPServerConfig
	var argsStr, envStr, capsStr, hdrsStr, authStr, retryStr string
	var timeoutSec int
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, name, transport, command, url, args, env, capabilities,
			       timeout_sec, version, headers, auth_config, retry_config
			FROM mcp_configs WHERE id=$1`, serverID).
			Scan(&out.ID, &out.Name, &out.Transport, &out.Command, &out.URL,
				&argsStr, &envStr, &capsStr, &timeoutSec,
				&out.Version, &hdrsStr, &authStr, &retryStr)
	})
	if err != nil {
		return nil, mcpdomain.ErrServerNotFound
	}
	out.Timeout = time.Duration(timeoutSec) * time.Second
	_ = json.Unmarshal([]byte(argsStr), &out.Args)
	_ = json.Unmarshal([]byte(envStr), &out.Env)
	_ = json.Unmarshal([]byte(capsStr), &out.Capabilities)
	_ = json.Unmarshal([]byte(hdrsStr), &out.Headers)
	if authStr != "" && authStr != "null" {
		out.Auth = &mcpdomain.AuthConfig{}
		_ = json.Unmarshal([]byte(authStr), out.Auth)
	}
	if retryStr != "" && retryStr != "null" {
		out.Retry = &mcpdomain.RetryConfig{}
		_ = json.Unmarshal([]byte(retryStr), out.Retry)
	}
	return &out, nil
}
