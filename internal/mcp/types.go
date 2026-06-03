// Package mcp provides MCP (Model Context Protocol) client implementation.
package mcp

import (
	"time"
)

// MCPServerConfig 定义 MCP 服务器配置
type MCPServerConfig struct {
	ID           string            `json:"id" yaml:"id"`
	Name         string            `json:"name" yaml:"name"`
	Version      string            `json:"version" yaml:"version"`
	Transport    string            `json:"transport" yaml:"transport"` // stdio, sse, http
	Command      string            `json:"command" yaml:"command"`
	Args         []string          `json:"args" yaml:"args"`
	URL          string            `json:"url" yaml:"url"`
	Env          map[string]string `json:"env" yaml:"env"`
	Capabilities []string          `json:"capabilities" yaml:"capabilities"`
	Timeout      time.Duration     `json:"timeout" yaml:"timeout"`
}

// MCPTool 定义 MCP 工具
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPResource 定义 MCP 资源
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// MCPCapability 定义 MCP 能力
type MCPCapability struct {
	Type  string      `json:"type"` // tools, resources, prompts
	Items interface{} `json:"items"`
}

// MCPServerInfo 定义 MCP 服务器信息
type MCPServerInfo struct {
	ID          string
	Name        string
	Version     string
	Protocol    string
	Transport   string
	Tools       []*MCPTool
	Resources   []*MCPResource
	Status      string // connected, disconnected, error
	LastUpdated time.Time
	Error       string
}

// MCPToolCall 定义工具调用
type MCPToolCall struct {
	ToolName string      `json:"toolName"`
	Input    interface{} `json:"input"`
}

// MCPToolResult 定义工具结果
type MCPToolResult struct {
	Content interface{} `json:"content"`
	Error   string      `json:"error,omitempty"`
}

// ToolFilter 定义工具过滤条件
type ToolFilter struct {
	ServerID    string
	NamePattern string
	Tags        []string
}

// ResourceFilter 定义资源过滤条件
type ResourceFilter struct {
	ServerID   string
	Type       string
	URIPattern string
}

// MCPRequest 定义 MCP 请求
type MCPRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

// MCPResponse 定义 MCP 响应
type MCPResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}

// ConnectionPoolConfig 定义连接池配置
type ConnectionPoolConfig struct {
	MaxConnections int           `yaml:"max_connections"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxRetries     int           `yaml:"max_retries"`
	RetryBackoff   time.Duration `yaml:"retry_backoff"`
}

// CacheConfig 定义缓存配置
type CacheConfig struct {
	Enabled bool          `yaml:"enabled"`
	TTL     time.Duration `yaml:"ttl"`
	MaxSize int           `yaml:"max_size"`
}

// MonitoringConfig 定义监控配置
type MonitoringConfig struct {
	Enabled             bool          `yaml:"enabled"`
	MetricsInterval     time.Duration `yaml:"metrics_interval"`
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

// MCPConfig 定义 MCP 总体配置
type MCPConfig struct {
	Servers        []*MCPServerConfig    `yaml:"servers"`
	ConnectionPool *ConnectionPoolConfig `yaml:"connection_pool"`
	Cache          *CacheConfig          `yaml:"cache"`
	Monitoring     *MonitoringConfig     `yaml:"monitoring"`
}
