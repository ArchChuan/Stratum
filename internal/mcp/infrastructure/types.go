// Package infrastructure provides MCP (Model Context Protocol) client implementation.
package infrastructure

import (
	"time"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

// AuthType is re-exported from domain for backwards compatibility within this
// package and existing tests.
type AuthType = domain.AuthType

const (
	AuthTypeNone   = domain.AuthTypeNone
	AuthTypeBearer = domain.AuthTypeBearer
	AuthTypeAPIKey = domain.AuthTypeAPIKey
	AuthTypeOAuth2 = domain.AuthTypeOAuth2
)

// MCPAuthConfig aliases the domain auth value object.
type MCPAuthConfig = domain.AuthConfig

// MCPRetryConfig aliases the domain retry value object.
type MCPRetryConfig = domain.RetryConfig

// MCPServerConfig aliases the domain server config; HTTP requests bind the
// domain shape and the infrastructure adapters consume it directly.
type MCPServerConfig = domain.ServerConfig

// _ keeps the time import live until other infrastructure types depend on it.
var _ = time.Second

// MCPTool aliases the domain type for backwards compatibility within this package.
type MCPTool = domain.Tool

// MCPResource aliases the domain type for backwards compatibility within this package.
type MCPResource = domain.Resource

// MCPServerInfo aliases the domain type for backwards compatibility within this package.
type MCPServerInfo = domain.ServerInfo

// MCPCapability 定义 MCP 能力
type MCPCapability struct {
	Type  string      `json:"type"` // tools, resources, prompts
	Items interface{} `json:"items"`
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
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// MCPResponse 定义 MCP 响应
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc,omitempty"`
	ID      int         `json:"id,omitempty"`
	Result  interface{} `json:"result"`
	Error   string      `json:"error,omitempty"`
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
