// Package mcp provides MCP (Model Context Protocol) client implementation.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"go.uber.org/zap"
)

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
	retries     int

	// 传输相关字段
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	httpClient *http.Client
	sseConn    net.Conn
	requestID  int64
	reqMu      sync.Mutex
}

// NewBaseClient 创建新的基础客户端
func NewBaseClient(config *MCPServerConfig, logger *zap.Logger) *BaseClient {
	return &BaseClient{
		config: config,
		serverInfo: &MCPServerInfo{
			ID:        config.ID,
			Name:      config.Name,
			Version:   config.Version,
			Transport: config.Transport,
			Status:    "disconnected",
		},
		logger:      logger.Named("mcp.client").With(zap.String("server_id", config.ID)),
		lastHealthy: time.Now(),
	}
}

// Connect 连接到 MCP 服务器
func (c *BaseClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	c.logger.Info("connecting to MCP server", zap.String("transport", c.config.Transport))

	// 根据传输方式选择连接方法
	var err error
	switch c.config.Transport {
	case "stdio":
		err = c.connectStdio(ctx)
	case "sse":
		err = c.connectSSE(ctx)
	case "http":
		err = c.connectHTTP(ctx)
	default:
		return fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}

	if err != nil {
		c.serverInfo.Status = "error"
		c.serverInfo.Error = err.Error()
		c.logger.Error("failed to connect", zap.Error(err))
		return err
	}

	c.connected = true
	c.healthy = true
	c.serverInfo.Status = "connected"
	c.serverInfo.LastUpdated = time.Now()
	c.logger.Info("connected to MCP server")

	return nil
}

// Disconnect 断开连接
func (c *BaseClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false
	c.healthy = false
	c.serverInfo.Status = "disconnected"
	c.logger.Info("disconnected from MCP server")

	return nil
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
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	// 构建请求
	req := MCPRequest{
		Method: "tools/call",
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

	return resp.Result, nil
}

// ListTools 列出所有工具
func (c *BaseClient) ListTools(ctx context.Context) ([]*MCPTool, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	req := MCPRequest{
		Method: "tools/list",
	}

	resp, err := c.sendRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	// 解析响应
	var tools []*MCPTool
	data, _ := json.Marshal(resp.Result)
	json.Unmarshal(data, &tools)

	c.mu.Lock()
	c.serverInfo.Tools = tools
	c.mu.Unlock()

	return tools, nil
}

// ListResources 列出所有资源
func (c *BaseClient) ListResources(ctx context.Context) ([]*MCPResource, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	req := MCPRequest{
		Method: "resources/list",
	}

	resp, err := c.sendRequest(ctx, &req)
	if err != nil {
		return nil, err
	}

	// 解析响应
	var resources []*MCPResource
	data, _ := json.Marshal(resp.Result)
	json.Unmarshal(data, &resources)

	c.mu.Lock()
	c.serverInfo.Resources = resources
	c.mu.Unlock()

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

	cmd := exec.CommandContext(ctx, c.config.Command, c.config.Args...)
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
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout

	c.logger.Info("stdio connection established", zap.String("command", c.config.Command))
	return nil
}

func (c *BaseClient) connectSSE(ctx context.Context) error {
	if c.config.URL == "" {
		return fmt.Errorf("URL not specified for SSE transport")
	}

	// 验证 URL 可达性
	req, err := http.NewRequestWithContext(ctx, "GET", c.config.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to SSE server: %w", err)
	}
	defer resp.Body.Close()

	c.httpClient = &http.Client{Timeout: c.config.Timeout}
	c.logger.Info("SSE connection established", zap.String("url", c.config.URL))
	return nil
}

func (c *BaseClient) connectHTTP(ctx context.Context) error {
	if c.config.URL == "" {
		return fmt.Errorf("URL not specified for HTTP transport")
	}

	c.httpClient = &http.Client{Timeout: c.config.Timeout}

	// 测试连接
	req, err := http.NewRequestWithContext(ctx, "GET", c.config.URL+"/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP server returned status %d", resp.StatusCode)
	}

	c.logger.Info("HTTP connection established", zap.String("url", c.config.URL))
	return nil
}

func (c *BaseClient) sendRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	switch c.config.Transport {
	case "stdio":
		return c.sendStdioRequest(ctx, req)
	case "sse":
		return c.sendSSERequest(ctx, req)
	case "http":
		return c.sendHTTPRequest(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", c.config.Transport)
	}
}

func (c *BaseClient) sendStdioRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	if c.stdin == nil || c.stdout == nil {
		return nil, fmt.Errorf("stdio connection not established")
	}

	// 发送请求
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("failed to write to stdin: %w", err)
	}

	// 读取响应
	reader := bufio.NewReader(c.stdout)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read from stdout: %w", err)
	}

	var resp MCPResponse
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, nil
}

func (c *BaseClient) sendSSERequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	if c.httpClient == nil {
		return nil, fmt.Errorf("SSE connection not established")
	}

	// 构建 HTTP 请求
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var mcpResp MCPResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &mcpResp, nil
}

func (c *BaseClient) sendHTTPRequest(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	if c.httpClient == nil {
		return nil, fmt.Errorf("HTTP client not initialized")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.URL+"/rpc", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var mcpResp MCPResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &mcpResp, nil
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
		Method: "tools/list",
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
