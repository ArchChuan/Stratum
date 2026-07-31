// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

const mcpProtocolVersion = constants.MCPProtocolVersion

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

type InvocationCredentialProvider interface {
	Authorization(ctx context.Context, serverID, toolName string) (string, error)
}

type ManagedHTTPTransportProvider interface {
	Transport() (http.RoundTripper, error)
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
	reqID       atomic.Int32
	// 传输相关字段
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stdinLock  sync.Mutex // serialises writes to stdin
	httpClient *http.Client
	sessionID  string
	// lastActivity records the wall time of the most recent CallTool / ListTools / ListResources.
	lastActivity time.Time
	credentials  InvocationCredentialProvider
	transport    ManagedHTTPTransportProvider
	providerMu   sync.RWMutex
	// negotiatedVersion is set only after a valid initialize response.
	negotiatedVersion string
}

func (c *BaseClient) SetInvocationCredentialProvider(provider InvocationCredentialProvider) {
	c.providerMu.Lock()
	defer c.providerMu.Unlock()
	c.credentials = provider
}

func (c *BaseClient) SetManagedHTTPTransportProvider(provider ManagedHTTPTransportProvider) {
	c.providerMu.Lock()
	defer c.providerMu.Unlock()
	c.transport = provider
}

func (c *BaseClient) nextID() int { return int(c.reqID.Add(1)) }

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

	var err error
	switch c.config.Transport {
	case "stdio":
		err = c.connectStdio(ctx)
	case "http", "streamable-http":
		err = c.connectHTTP(ctx)
	default:
		return fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}

	if err != nil {
		c.serverInfo.Status = "error"
		c.serverInfo.Error = "connect_failed"
		c.logger.Error("failed to connect", zap.String("error_category", "connect_failed"))
		return err
	}

	c.connected = true
	c.healthy = true
	c.serverInfo.Status = "connected"
	c.serverInfo.LastUpdated = time.Now()
	c.logger.Info("connected to MCP server")
	return nil
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
	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close MCP stdin: %w", err))
		}
		c.stdin = nil
	}
	if c.stdout != nil {
		if err := c.stdout.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close MCP stdout: %w", err))
		}
		c.stdout = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		command := c.cmd
		done := make(chan struct{})
		go func() {
			_ = command.Wait()
			close(done)
		}()
		// Kill process group: SIGTERM first, then SIGKILL after grace period.
		pgid := -command.Process.Pid
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, fmt.Errorf("SIGTERM MCP process group: %w", err))
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(pgid, syscall.SIGKILL)
			<-done
		}
		c.cmd = nil
	}
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
		c.httpClient = nil
	}
	c.sessionID = ""
	c.negotiatedVersion = ""
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

	// 构建请求
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": input,
		},
	}

	// 发送请求并获取响应
	resp, err := c.sendRequest(ctx, &req)
	if err != nil {
		c.logger.Error("failed to call tool", zap.String("tool", toolName), zap.Error(err))
		return nil, err
	}
	c.markActivity()

	return resp.Result, nil
}

// ListTools 列出所有工具
func (c *BaseClient) ListTools(ctx context.Context) ([]*MCPTool, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/list",
	}

	resp, err := c.sendRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	var toolsWrapper struct {
		Tools []*MCPTool `json:"tools"`
	}
	data, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(data, &toolsWrapper)
	tools := toolsWrapper.Tools
	if tools == nil {
		tools = []*MCPTool{}
	}

	c.mu.Lock()
	c.serverInfo.Tools = tools
	c.mu.Unlock()
	c.markActivity()

	return tools, nil
}

