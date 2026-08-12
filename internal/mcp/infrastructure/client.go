// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// ErrTransportTimeout is returned when an HTTP request is abandoned by ctx
// deadline/cancel. It is the safe projection of a url.Error whose original
// text contains the full request URL (credentials must never leak into logs);
// errors.Is(err, context.DeadlineExceeded) callers use this sentinel instead.
var ErrTransportTimeout = errors.New("mcp http transport timed out")

// ErrClientClosed is returned when an in-flight request races with Disconnect
// (or is issued against a client whose transport is no longer available).
var ErrClientClosed = errors.New("mcp client closed")

// unhealthyError reports whether err means the session or transport is dead,
// so the manager's single-flight reconnect should rebuild a fresh session.
//
// ErrTransportFailed is deliberately NOT here: it is the projection of the
// SDK's rejection family (transient 429/502/503/504 plus per-call JSON-RPC
// errors), which the SDK explicitly keeps the connection alive for. Marking
// unhealthy on one application-level tool error would tear down a session the
// SDK deliberately preserved and rebuild it every MCPMinReconnectInterval
// forever — and a successful call never restores the healthy flag, so the
// rebuild loop would be permanent. Real connection death surfaces as
// ErrSessionMissing / ErrClientClosed / ErrTransportTimeout instead, and the
// manager's 30s health check catches any residual dead session.
//
// context.Canceled is excluded: it is the caller's own context, not a
// server-side failure. An oversized SSE frame also does not reach here: the
// SDK reports it as the generic "request terminated without response" (which
// carries no distinguishable symbol), and it fails only that single call
// closed while the session stays usable for new streams (verified in
// TestInteropOversizedLineFailsSafely) — so no reconnect is warranted.
func unhealthyError(err error) bool {
	switch {
	case errors.Is(err, mcpdomain.ErrSessionMissing),
		errors.Is(err, ErrClientClosed),
		errors.Is(err, ErrTransportTimeout):
		return true
	}
	return false
}

// MCPClient 定义 MCP 客户端接口
type MCPClient interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool
	IsHealthy() bool
	CallTool(ctx context.Context, toolName string, input interface{}) (interface{}, error)
	ListTools(ctx context.Context) ([]*MCPTool, error)
	ListResources(ctx context.Context) ([]*MCPResource, error)
	GetServerInfo() *MCPServerInfo
	LastActivity() time.Time
}

// BaseClient 实现基础 MCP 客户端
type BaseClient struct {
	config      *MCPServerConfig
	serverInfo  *MCPServerInfo
	connected   bool
	healthy     bool
	lastHealthy time.Time
	mu          sync.RWMutex
	logger      *zap.Logger
	// origin is the validated, normalized endpoint snapshot captured at
	// doConnect. The round tripper compares every request URL against it
	// before injecting credentials; config is a pointer and may be mutated
	// externally, so the origin is never re-derived at request time.
	origin *url.URL
	// sdk and session hold the SDK client and its initialized session. Both
	// are nil while disconnected. The SDK session is concurrency-safe (ids
	// are allocated per request), so no request mutex is needed.
	sdk     *mcp.Client
	session *mcp.ClientSession
	// urlPolicy gates the SSRF dial policy. Production construction leaves it
	// at the zero value (URLPolicyStrict); only same-package tests flip it to
	// URLPolicyAllowPrivate to exercise httptest loopback servers. No
	// production call site may set it.
	urlPolicy URLPolicyOption
	// lastActivity records the wall time of the most recent CallTool / ListTools / ListResources.
	lastActivity time.Time
}

func NewBaseClient(config *MCPServerConfig, logger *zap.Logger) *BaseClient {
	now := time.Now()
	return &BaseClient{
		config: config,
		serverInfo: &MCPServerInfo{
			ID:        config.ID,
			Name:      config.Name,
			Version:   config.Version,
			Transport: config.Transport,
			Status:    "disconnected",
		},
		logger:       logger.Named("mcp.client").With(zap.String("server_id", config.ID)),
		lastHealthy:  now,
		lastActivity: now,
	}
}

