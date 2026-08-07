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

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/infrastructure/mcpnode"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
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

	// serverCtx is the lifecycle context for spawned child processes.
	// Cancelled only when the server shuts down, not on HTTP request end.
	serverCtx    context.Context
	serverCancel context.CancelFunc

	// nodeID identifies this instance in a multi-pod deployment. Only
	// stdio servers owned by this node are restored/spawned locally.
	nodeID string

	clientFactory func(*MCPServerConfig, *zap.Logger) MCPClient

	metrics observability.MetricsProvider
}

// NewClientManager 创建新的客户端管理器
func NewClientManager(
	logger *zap.Logger, poolConfig *ConnectionPoolConfig, pool *pgxpool.Pool, nodeID string,
) *ClientManager {
	if poolConfig == nil {
		poolConfig = &ConnectionPoolConfig{
			MaxConnections: 10,
			IdleTimeout:    constants.MCPIdleTimeout,
			MaxRetries:     3,
			RetryBackoff:   1 * time.Second,
		}
	}
	if nodeID == "" {
		nodeID = mcpnode.NodeID()
	}

	manager := &ClientManager{
		clients:    make(map[string]MCPClient),
		configs:    make(map[string]*MCPServerConfig),
		connecting: make(map[string]struct{}),
		cache:      NewCapabilityCache(1000, 1*time.Hour),
		logger:     logger.Named("mcp.client_manager"),
		poolConfig: poolConfig,
		stopCh:     make(chan struct{}),
		pool:       pool,
		nodeID:     nodeID,
		metrics:    observability.NoopMetrics{},
	}
	//nolint:gosec // serverCancel is called in Stop()
	manager.serverCtx, manager.serverCancel = context.WithCancel(context.Background())
	manager.clientFactory = func(cfg *MCPServerConfig, logger *zap.Logger) MCPClient {
		return NewBaseClient(cfg, logger)
	}
	return manager
}

// SetMetrics injects the observability MetricsProvider (no-ops until set).
func (m *ClientManager) SetMetrics(metrics observability.MetricsProvider) {
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	m.metrics = metrics
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

func (m *ClientManager) checkConnectionLimits(tenantID string) error {
	m.mu.RLock()
	prefix := tenantID + ":"
	totalCount := len(m.clients) + len(m.connecting)
	tenantCount := 0
	for k := range m.clients {
		if strings.HasPrefix(k, prefix) {
			tenantCount++
		}
	}
	for k := range m.connecting {
		if strings.HasPrefix(k, prefix) {
			tenantCount++
		}
	}
	m.mu.RUnlock()

	maxTotal := m.poolConfig.MaxConnections
	if maxTotal <= 0 {
		maxTotal = constants.MCPMaxTotalConnections
	}
	maxPerTenant := m.poolConfig.MaxPerTenant
	if maxPerTenant <= 0 {
		maxPerTenant = constants.MCPMaxConnectionsPerTenant
	}

	if totalCount >= maxTotal {
		return fmt.Errorf("%w: global limit %d", mcpdomain.ErrConnectionLimitExceeded, maxTotal)
	}
	if tenantCount >= maxPerTenant {
		return fmt.Errorf("%w: tenant limit %d", mcpdomain.ErrConnectionLimitExceeded, maxPerTenant)
	}
	return nil
}

// insertChangeAudit inserts the audit row in the SAME transaction as the
// business write; an audit failure rolls the business change back (fail
// closed). A nil event skips auditing — reserved for internal reentrant
// paths (restore/reconnect). Incomplete events are a caller bug and fail
// the transaction.
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
		return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
			ev.ResourceKind, ev.ResourceID, ev.Operation)
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("change audit: missing tenant context")
	}
	_, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
		uuid.Must(uuid.NewV7()).String(), tc.TenantID,
		ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
		ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
	}
	return nil
}