// ListResources 列出所有资源
func (c *BaseClient) ListResources(ctx context.Context) ([]*MCPResource, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "resources/list",
	}

	resp, err := c.sendRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	var resWrapper struct {
		Resources []*MCPResource `json:"resources"`
	}
	data, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(data, &resWrapper)
	resources := resWrapper.Resources
	if resources == nil {
		resources = []*MCPResource{}
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

// 私有方法

func (c *BaseClient) connectStdio(ctx context.Context) error {
	if c.config.Command == "" {
		return fmt.Errorf("command not specified for stdio transport")
	}

	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...) //nolint:gosec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()
	for k, v := range c.config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("failed to start command: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.serverInfo.Pid = cmd.Process.Pid
	c.serverInfo.StartedAt = time.Now()

	c.logger.Info("stdio connection established", zap.String("command", c.config.Command))
	return nil
}

func (c *BaseClient) connectHTTP(ctx context.Context) error {
	if c.config.URL == "" {
		return fmt.Errorf("URL not specified for HTTP transport")
	}

	transport, err := c.httpTransport()
	if err != nil {
		return err
	}
	c.httpClient = &http.Client{Transport: transport, Timeout: c.config.Timeout}
	initReq := c.newHTTPInitializeRequest()
	resp, err := c.sendHTTPInitialize(ctx, &initReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	initializeResult, err := validateHTTPInitializeResponse(resp, initReq.ID)
	if err != nil {
		return err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	c.negotiatedVersion = initializeResult.ProtocolVersion
	initialized := MCPRequest{JSONRPC: "2.0", Method: "notifications/initialized", Params: map[string]any{}}
	if err := c.sendHTTPNotification(ctx, &initialized); err != nil {
		c.sessionID = ""
		c.negotiatedVersion = ""
		return err
	}
	c.logger.Info("HTTP connection established", zap.String("transport", c.config.Transport),
		zap.String("server_id", c.config.ID))
	return nil
}

type httpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

func (c *BaseClient) newHTTPInitializeRequest() MCPRequest {
	return MCPRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": constants.MCPProtocolVersion,
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "stratum", "version": "1.0"},
		},
	}
}

func (c *BaseClient) sendHTTPInitialize(ctx context.Context, initReq *MCPRequest) (*http.Response, error) {
	data, err := json.Marshal(initReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal initialize request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("MCP HTTP initialize request invalid")
	}
	if err := c.applyHTTPHeaders(ctx, req, false, ""); err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("MCP HTTP initialize transport failed")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("MCP HTTP initialize failed with status %d", resp.StatusCode)
	}
	return resp, nil
}

func validateHTTPInitializeResponse(resp *http.Response, requestID int) (httpInitializeResult, error) {
	initializeResponse, err := decodeHTTPMCPResponse(resp)
	if err != nil {
		return httpInitializeResult{}, fmt.Errorf("decode MCP initialize response: %w", err)
	}
	if err := validateHTTPInitializeEnvelope(initializeResponse, requestID); err != nil {
		return httpInitializeResult{}, err
	}
	var initializeResult httpInitializeResult
	resultData, err := json.Marshal(initializeResponse.Result)
	if err != nil {
		return httpInitializeResult{}, errors.New("MCP initialize result invalid")
	}
	if err := json.Unmarshal(resultData, &initializeResult); err != nil {
		return httpInitializeResult{}, errors.New("MCP initialize result invalid")
	}
	if err := validateHTTPInitializeResult(initializeResult); err != nil {
		return httpInitializeResult{}, err
	}
	return initializeResult, nil
}

func validateHTTPInitializeEnvelope(response *MCPResponse, requestID int) error {
	if response.JSONRPC != "2.0" || response.ID != requestID {
		return errors.New("MCP initialize response envelope invalid")
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return errors.New("MCP initialize protocol error")
	}
	return nil
}

func validateHTTPInitializeResult(result httpInitializeResult) error {
	if result.ProtocolVersion != mcpProtocolVersion {
		return errors.New("MCP initialize selected unsupported protocol version")
	}
	if len(result.Capabilities) == 0 || string(result.Capabilities) == "null" ||
		result.ServerInfo.Name == "" || result.ServerInfo.Version == "" {
		return errors.New("MCP initialize result incomplete")
	}
	return nil
}

func (c *BaseClient) httpTransport() (http.RoundTripper, error) {
	if c.config.SystemKey != platformmcp.SystemServerKey ||
		c.config.ManagementMode != platformmcp.ManagementPlatform {
		return nil, nil
	}
	c.providerMu.RLock()
	provider := c.transport
	c.providerMu.RUnlock()
	if provider == nil {
		return nil, errors.New("Platform MCP mTLS transport provider is not configured")
	}
	transport, err := provider.Transport()
	if err != nil {
		return nil, fmt.Errorf("create Platform MCP mTLS transport: %w", err)
	}
	return transport, nil
}

func (c *BaseClient) sendRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	var resp *MCPResponse
	var err error
	switch c.config.Transport {
	case "stdio":
		resp, err = c.sendStdioRequest(ctx, req)
	case "http", "streamable-http":
		resp, err = c.sendHTTPRequest(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}
	if err != nil {
		return nil, err
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		return nil, fmt.Errorf("MCP protocol error")
	}
	return resp, nil
}