// LastActivity returns the wall time of the most recent tool/resource interaction.
func (c *BaseClient) LastActivity() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastActivity
}

func (c *BaseClient) markActivity() {
	c.mu.Lock()
	c.lastActivity = time.Now()
	c.mu.Unlock()
}

// Connect 连接到 MCP 服务器
func (c *BaseClient) Connect(ctx context.Context) error {
	return c.ensureConnected(ctx)
}

func (c *BaseClient) doConnect(ctx context.Context) error {
	c.logger.Info("connecting to MCP server", zap.String("transport", c.config.Transport))

	// stdio 拒绝的唯一权威落点：租户 stdio 意味着按租户可编辑配置 spawn
	// 任意进程（宿主机任意命令执行）。所有连接路径（service 写入、
	// ReconnectServer 路由、懒恢复、evaluation 基线、performHealthCheck
	// 重连、restoreServer 存量行）最终都收敛于 doConnect，这里拒绝即全链
	// 禁用；后续新增连接路径一律不得绕过。
	if c.config.Transport == "stdio" {
		return mcpdomain.ErrUnsupportedTransport
	}

	switch c.config.Transport {
	case "http", "streamable-http":
	default:
		return fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}

	if err := c.connectStreamableHTTP(ctx); err != nil {
		// ensureConnected holds c.mu here; markUnhealthy would re-acquire it
		// and self-deadlock (non-reentrant RWMutex). It is also a no-op on a
		// fresh client: healthy flips to true only on successful connect.
		c.serverInfo.Status = "error"
		c.serverInfo.Error = "connect_failed"
		c.logger.Error("failed to connect",
			zap.String("error_category", "connect_failed"),
			zap.String("server_id", c.config.ID))
		return err
	}

	c.connected = true
	c.healthy = true
	c.serverInfo.Status = "connected"
	c.serverInfo.Error = ""
	c.serverInfo.Protocol = c.session.InitializeResult().ProtocolVersion
	c.serverInfo.LastUpdated = time.Now()
	c.logger.Info("connected to MCP server",
		zap.String("protocol", c.serverInfo.Protocol))
	return nil
}

// connectStreamableHTTP runs the SDK handshake over the hardened transport.
// Errors are translated to safe sentinels before leaving this function.
func (c *BaseClient) connectStreamableHTTP(ctx context.Context) error {
	if c.config.URL == "" {
		return mcpdomain.ErrInvalidServerURL
	}
	if c.config.Auth != nil && c.config.Auth.Type == mcpdomain.AuthTypeOAuth2 {
		// OAuth2 拒绝落点 = doConnect：client-credentials 直配模型与 SDK
		// OAuth flow 不对齐（SDK 需要 discovery/authorization-server 协商），
		// 本次不做。OAuthHandler 保持 nil，这里拒绝即全链无 OAuth 路径。
		return mcpdomain.ErrUnsupportedAuth
	}
	if c.config.Auth != nil && c.config.Auth.Type == mcpdomain.AuthTypeAPIKey && c.config.Auth.APIKeyHeader == "" {
		// fail closed：禁止默认猜 X-API-Key 之类的通用头。
		return mcpdomain.ErrUnsupportedAuth
	}

	origin, err := ValidateMCPURL(c.config.URL)
	if err != nil {
		return err
	}

	// SDK 要求非 nil Implementation（nil 直接 panic）；clientInfo 不含凭据。
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "stratum", Version: "1.0"}, nil)
	session, err := sdkClient.Connect(ctx, newSDKTransport(origin, c.config, c.urlPolicy),
		&mcp.ClientSessionOptions{ProtocolVersion: constants.MCPProtocolVersion})
	if err != nil {
		return translateSDKError(err)
	}
	c.origin = origin
	c.sdk = sdkClient
	c.session = session
	return nil
}

// markUnhealthy flips the health flag; the manager's single-flight reconnect
// reacts to unhealthy clients.
func (c *BaseClient) markUnhealthy() {
	c.mu.Lock()
	c.healthy = false
	c.mu.Unlock()
}