func (m *ClientManager) persistConnect(ctx context.Context, cfg *MCPServerConfig, audit *auditdomain.ResourceChangeAuditEvent) error {
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
		if _, err := tx.Exec(ctx, `
			INSERT INTO mcp_configs
				(id, name, transport, command, url, args, env, capabilities, timeout_sec,
				 enabled, version, headers, auth_config, retry_config, updated_at, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $11, $12, $13, NOW(), $14)
			ON CONFLICT (id) DO UPDATE SET
				name=$2, transport=$3, command=$4, url=$5,
				args=$6, env=$7, capabilities=$8, timeout_sec=$9,
				enabled=true, version=$10, headers=$11, auth_config=$12, retry_config=$13,
				updated_at=NOW()`,
			cfg.ID, cfg.Name, cfg.Transport, cfg.Command, cfg.URL,
			argsJSON, envJSON, capsJSON, timeoutSec,
			cfg.Version, hdrsJSON, authJSON, retryJSON, cfg.CreatedBy); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
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
func (m *ClientManager) Connect(ctx context.Context, config *MCPServerConfig, audit *auditdomain.ResourceChangeAuditEvent) error {
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

	// Enforce connection limits.
	tenantID := tenantIDFromCtx(ctx)
	if err := m.checkConnectionLimits(tenantID); err != nil {
		m.mu.Lock()
		delete(m.connecting, key)
		m.mu.Unlock()
		return err
	}

	client := m.clientFactory(config, m.logger)

	// Derive connection context from server lifecycle, NOT the HTTP request.
	connCtx := m.serverCtx
	if tc, ok := tenantdb.FromContext(ctx); ok {
		connCtx = tenantdb.WithTenant(connCtx, tc)
	}

	if err := client.Connect(connCtx); err != nil {
		m.metrics.IncMCPClientRequest(config.ID, "connect", "error")
		cleanupErr := disconnectMCPClient(client)
		if cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("cleanup failed MCP connection: %w", cleanupErr))
		}
		m.mu.Lock()
		delete(m.connecting, key)
		m.mu.Unlock()
		return err
	}

	tools, resources, err := m.scanCapabilities(ctx, client, config, key, audit)
	if err != nil {
		_ = disconnectMCPClient(client)
		m.mu.Lock()
		delete(m.connecting, key)
		m.mu.Unlock()
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
	m.metrics.IncMCPClientRequest(config.ID, "connect", "ok")

	return nil
}

// scanCapabilities discovers tools and resources from a connected client,
// caches them, and persists the config.
func (m *ClientManager) scanCapabilities(
	ctx context.Context, client MCPClient, config *MCPServerConfig, key string, audit *auditdomain.ResourceChangeAuditEvent,
) ([]*MCPTool, []*MCPResource, error) {
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("discover MCP tools: %w", err)
	}
	resources, err := client.ListResources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("discover MCP resources: %w", err)
	}
	m.cache.Store(key, tools, resources)
	if err := m.persistConnect(ctx, config, audit); err != nil {
		m.cache.Delete(key)
		return nil, nil, err
	}
	return tools, resources, nil
}

func disconnectMCPClient(client MCPClient) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), revisionClientCleanupTimeout)
	defer cancel()
	return client.Disconnect(cleanupCtx)
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

// getOrRestoreClient returns the live client for serverID, lazily reconnecting
// from persisted config when the client was evicted by the idle reaper.
func (m *ClientManager) getOrRestoreClient(ctx context.Context, serverID string) (MCPClient, error) {
	if client := m.GetClient(ctx, serverID); client != nil {
		return client, nil
	}

	cfg, err := m.GetServerConfig(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}
	if !cfg.Enabled {
		return nil, fmt.Errorf("client not found: %s", serverID)
	}

	if err := m.Connect(ctx, cfg, nil); err != nil {
		// Re-check: concurrent goroutine may have just registered the client
		// between our GetClient and Connect (Connect deduplicates via m.connecting).
		if client := m.GetClient(ctx, serverID); client != nil {
			return client, nil
		}
		// Winner still mid-connect; poll briefly for it to land so a burst
		// of calls after eviction all succeed.
		if strings.Contains(err.Error(), "already connected") {
			client := m.waitForClient(ctx, serverID, 5*time.Second)
			if client != nil {
				return client, nil
			}
		}
		m.logger.Warn("mcp auto-reconnect failed",
			zap.String("server_id", serverID), zap.Error(err))
		m.metrics.IncMCPClientReconnect(serverID)
		return nil, err
	}

	if client := m.GetClient(ctx, serverID); client != nil {
		m.logger.Info("lazily reconnected MCP server", zap.String("server_id", serverID))
		return client, nil
	}
	return nil, fmt.Errorf("client not found: %s", serverID)
}