func (c *BaseClient) sendStdioRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	if c.stdin == nil || c.stdout == nil {
		return nil, fmt.Errorf("stdio connection not established")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Serialise writes across concurrent goroutines.
	c.stdinLock.Lock()
	_, writeErr := c.stdin.Write(append(data, '\n'))
	c.stdinLock.Unlock()
	if writeErr != nil {
		return nil, fmt.Errorf("failed to write to stdin: %w", writeErr)
	}

	// Read response in a goroutine so ctx cancellation is honoured.
	// Limit reads to 8 MiB to prevent memory exhaustion.
	type readResult struct {
		resp *MCPResponse
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		limitReader := io.LimitReader(c.stdout, 8<<20)
		reader := bufio.NewReader(limitReader)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			ch <- readResult{err: fmt.Errorf("failed to read from stdout: %w", err)}
			return
		}
		var resp MCPResponse
		if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
			ch <- readResult{err: fmt.Errorf("failed to unmarshal response: %w", err)}
			return
		}
		ch <- readResult{resp: &resp}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}

func (c *BaseClient) sendHTTPRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	if c.httpClient == nil {
		return nil, fmt.Errorf("HTTP client not initialized")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("MCP HTTP request invalid")
	}

	if err := c.applyHTTPHeaders(ctx, httpReq, true, toolNameFromRequest(req)); err != nil {
		return nil, err
	}
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, errors.New("MCP HTTP transport failed")
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP HTTP request failed with status %d", resp.StatusCode)
	}

	return decodeHTTPMCPResponse(resp)
}

func (c *BaseClient) sendHTTPNotification(ctx context.Context, notification *MCPRequest) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal MCP notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, bytes.NewReader(data))
	if err != nil {
		return errors.New("MCP HTTP notification request invalid")
	}
	if err := c.applyHTTPHeaders(ctx, req, true, ""); err != nil {
		return err
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.New("MCP HTTP initialized notification transport failed")
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("MCP HTTP initialized notification failed with status %d", resp.StatusCode)
	}
	return nil
}

func decodeHTTPMCPResponse(resp *http.Response) (*MCPResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read MCP response: %w", err)
	}
	jsonBody := body
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
		for _, line := range bytes.Split(body, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				jsonBody = bytes.TrimSpace(line[5:])
				break
			}
		}
	}
	var response MCPResponse
	if err := json.Unmarshal(jsonBody, &response); err != nil {
		return nil, fmt.Errorf("unmarshal MCP response: %w", err)
	}
	return &response, nil
}

func (c *BaseClient) applyHTTPHeaders(
	ctx context.Context,
	req *http.Request,
	includeProtocolVersion bool,
	toolName string,
) error {
	for name, value := range c.config.Headers {
		req.Header.Set(name, value)
	}
	if err := c.applyInvocationCredential(ctx, req, toolName); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if includeProtocolVersion {
		req.Header.Set("MCP-Protocol-Version", c.negotiatedVersion)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	return nil
}

func (c *BaseClient) applyInvocationCredential(ctx context.Context, req *http.Request, toolName string) error {
	if toolName == "" || c.config.SystemKey != platformmcp.SystemServerKey ||
		c.config.ManagementMode != platformmcp.ManagementPlatform {
		return nil
	}
	c.providerMu.RLock()
	provider := c.credentials
	c.providerMu.RUnlock()
	if provider == nil {
		return errors.New("Platform MCP invocation credential provider is not configured")
	}
	authorization, err := provider.Authorization(ctx, c.config.ID, toolName)
	if err != nil {
		return fmt.Errorf("issue Platform MCP invocation credential: %w", err)
	}
	if !strings.HasPrefix(authorization, "Bearer ") {
		return errors.New("Platform MCP invocation credential is invalid")
	}
	req.Header.Set("Authorization", authorization)
	return nil
}

func toolNameFromRequest(req *MCPRequest) string {
	if req.Method != "tools/call" {
		return ""
	}
	params, ok := req.Params.(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := params["name"].(string)
	return name
}

// HealthCheck 执行健康检查
func (c *BaseClient) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		c.mu.Lock()
		c.healthy = false
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}

	// 网络调用在锁外执行，不阻塞并发读
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/list",
	}

	_, err := c.sendRequest(ctx, &req)

	c.mu.Lock()
	if err != nil {
		c.healthy = false
		c.logger.Warn("health check failed", zap.Error(err))
	} else {
		c.healthy = true
		c.lastHealthy = time.Now()
	}
	c.mu.Unlock()

	return err
}