func (c *BaseClient) ensureConnected(ctx context.Context) error {
	c.mu.RLock()
	if c.connected {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	return c.doConnect(ctx)
}

// Disconnect 断开连接
func (c *BaseClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	c.healthy = false
	c.serverInfo.Status = "disconnected"
	var result error
	if c.session != nil {
		// Session close tears down the standalone SSE stream and any
		// in-flight request channels.
		if err := c.session.Close(); err != nil && result == nil {
			// Close 错误同样是 SDK 原文（可能带 server-controlled 文本），
			// 上抛前投影；Stop/RemoveTenant 会 zap.Error 记录这个错误。
			result = translateSDKError(err)
		}
		c.session = nil
		c.sdk = nil
		c.origin = nil
	}
	c.logger.Info("disconnected from MCP server")
	return result
}

// IsConnected 检查是否已连接
func (c *BaseClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// IsHealthy 检查是否健康
func (c *BaseClient) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy && c.connected
}

// CallTool 调用工具
func (c *BaseClient) CallTool(ctx context.Context, toolName string, input interface{}) (interface{}, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return nil, ErrClientClosed
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: input})
	if err != nil {
		err = translateSDKError(err)
		if unhealthyError(err) {
			// Session or transport gone: flag unhealthy so the manager's
			// single-flight reconnect rebuilds a fresh session. Canceled
			// errors pass through without marking (caller's own context).
			c.markUnhealthy()
		}
		c.logger.Error("failed to call tool",
			zap.String("tool", toolName),
			zap.String("error_category", "call_failed"))
		return nil, err
	}
	c.markActivity()
	return result, nil
}

// ListTools 列出所有工具
func (c *BaseClient) ListTools(ctx context.Context) ([]*MCPTool, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return nil, ErrClientClosed
	}

	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		err = translateSDKError(err)
		if unhealthyError(err) {
			c.markUnhealthy()
		}
		return nil, err
	}
	tools := make([]*MCPTool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, &MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: mcpToolInputSchema(t.InputSchema),
		})
	}

	c.mu.Lock()
	c.serverInfo.Tools = tools
	c.mu.Unlock()
	c.markActivity()

	return tools, nil
}

// mcpToolInputSchema converts the SDK's any-typed input schema to the domain
// map shape (an empty schema when the server sent something exotic).
func mcpToolInputSchema(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// ListResources 列出所有资源
func (c *BaseClient) ListResources(ctx context.Context) ([]*MCPResource, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return nil, ErrClientClosed
	}

	result, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		err = translateSDKError(err)
		if unhealthyError(err) {
			c.markUnhealthy()
		}
		return nil, err
	}
	resources := make([]*MCPResource, 0, len(result.Resources))
	for _, r := range result.Resources {
		resources = append(resources, &MCPResource{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MIMEType,
		})
	}

	c.mu.Lock()
	c.serverInfo.Resources = resources
	c.mu.Unlock()
	c.markActivity()

	return resources, nil
}

// GetServerInfo 获取服务器信息
func (c *BaseClient) GetServerInfo() *MCPServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// HealthCheck 执行健康检查。协议 ping 是最轻的探活方式（无状态副作用，
// 所有符合 2025-06-18 的 server 必须实现）。
func (c *BaseClient) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		c.markUnhealthy()
		return fmt.Errorf("not connected")
	}

	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()
	if session == nil {
		return ErrClientClosed
	}

	err := session.Ping(ctx, &mcp.PingParams{})

	c.mu.Lock()
	// Canceled 是调用方自己的 context，不是服务器故障：health probe 用短
	// 超时时不得把健康的客户端标记为不健康。其余失败（含 ErrTransportFailed
	// 投影的连接死亡）都标 unhealthy，触发 manager 的单飞重连。
	if err != nil && !errors.Is(err, context.Canceled) {
		c.healthy = false
		c.logger.Warn("health check failed",
			zap.String("error_category", "health_check_failed"))
	} else if err == nil {
		c.healthy = true
		c.lastHealthy = time.Now()
	}
	c.mu.Unlock()

	// 返回前翻译：SDK 错误原文可能回显 Authorization，不得上抛。
	return translateSDKError(err)
}
