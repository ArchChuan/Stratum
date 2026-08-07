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
)

const mcpProtocolVersion = constants.MCPProtocolVersion

const (
	// maxStdioReadBytes caps a single stdio response line to prevent memory exhaustion.
	maxStdioReadBytes = 8 << 20
	// processTerminateGrace is how long Disconnect waits for a stdio child to
	// exit after SIGTERM before escalating to SIGKILL.
	processTerminateGrace = 5 * time.Second
)

// ErrClientClosed is returned when an in-flight request races with Disconnect
// (or is issued against a client whose transport is no longer available).
var ErrClientClosed = errors.New("mcp client closed")

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
	reqID       atomic.Int32
	// 传输相关字段
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	stdinLock  sync.Mutex // serialises writes to stdin
	readMu     sync.Mutex // serialises reads on stdout (single-reader)
	reqMu      sync.Mutex // serialises request periods (at most one in-flight request per client)
	httpClient *http.Client
	sessionID  string
	// lastActivity records the wall time of the most recent CallTool / ListTools / ListResources.
	lastActivity time.Time
	// negotiatedVersion is set only after a valid initialize response.
	negotiatedVersion string
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
		case <-time.After(processTerminateGrace):
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

	c.httpClient = &http.Client{Timeout: c.config.Timeout}
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
	// 本函数由 ensureConnected 的写锁调用（doConnect），状态字段已受保护；
	// 不能走 RLock 快照路径（Go RWMutex 不可重入，锁内再 RLock 自死锁），
	// 因此直接传字段快照给请求内核。
	if err := c.sendNotificationWith(c.httpClient, c.sessionID, c.negotiatedVersion, ctx, &initialized); err != nil {
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

func (c *BaseClient) sendRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	// At most one request may be in flight per client: stdio responses are
	// consumed as the next line on a shared pipe, so concurrent requests would
	// steal each other's responses. The lock spans the whole request period
	// (write, read, timeout) but never connection lifecycle methods.
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
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
	// Capture the transport under the lock: Disconnect closes and nils the
	// fields at any time, so checking the fields and using them without the
	// lock would race and could panic on a nil interface.
	c.mu.RLock()
	stdin := c.stdin
	stdout := c.stdout
	c.mu.RUnlock()
	if stdin == nil || stdout == nil {
		return nil, ErrClientClosed
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Serialise writes across concurrent goroutines. A write on a pipe closed
	// by Disconnect surfaces as os.ErrClosed, never a panic.
	c.stdinLock.Lock()
	_, writeErr := stdin.Write(append(data, '\n'))
	c.stdinLock.Unlock()
	if writeErr != nil {
		if errors.Is(writeErr, os.ErrClosed) {
			return nil, fmt.Errorf("%w: stdin closed during request", ErrClientClosed)
		}
		return nil, fmt.Errorf("failed to write to stdin: %w", writeErr)
	}

	// Single-reader: at most one in-flight read on stdout at a time. The lock
	// is held until the reader goroutine has exited, so a later request can
	// never start reading while a previous reader is still blocked.
	c.readMu.Lock()
	defer c.readMu.Unlock()

	// Read response in a goroutine so ctx cancellation is honoured. The caller
	// always waits for the goroutine (wg) before returning, so a timeout can
	// never leak it.
	type readResult struct {
		resp *MCPResponse
		err  error
	}
	ch := make(chan readResult, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		limitReader := io.LimitReader(stdout, maxStdioReadBytes)
		reader := bufio.NewReader(limitReader)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				ch <- readResult{err: fmt.Errorf("%w: stdout closed during request", ErrClientClosed)}
				return
			}
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
		// Wake the blocked read so the reader goroutine exits boundedly, then
		// wait for it before releasing the single-reader lock. The stdout pipe
		// is an *os.File in production (exec and os.Pipe), which supports read
		// deadlines; the deadline is cleared so the next read is not cut short.
		if f, ok := stdout.(*os.File); ok {
			_ = f.SetReadDeadline(time.Now())
		}
		wg.Wait()
		if f, ok := stdout.(*os.File); ok {
			_ = f.SetReadDeadline(time.Time{})
		}
		return nil, ctx.Err()
	case r := <-ch:
		wg.Wait()
		return r.resp, r.err
	}
}

func (c *BaseClient) sendHTTPRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	// Capture shared state under the lock: Disconnect nils httpClient and
	// clears sessionID/negotiatedVersion, so lock-free use would race and
	// could panic on a nil client.
	c.mu.RLock()
	httpClient := c.httpClient
	sessionID := c.sessionID
	negotiatedVersion := c.negotiatedVersion
	c.mu.RUnlock()
	if httpClient == nil {
		return nil, ErrClientClosed
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("MCP HTTP request invalid")
	}

	if err := c.applyHTTPHeaders(ctx, httpReq, true, negotiatedVersion); err != nil {
		return nil, err
	}
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, errors.New("MCP HTTP transport failed")
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP HTTP request failed with status %d", resp.StatusCode)
	}

	return decodeHTTPMCPResponse(resp)
}

// sendNotificationWith 是 notification 发送的请求内核。前三个参数必须是
// 锁内快照：connectHTTP 初始化路径由 ensureConnected 的写锁保护，
// 直接传字段值（见 connectHTTP）。
func (c *BaseClient) sendNotificationWith(httpClient *http.Client, sessionID, negotiatedVersion string, ctx context.Context, notification *MCPRequest) error {
	data, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal MCP notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.URL, bytes.NewReader(data))
	if err != nil {
		return errors.New("MCP HTTP notification request invalid")
	}
	if err := c.applyHTTPHeaders(ctx, req, true, negotiatedVersion); err != nil {
		return err
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := httpClient.Do(req)
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
	negotiatedVersion string,
) error {
	for name, value := range c.config.Headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if includeProtocolVersion {
		req.Header.Set("MCP-Protocol-Version", negotiatedVersion)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	return nil
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