// waitForClient polls GetClient until the client appears or the timeout expires.
func (m *ClientManager) waitForClient(ctx context.Context, serverID string, timeout time.Duration) MCPClient {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if client := m.GetClient(ctx, serverID); client != nil {
			return client
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
	return m.GetClient(ctx, serverID)
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
	client, err := m.getOrRestoreClient(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return client.CallTool(ctx, toolName, input)
}

// CallToolWithConfig uses an isolated client built from an immutable revision
// config. It never registers or persists the client in the mutable manager.
func (m *ClientManager) CallToolWithConfig(
	ctx context.Context, config *MCPServerConfig, toolName string, input any,
) (result any, resultErr error) {
	result, resultErr = m.withRevisionClient(ctx, config, func(client MCPClient) (any, error) {
		return client.CallTool(ctx, toolName, input)
	})
	if resultErr != nil {
		m.metrics.IncMCPClientRequest(config.ID, "call_tool", "error")
	} else {
		m.metrics.IncMCPClientRequest(config.ID, "call_tool", "ok")
	}
	return
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

	client, err := m.getOrRestoreClient(ctx, serverID)
	if err != nil {
		return nil, err
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

	client, err := m.getOrRestoreClient(ctx, serverID)
	if err != nil {
		return nil, err
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

// StartIdleEviction starts a background goroutine that periodically disconnects
// clients that have been idle longer than the configured idle timeout.
func (m *ClientManager) StartIdleEviction(interval, idleTimeout time.Duration) {
	if interval <= 0 {
		interval = constants.MCPIdleEvictionInterval
	}
	if idleTimeout <= 0 {
		idleTimeout = constants.MCPIdleTimeout
	}
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
				m.evictIdle(idleTimeout)
			}
		}
	}()
}

func (m *ClientManager) evictIdle(idleTimeout time.Duration) {
	now := time.Now()
	m.mu.RLock()
	var idle []struct {
		key    string
		client MCPClient
	}
	for key, client := range m.clients {
		if now.Sub(client.LastActivity()) > idleTimeout {
			idle = append(idle, struct {
				key    string
				client MCPClient
			}{key, client})
		}
	}
	m.mu.RUnlock()

	for _, entry := range idle {
		m.mu.Lock()
		_, stillIdle := m.clients[entry.key]
		if !stillIdle || now.Sub(entry.client.LastActivity()) <= idleTimeout {
			m.mu.Unlock()
			continue
		}
		delete(m.clients, entry.key)
		delete(m.configs, entry.key)
		m.cache.Delete(entry.key)
		m.mu.Unlock()

		_ = disconnectMCPClient(entry.client)
		m.logger.Info("evicted idle MCP client", zap.String("key", entry.key))
	}
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

	schemas, err := tenantdb.ListTenantSchemas(ctx, m.pool)
	if err != nil {
		return fmt.Errorf("RestoreFromDB: list tenants: %w", err)
	}

	for _, schema := range schemas {
		tenantID := strings.TrimPrefix(schema, "tenant_")
		if err := m.restoreTenantServers(ctx, tenantID); err != nil {
			m.logger.Warn("RestoreFromDB: failed to restore tenant",
				zap.String("tenant_id", tenantID), zap.Error(err))
		}
	}
	return nil
}

type mcpConfigRow struct {
	id, name, transport, command, url, version, createdBy string
	args, env, caps, headers, authCfg, retryCfg           []byte
	timeoutSec                                            int
	systemKey, managementMode                             string
}

func (m *ClientManager) restoreTenantServers(ctx context.Context, tenantID string) error {
	tctx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
		TenantID: tenantID, Role: tenantdb.RoleTenantAdmin,
	})

	rows, err := m.loadMCPConfigRows(tctx, tenantID)
	if err != nil {
		return err
	}

	for _, r := range rows {
		cfg := configFromDBRow(r)
		m.restoreServer(ctx, tenantID, cfg)
	}
	return nil
}

