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

	// reconnectMu guards the single-flight reconnect state shared by
	// performHealthCheck and CallTool: a burst of failures (N concurrent
	// requests against a dead session) collapses into one rebuild per
	// server, and MCPMinReconnectInterval rate-gates repeated rebuilds.
	reconnectMu   sync.Mutex
	reconnecting  map[string]struct{}
	lastReconnect map[string]time.Time

	// serverCtx is the lifecycle context for spawned child processes.
	// Cancelled only when the server shuts down, not on HTTP request end.
	serverCtx    context.Context
	serverCancel context.CancelFunc

	clientFactory func(*MCPServerConfig, *zap.Logger) MCPClient

	// urlPolicy gates the SSRF dial policy of every client the manager
	// constructs. Zero value (URLPolicyStrict) blocks loopback/private
	// targets; deployments that must reach local fixtures flip it via
	// WithURLPolicy.
	urlPolicy URLPolicyOption

	metrics observability.MetricsProvider

	// secretKey 是 mcp_configs 中 env/headers/auth_config 敏感字段的
	// at-rest 加密密钥（AES-256-GCM，来源见 wiring 的 WithSecretKey）。
	// 加解密对零值密钥对称可用，因此零值必须由 WithSecretKey 拒绝——
	// 否则 wiring 漏传时落到公开常量密钥（伪安全），密文形同明文。
	secretKey [32]byte
}

// WithSecretKey 注入 at-rest 加密密钥。由组合根在构造后立即调用；
// 注入前持久化的敏感字段以明文落库，因此必须在使用 manager 前设置。
// 零值密钥拒绝注入（fail closed），让 wiring 启动失败而非静默降级。
func (m *ClientManager) WithSecretKey(key [32]byte) error {
	if key == [32]byte{} {
		return errors.New("mcp client manager: secret key must not be zero value")
	}
	m.secretKey = key
	return nil
}

// NewClientManager 创建新的客户端管理器
func NewClientManager(
	logger *zap.Logger, poolConfig *ConnectionPoolConfig, pool *pgxpool.Pool,
) *ClientManager {
	if poolConfig == nil {
		poolConfig = &ConnectionPoolConfig{
			MaxConnections: 10,
			IdleTimeout:    constants.MCPIdleTimeout,
			MaxRetries:     3,
			RetryBackoff:   1 * time.Second,
		}
	}

	manager := &ClientManager{
		clients:       make(map[string]MCPClient),
		configs:       make(map[string]*MCPServerConfig),
		connecting:    make(map[string]struct{}),
		reconnecting:  make(map[string]struct{}),
		lastReconnect: make(map[string]time.Time),
		cache:         NewCapabilityCache(1000, 1*time.Hour),
		logger:        logger.Named("mcp.client_manager"),
		poolConfig:    poolConfig,
		stopCh:        make(chan struct{}),
		pool:          pool,
		metrics:       observability.NoopMetrics{},
	}
	//nolint:gosec // serverCancel is called in Stop()
	manager.serverCtx, manager.serverCancel = context.WithCancel(context.Background())
	manager.clientFactory = func(cfg *MCPServerConfig, logger *zap.Logger) MCPClient {
		c := NewBaseClient(cfg, logger)
		c.urlPolicy = manager.urlPolicy
		return c
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

// WithURLPolicy sets the SSRF dial policy every client this manager
// constructs applies. Production wiring leaves the default (URLPolicyStrict)
// in place; only deployments that must reach loopback/private fixtures (e2e,
// local verification) set URLPolicyAllowPrivate via config.
func (m *ClientManager) WithURLPolicy(policy URLPolicyOption) {
	m.urlPolicy = policy
}

// WithAllowPrivateClientFactoryForTest dials loopback/private targets
// (URLPolicyAllowPrivate). Cross-package e2e tests exercise the client
// against httptest/fixture servers; production wiring must use
// WithURLPolicy(URLPolicyStrict) instead.
func (m *ClientManager) WithAllowPrivateClientFactoryForTest() {
	m.WithURLPolicy(URLPolicyAllowPrivate)
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

// resourceEditorKind identifies mcp rows in the shared resource_editors table.
const resourceEditorKind = "mcp"

// editorEligible checks, inside the write transaction, that userID currently
// holds role admin or owner in the tenant. Fail closed on any lookup error.
// public.tenant_members is schema-qualified: the transaction search_path
// points at the tenant schema.
func editorEligible(ctx context.Context, tx pgx.Tx, tenantID, userID string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM public.tenant_members
			WHERE tenant_id=$1 AND user_id=$2 AND role IN ('admin','owner'))`,
		tenantID, userID,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("editor role check: %w", err)
	}
	return ok, nil
}

// insertEditors validates and persists the editor set inside the write
// transaction. A non-eligible id fails the whole transaction (fail closed),
// so a forged editor can never be created alongside the resource.
func insertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string) error {
	for _, id := range editorIDs {
		eligible, err := editorEligible(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("%w: user %s", mcpdomain.ErrEditorNotEligible, id)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_editors (resource_kind, resource_id, editor_id, created_by)
			 VALUES ($1,$2,$3,$4)`,
			kind, resourceID, id, createdBy,
		); err != nil {
			return fmt.Errorf("insert editor %s: %w", id, err)
		}
	}
	return nil
}