func (m *ClientManager) loadMCPConfigRows(ctx context.Context, _ string) ([]mcpConfigRow, error) {
	var rows []mcpConfigRow
	err := tenantdb.ExecTenant(ctx, m.pool, func(qctx context.Context, tx pgx.Tx) error {
		pgRows, qErr := tx.Query(qctx, `
			SELECT id, name, transport, command, url, version,
			       args, env, capabilities, headers, auth_config, retry_config, timeout_sec,
			       COALESCE(system_key, ''), management_mode, COALESCE(created_by, '')
			FROM mcp_configs WHERE enabled = true`)
		if qErr != nil {
			return fmt.Errorf("restore mcp_configs query: %w", qErr)
		}
		defer pgRows.Close()
		for pgRows.Next() {
			var r mcpConfigRow
			if sErr := pgRows.Scan(&r.id, &r.name, &r.transport, &r.command, &r.url, &r.version,
				&r.args, &r.env, &r.caps, &r.headers, &r.authCfg, &r.retryCfg, &r.timeoutSec,
				&r.systemKey, &r.managementMode, &r.createdBy); sErr != nil {
				return fmt.Errorf("restore mcp_configs scan: %w", sErr)
			}
			rows = append(rows, r)
		}
		return pgRows.Err()
	})
	return rows, err
}

func configFromDBRow(r mcpConfigRow) *MCPServerConfig {
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
	_ = json.Unmarshal(r.authCfg, &auth)
	_ = json.Unmarshal(r.retryCfg, &retry)
	return &MCPServerConfig{
		ID: r.id, Name: r.name, Transport: r.transport,
		Command: r.command, URL: r.url, Version: r.version,
		Args: args, Env: env, Capabilities: caps, Headers: headers,
		Auth: auth, Retry: retry,
		Timeout:        time.Duration(r.timeoutSec) * time.Second,
		SystemKey:      r.systemKey,
		ManagementMode: r.managementMode,
		CreatedBy:      r.createdBy,
	}
}

func (m *ClientManager) restoreServer(ctx context.Context, tenantID string, cfg *MCPServerConfig) {
	// stdio servers are node-local — only the owning node spawns.
	if cfg.Transport == "stdio" {
		claimed, claimErr := m.claimOwnership(ctx, tenantID, cfg.ID)
		if claimErr != nil {
			m.logger.Warn("RestoreFromDB: claim ownership failed",
				zap.String("tenant_id", tenantID),
				zap.String("server_id", cfg.ID),
				zap.Error(claimErr))
			return
		}
		if !claimed {
			return // owned by another node
		}
	}

	connectCtx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
		TenantID: tenantID, Role: tenantdb.RoleTenantAdmin,
	})
	if err := m.Connect(connectCtx, cfg, nil); err != nil {
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

// claimOwnership atomically adopts a stdio server for this node. Only
// succeeds when owner_node is empty or the previous heartbeat is stale.
func (m *ClientManager) claimOwnership(ctx context.Context, tenantID, serverID string) (bool, error) {
	if m.pool == nil {
		return true, nil // single node with no DB fallback
	}
	var claimed bool
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		var ownerNode string
		var heartbeat time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(owner_node,''), COALESCE(owner_heartbeat, '1970-01-01'::timestamptz)
			FROM mcp_configs WHERE id=$1 FOR UPDATE`,
			serverID,
		).Scan(&ownerNode, &heartbeat); err != nil {
			return err
		}
		if ownerNode == "" || time.Since(heartbeat) > mcpnode.FailoverTimeout {
			_, err := tx.Exec(ctx, `
				UPDATE mcp_configs SET owner_node=$1, owner_heartbeat=NOW()
				WHERE id=$2`, m.nodeID, serverID)
			if err != nil {
				return err
			}
			claimed = true
		}
		return nil
	})
	return claimed, err
}

// StartHeartbeat starts a background goroutine that refreshes the
// owner_heartbeat for every stdio server this node owns.
func (m *ClientManager) StartHeartbeat(interval time.Duration) {
	if interval <= 0 {
		interval = mcpnode.HeartbeatInterval
	}
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
				m.refreshHeartbeat()
			}
		}
	}()
}

func (m *ClientManager) refreshHeartbeat() {
	if m.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.mu.RLock()
	owned := make(map[string]string)
	for key, cfg := range m.configs {
		if cfg.Transport == "stdio" {
			owned[key] = cfg.ID
		}
	}
	m.mu.RUnlock()
	for _, serverID := range owned {
		// Heartbeat uses public schema; mcp_configs is tenant-scoped but
		// owner_node/heartbeat columns are in the public-facing table.
		// We simply refresh via public exec since owner NodeID is
		// not tenant-specific.
		_ = m.pool.AcquireFunc(ctx, func(conn *pgxpool.Conn) error {
			_, err := conn.Exec(ctx,
				`UPDATE mcp_configs SET owner_heartbeat=NOW()
				 WHERE id=$1 AND owner_node=$2`,
				serverID, m.nodeID)
			return err
		})
	}
}

// StartFailoverScanner periodically scans for stdio servers whose owner
// heartbeat has expired and attempts to take them over.
func (m *ClientManager) StartFailoverScanner(interval time.Duration) {
	if interval <= 0 {
		interval = mcpnode.FailoverTimeout
	}
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
				m.scanOrphaned()
			}
		}
	}()
}

func (m *ClientManager) scanOrphaned() {
	if m.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// We need to query across all tenant schemas. For simplicity,
	// iterate tenants and check each one.
	schemas, err := tenantdb.ListTenantSchemas(ctx, m.pool)
	if err != nil {
		m.logger.Warn("failover scan: list tenants failed", zap.Error(err))
		return
	}
	for _, schema := range schemas {
		tenantID := strings.TrimPrefix(schema, "tenant_")
		_ = tenantdb.ExecTenant(ctx, m.pool, func(qctx context.Context, tx pgx.Tx) error {
			rows, qErr := tx.Query(qctx, `
				SELECT id FROM mcp_configs
				WHERE transport='stdio'
				  AND owner_heartbeat < NOW() - $1::interval`,
				fmt.Sprintf("%.0f seconds", mcpnode.FailoverTimeout.Seconds()))
			if qErr != nil {
				return qErr
			}
			defer rows.Close()
			var orphans []string
			for rows.Next() {
				var id string
				if scanErr := rows.Scan(&id); scanErr != nil {
					return scanErr
				}
				orphans = append(orphans, id)
			}
			_ = rows.Err()
			for _, serverID := range orphans {
				claimed, claimErr := m.claimOwnership(ctx, tenantID, serverID)
				if claimErr != nil {
					m.logger.Warn("failover claim error",
						zap.String("tenant_id", tenantID),
						zap.String("server_id", serverID),
						zap.Error(claimErr))
					continue
				}
				if !claimed {
					continue
				}
				m.logger.Warn("failover: taking over orphaned stdio server",
					zap.String("tenant_id", tenantID),
					zap.String("server_id", serverID))
				// Restore single server via Connect.
				cfg, cfgErr := m.GetServerConfig(ctx, serverID)
				if cfgErr != nil {
					m.logger.Warn("failover: get config failed",
						zap.String("server_id", serverID),
						zap.Error(cfgErr))
					continue
				}
				// Remove stale local state if any.
				m.mu.Lock()
				key := tenantKey(tenantID, serverID)
				if old, ok := m.clients[key]; ok {
					delete(m.clients, key)
					delete(m.configs, key)
					m.cache.Delete(key)
					_ = disconnectMCPClient(old)
				}
				m.mu.Unlock()
				if connErr := m.Connect(ctx, cfg, nil); connErr != nil {
					m.logger.Warn("failover: connect failed",
						zap.String("server_id", serverID),
						zap.Error(connErr))
				}
			}
			return nil
		})
	}
}

// Stop 停止管理器
func (m *ClientManager) Stop(ctx context.Context) error {
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.wg.Wait()

	// Cancel serverCtx first — kills all child processes spawned via serverCtx.
	if m.serverCancel != nil {
		m.serverCancel()
	}

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
func (m *ClientManager) UpdateServer(ctx context.Context, cfg *MCPServerConfig, audit *auditdomain.ResourceChangeAuditEvent) error {
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
	return m.Connect(ctx, cfg, audit)
}

// Reconnect reads the saved config for serverID from DB and reconnects.
func (m *ClientManager) Reconnect(ctx context.Context, serverID string) error {
	cfg, err := m.GetServerConfig(ctx, serverID)
	if err != nil {
		return fmt.Errorf("reconnect %s: %w", serverID, err)
	}
	return m.Connect(ctx, cfg, nil)
}

// Delete disconnects the server if connected and hard-deletes its config from DB.
// agent_mcp_servers is removed via ON DELETE CASCADE.
func (m *ClientManager) Delete(ctx context.Context, serverID string, audit *auditdomain.ResourceChangeAuditEvent) error {
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
		if _, err := tx.Exec(ctx, `DELETE FROM mcp_configs WHERE id=$1`, serverID); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

// RemoveTenant disconnects all MCP clients belonging to tenantID across all
// transports, clears memory state, and deletes tenant-scoped DB configs.
func (m *ClientManager) RemoveTenant(ctx context.Context, tenantID string) error {
	prefix := tenantID + ":"
	m.mu.Lock()
	var toRemove []MCPClient
	var toDelete []string
	for key, client := range m.clients {
		if strings.HasPrefix(key, prefix) {
			toRemove = append(toRemove, client)
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		delete(m.clients, key)
		delete(m.configs, key)
		m.cache.Delete(key)
	}
	m.mu.Unlock()

	for _, client := range toRemove {
		if err := disconnectMCPClient(client); err != nil {
			m.logger.Warn("RemoveTenant: disconnect failed",
				zap.String("tenant_id", tenantID), zap.Error(err))
		}
	}

	m.logger.Info("RemoveTenant: evicted all MCP connections",
		zap.String("tenant_id", tenantID), zap.Int("count", len(toRemove)))
	return nil
}

// Quota returns per-tenant connection accounting for the current tenant
// derived from ctx.
func (m *ClientManager) Quota(ctx context.Context) mcpdomain.Quota {
	tenantID := tenantIDFromCtx(ctx)
	limit := m.poolConfig.MaxPerTenant
	if limit <= 0 {
		limit = constants.MCPMaxConnectionsPerTenant
	}
	prefix := tenantID + ":"
	var healthy, unhealthy int
	m.mu.RLock()
	for key, client := range m.clients {
		if strings.HasPrefix(key, prefix) {
			if client.IsHealthy() {
				healthy++
			} else {
				unhealthy++
			}
		}
	}
	used := healthy + unhealthy
	m.mu.RUnlock()
	return mcpdomain.Quota{
		TenantID: tenantID,
		Used:     used,
		Limit:    limit,
		Healthy:  healthy,
		Dead:     unhealthy,
	}
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
			       timeout_sec, version, headers, auth_config, retry_config,
			       COALESCE(system_key, ''), management_mode, enabled, COALESCE(created_by, '')
			FROM mcp_configs WHERE id=$1`, serverID).
			Scan(&out.ID, &out.Name, &out.Transport, &out.Command, &out.URL,
				&argsStr, &envStr, &capsStr, &timeoutSec,
				&out.Version, &hdrsStr, &authStr, &retryStr, &out.SystemKey, &out.ManagementMode, &out.Enabled, &out.CreatedBy)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, mcpdomain.ErrServerNotFound
		}
		return nil, fmt.Errorf("get mcp server config %s: %w", serverID, err)
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