// revalidateEditorAccess re-checks, inside the write transaction, that the
// actor still qualifies as an editor of this resource: role admin/owner AND
// present in resource_editors. Both checks share the transaction with the
// business UPDATE, closing the check-then-write TOCTOU window.
func revalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string) error {
	eligible, err := editorEligible(ctx, tx, tenantID, actorID)
	if err != nil {
		return err
	}
	if !eligible {
		return mcpdomain.ErrForbidden
	}
	var present bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resource_editors
			WHERE resource_kind=$1 AND resource_id=$2 AND editor_id=$3)`,
		kind, resourceID, actorID,
	).Scan(&present); err != nil {
		return fmt.Errorf("editor membership check: %w", err)
	}
	if !present {
		return mcpdomain.ErrForbidden
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

func (m *ClientManager) persistConnect(ctx context.Context, cfg *MCPServerConfig, editors []string, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if m.pool == nil {
		return nil
	}
	// 敏感字段（env 值、header 值、auth secret）在落库前逐项加密；
	// 深拷贝保证内存中的 cfg 保持明文，不会被二次加密。
	envEnc, err := encryptSecretMap(m.secretKey, cfg.Env)
	if err != nil {
		return fmt.Errorf("persist mcp config %s: %w", cfg.ID, err)
	}
	hdrsEnc, err := encryptSecretMap(m.secretKey, cfg.Headers)
	if err != nil {
		return fmt.Errorf("persist mcp config %s: %w", cfg.ID, err)
	}
	authEnc, err := encryptAuthConfig(m.secretKey, cfg.Auth)
	if err != nil {
		return fmt.Errorf("persist mcp config %s: %w", cfg.ID, err)
	}
	argsB, _ := json.Marshal(cfg.Args)
	envB, _ := json.Marshal(envEnc)
	capsB, _ := json.Marshal(cfg.Capabilities)
	hdrsB, _ := json.Marshal(hdrsEnc)
	authB, _ := json.Marshal(authEnc)
	retryB, _ := json.Marshal(cfg.Retry)
	argsJSON, envJSON, capsJSON, hdrsJSON, authJSON, retryJSON :=
		string(argsB), string(envB), string(capsB), string(hdrsB), string(authB), string(retryB)
	timeoutSec := int(cfg.Timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	err = tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx, `
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
			cfg.Version, hdrsJSON, authJSON, retryJSON, cfg.CreatedBy)
		if execErr != nil {
			return execErr
		}
		tc, ok := tenantdb.FromContext(ctx)
		if !ok || tc.TenantID == "" {
			return fmt.Errorf("persist mcp config %s: missing tenant context", cfg.ID)
		}
		// A granted editor performing an update is re-validated inside the
		// write transaction (role + editor membership), closing the
		// check-then-write TOCTOU window.
		if editorActor != "" {
			if err := revalidateEditorAccess(ctx, tx, tc.TenantID, resourceEditorKind, cfg.ID, editorActor); err != nil {
				return err
			}
		}
		// Create path: the granted editor set lands in the same transaction
		// as the config row; update paths pass nil and leave it untouched.
		if err := insertEditors(ctx, tx, tc.TenantID, resourceEditorKind, cfg.ID, editors, cfg.CreatedBy); err != nil {
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

func (m *ClientManager) persistDisconnect(ctx context.Context, serverID string) error {
	if m.pool == nil {
		return nil
	}
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE mcp_configs SET enabled=false WHERE id=$1`, serverID)
		return err
	})
	if err != nil {
		m.logger.Error("failed to persist disconnect", zap.String("server_id", serverID), zap.Error(err))
		return fmt.Errorf("persist disconnect %s: %w", serverID, err)
	}
	return nil
}

// Connect 连接到 MCP 服务器
func (m *ClientManager) Connect(ctx context.Context, config *MCPServerConfig, editors []string, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error {
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

	tools, resources, err := m.scanCapabilities(ctx, client, config, key, editors, editorActor, audit)
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
	ctx context.Context, client MCPClient, config *MCPServerConfig, key string, editors []string, editorActor string, audit *auditdomain.ResourceChangeAuditEvent,
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
	if err := m.persistConnect(ctx, config, editors, editorActor, audit); err != nil {
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

	if err := m.persistDisconnect(ctx, serverID); err != nil {
		return err
	}

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

	if err := m.Connect(ctx, cfg, nil, "", nil); err != nil {
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

// CallTool 调用工具。调用失败且 client 已不健康（会话丢失或传输死亡）时
// 触发单飞重连，让下一次调用落到新会话上；重连异步进行，不拖慢本次失败。
func (m *ClientManager) CallTool(ctx context.Context, serverID, toolName string, input any) (any, error) {
	client, err := m.getOrRestoreClient(ctx, serverID)
	if err != nil {
		return nil, err
	}
	result, err := client.CallTool(ctx, toolName, input)
	if err != nil && !client.IsHealthy() {
		key := tenantKey(tenantIDFromCtx(ctx), serverID)
		m.scheduleReconnect(key, client)
	}
	return result, err
}

// scheduleReconnect triggers the single-flight reconnect for a failed
// client. Fire-and-forget: the caller's request has already failed, the
// rebuild serves subsequent calls. Bound by a 10s budget so the goroutine
// cannot outlive the server process by more than the connect timeout.
func (m *ClientManager) scheduleReconnect(key string, client MCPClient) {
	m.reconnectMu.Lock()
	_, inflight := m.reconnecting[key]
	gated := time.Since(m.lastReconnect[key]) < constants.MCPMinReconnectInterval
	m.reconnectMu.Unlock()
	if inflight || gated {
		return
	}
	ctx, cancel := context.WithTimeout(m.serverCtx, 10*time.Second)
	go func() {
		defer cancel()
		_, _ = m.reconnectClient(ctx, key, client)
	}()
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

// reconnectClient rebuilds one unhealthy client through the single-flight
// reconnect path shared by performHealthCheck and CallTool. It returns the
// replacement client (nil when skipped or failed) and whether a rebuild
// happened. The gate is per-server: concurrent rebuild attempts collapse
// into the first one (single-flight), and a server is not rebuilt again
// within MCPMinReconnectInterval. Skipped rebuilds leave the candidate in
// place.
func (m *ClientManager) reconnectClient(ctx context.Context, key string, candidate MCPClient) (MCPClient, bool) {
	if !m.beginReconnect(key) {
		return nil, false
	}
	defer m.endReconnect(key)

	// Build from the config snapshot held by the manager: it may have been
	// updated after the candidate was constructed.
	m.mu.RLock()
	cfg := m.configs[key]
	m.mu.RUnlock()
	if cfg == nil {
		return nil, false
	}
	m.logger.Warn("client unhealthy, attempting reconnect", zap.String("key", key))
	fresh := m.clientFactory(cfg, m.logger)
	if fresh == nil {
		return nil, false
	}
	if err := fresh.Connect(ctx); err != nil {
		m.logger.Error("reconnect failed", zap.String("key", key), zap.Error(err))
		m.metrics.IncMCPClientReconnect(cfg.ID)
		return nil, false
	}
	if !m.swapReconnectClient(ctx, key, candidate, fresh) {
		return nil, false
	}
	m.logger.Info("reconnected MCP server", zap.String("key", key))
	return fresh, true
}

// beginReconnect acquires the per-server single-flight slot under the
// MCPMinReconnectInterval rate gate. False means another rebuild is inflight
// or the interval has not elapsed.
func (m *ClientManager) beginReconnect(key string) bool {
	m.reconnectMu.Lock()
	defer m.reconnectMu.Unlock()
	if _, inflight := m.reconnecting[key]; inflight {
		return false
	}
	if time.Since(m.lastReconnect[key]) < constants.MCPMinReconnectInterval {
		return false
	}
	m.reconnecting[key] = struct{}{}
	return true
}

func (m *ClientManager) endReconnect(key string) {
	m.reconnectMu.Lock()
	delete(m.reconnecting, key)
	m.lastReconnect[key] = time.Now()
	m.reconnectMu.Unlock()
}

// swapReconnectClient installs fresh when the server is still configured and
// no newer client won the race (e.g. a lazy reconnect landed first); the loser
// fresh is discarded. Disconnect/Delete remove m.clients and m.configs as a
// pair under the same lock, so re-checking m.configs here is the guard against
// a reconnect racing a teardown and resurrecting a disabled server. A
// displaced candidate is disconnected so the old transport closes
// deterministically.
func (m *ClientManager) swapReconnectClient(ctx context.Context, key string, candidate, fresh MCPClient) bool {
	m.mu.Lock()
	_, stillConfigured := m.configs[key]
	current := m.clients[key]
	installed := stillConfigured && (current == nil || current == candidate)
	if installed {
		m.clients[key] = fresh
	}
	m.mu.Unlock()
	if !installed {
		// server 已被 Disconnect/Delete 移除，或已有更新的 client 胜出：
		// 丢弃 fresh（其 session 已建立，必须断开以免泄漏 transport）。
		_ = fresh.Disconnect(ctx)
		return false
	}
	if candidate != nil {
		if err := candidate.Disconnect(ctx); err != nil {
			m.logger.Warn("displaced client disconnect failed", zap.String("key", key), zap.Error(err))
		}
	}
	return true
}

// performHealthCheck 执行健康检查。不健康 client 的重连经单飞路径
// （reconnectClient），并发失败风暴收敛为每 server 一次重建。
func (m *ClientManager) performHealthCheck() {
	m.mu.RLock()
	var unhealthy []struct {
		key    string
		client MCPClient
	}
	for k, v := range m.clients {
		if !v.IsHealthy() {
			unhealthy = append(unhealthy, struct {
				key    string
				client MCPClient
			}{k, v})
		}
	}
	m.mu.RUnlock()

	for _, entry := range unhealthy {
		ctx, cancel := context.WithTimeout(m.serverCtx, 10*time.Second)
		_, _ = m.reconnectClient(ctx, entry.key, entry.client)
		cancel()
	}
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
		cfg, err := m.configFromDBRow(r)
		if err != nil {
			// fail closed：敏感字段解不开（历史明文或密文损坏）的配置不恢复、
			// 不连接，记 warn 后跳过该 server，不阻塞其他 server 的恢复。
			// metric 计数暴露"静默消失"的行，避免仅靠日志难发现。
			m.metrics.IncComponentError("mcp-server-restore", "decrypt")
			m.logger.Warn("RestoreFromDB: config secrets cannot be decrypted, skipping server",
				zap.String("tenant_id", tenantID),
				zap.String("server_id", r.id),
				zap.Error(err))
			continue
		}
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

// configFromDBRow 把 DB 行还原为明文配置。env/headers/auth 中的敏感字段以
// 密文落库，这里逐项解密；任一字段解密失败（历史明文或密文损坏）返回错误，
// fail closed，禁止把密文当明文使用。
func (m *ClientManager) configFromDBRow(r mcpConfigRow) (*MCPServerConfig, error) {
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
	env, err := decryptSecretMap(m.secretKey, env)
	if err != nil {
		return nil, fmt.Errorf("mcp config %s: env secrets: %w", r.id, err)
	}
	headers, err = decryptSecretMap(m.secretKey, headers)
	if err != nil {
		return nil, fmt.Errorf("mcp config %s: headers secrets: %w", r.id, err)
	}
	auth, err = decryptAuthConfig(m.secretKey, auth)
	if err != nil {
		return nil, fmt.Errorf("mcp config %s: auth secrets: %w", r.id, err)
	}
	return &MCPServerConfig{
		ID: r.id, Name: r.name, Transport: r.transport,
		Command: r.command, URL: r.url, Version: r.version,
		Args: args, Env: env, Capabilities: caps, Headers: headers,
		Auth: auth, Retry: retry,
		Timeout:        time.Duration(r.timeoutSec) * time.Second,
		SystemKey:      r.systemKey,
		ManagementMode: r.managementMode,
		CreatedBy:      r.createdBy,
	}, nil
}

func (m *ClientManager) restoreServer(ctx context.Context, tenantID string, cfg *MCPServerConfig) {
	// stdio 已全链禁用（doConnect 唯一权威拒绝点）：存量 stdio 行不尝试
	// 连接也不 spawn 任何进程，仅记录一次并跳过。改写成 streamable-http
	// 的存量行在下次更新后走正常恢复路径。
	if cfg.Transport == "stdio" {
		m.metrics.IncComponentError("mcp-server-restore", "stdio_disabled")
		m.logger.Warn("RestoreFromDB: skip disabled stdio MCP server",
			zap.String("tenant_id", tenantID),
			zap.String("server_id", cfg.ID))
		return
	}

	connectCtx := tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
		TenantID: tenantID, Role: tenantdb.RoleTenantAdmin,
	})
	if err := m.Connect(connectCtx, cfg, nil, "", nil); err != nil {
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
func (m *ClientManager) UpdateServer(ctx context.Context, cfg *MCPServerConfig, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error {
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
	return m.Connect(ctx, cfg, nil, editorActor, audit)
}

// Reconnect reads the saved config for serverID from DB and reconnects.
func (m *ClientManager) Reconnect(ctx context.Context, serverID string) error {
	cfg, err := m.GetServerConfig(ctx, serverID)
	if err != nil {
		return fmt.Errorf("reconnect %s: %w", serverID, err)
	}
	return m.Connect(ctx, cfg, nil, "", nil)
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
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, serverID,
		); err != nil {
			return fmt.Errorf("delete editors: %w", err)
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

// ListEditors returns the editor ids of a server config, or an empty slice.
func (m *ClientManager) ListEditors(ctx context.Context, tenantID, serverID string) ([]string, error) {
	if m.pool == nil {
		return []string{}, nil
	}
	out := make([]string, 0)
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT editor_id FROM resource_editors
			  WHERE resource_kind=$1 AND resource_id=$2
			  ORDER BY created_at`,
			resourceEditorKind, serverID,
		)
		if err != nil {
			return fmt.Errorf("list editors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan editor: %w", err)
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// ReplaceEditors atomically swaps the editor set. Each editor must hold role
// admin or owner at write time (checked inside the transaction, fail closed);
// a non-eligible id returns mcpdomain.ErrEditorNotEligible. The audit event,
// when non-nil, is written in the same transaction.
func (m *ClientManager) ReplaceEditors(ctx context.Context, tenantID, serverID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if m.pool == nil {
		return nil
	}
	return tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, serverID,
		); err != nil {
			return fmt.Errorf("replace editors: clear: %w", err)
		}
		if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, serverID, editorIDs, createdBy); err != nil {
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
	var row serverConfigRow
	err := tenantdb.ExecTenant(ctx, m.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, name, transport, command, url, args, env, capabilities,
			       timeout_sec, version, headers, auth_config, retry_config,
			       COALESCE(system_key, ''), management_mode, enabled, COALESCE(created_by, '')
			FROM mcp_configs WHERE id=$1`, serverID).
			Scan(&out.ID, &out.Name, &out.Transport, &out.Command, &out.URL,
				&row.args, &row.env, &row.caps, &row.timeoutSec,
				&out.Version, &row.headers, &row.authCfg, &row.retryCfg, &out.SystemKey, &out.ManagementMode, &out.Enabled, &out.CreatedBy)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, mcpdomain.ErrServerNotFound
		}
		return nil, fmt.Errorf("get mcp server config %s: %w", serverID, err)
	}
	if err := m.decodeServerConfigRow(serverID, &out, row); err != nil {
		return nil, err
	}
	return &out, nil
}

// serverConfigRow carries the raw JSONB string columns of a mcp_configs row,
// before JSON decode and secrets decryption.
type serverConfigRow struct {
	args, env, caps, headers, authCfg, retryCfg string
	timeoutSec                                  int
}

// decodeServerConfigRow decodes a raw mcp_configs row into the in-memory
// plaintext config. Sensitive fields (env/headers/auth secrets) are decrypted
// at the DB boundary; a decryption failure (legacy plaintext or corrupted
// ciphertext) fails closed — ciphertext must never be used as plaintext.
func (m *ClientManager) decodeServerConfigRow(serverID string, out *MCPServerConfig, row serverConfigRow) error {
	out.Timeout = time.Duration(row.timeoutSec) * time.Second
	_ = json.Unmarshal([]byte(row.args), &out.Args)
	_ = json.Unmarshal([]byte(row.env), &out.Env)
	_ = json.Unmarshal([]byte(row.caps), &out.Capabilities)
	_ = json.Unmarshal([]byte(row.headers), &out.Headers)
	var auth *MCPAuthConfig
	if row.authCfg != "" && row.authCfg != "null" {
		auth = &MCPAuthConfig{}
		if err := json.Unmarshal([]byte(row.authCfg), auth); err != nil {
			return fmt.Errorf("mcp server config %s: decode auth: %w", serverID, err)
		}
	}
	// 敏感字段解密：失败（历史明文或密文损坏）即 fail closed，
	// 返回"配置无效"错误，禁止把密文当明文使用。
	env, err := decryptSecretMap(m.secretKey, out.Env)
	if err != nil {
		return fmt.Errorf("mcp server config %s: env secrets: %w", serverID, err)
	}
	out.Env = env
	headers, err := decryptSecretMap(m.secretKey, out.Headers)
	if err != nil {
		return fmt.Errorf("mcp server config %s: headers secrets: %w", serverID, err)
	}
	out.Headers = headers
	auth, err = decryptAuthConfig(m.secretKey, auth)
	if err != nil {
		return fmt.Errorf("mcp server config %s: auth secrets: %w", serverID, err)
	}
	out.Auth = auth
	if row.retryCfg != "" && row.retryCfg != "null" {
		out.Retry = &mcpdomain.RetryConfig{}
		_ = json.Unmarshal([]byte(row.retryCfg), out.Retry)
	}
	return nil
}
