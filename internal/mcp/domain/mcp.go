// Package domain holds mcp context entities.
package domain

import "time"

// Server is a lightweight projection of an MCP server (read-side).
type Server struct {
	ID, Name, URL string
}

// Tool describes an MCP tool descriptor.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// AuthType identifies the authentication scheme used for HTTP/SSE transports.
type AuthType string

const (
	// AuthTypeNone disables auth (default).
	AuthTypeNone AuthType = "none"
	// AuthTypeBearer attaches a bearer token.
	AuthTypeBearer AuthType = "bearer"
	// AuthTypeAPIKey attaches an API key in a header.
	AuthTypeAPIKey AuthType = "api_key"
	// AuthTypeOAuth2 negotiates an OAuth2 client-credentials flow.
	AuthTypeOAuth2 AuthType = "oauth2"
)

// AuthConfig captures HTTP/SSE auth metadata for an MCP server.
type AuthConfig struct {
	Type               AuthType `json:"type"                            yaml:"type"`
	Token              string   `json:"token,omitempty"                 yaml:"token,omitempty"`
	APIKeyHeader       string   `json:"api_key_header,omitempty"        yaml:"api_key_header,omitempty"`
	APIKeyValue        string   `json:"api_key_value,omitempty"         yaml:"api_key_value,omitempty"`
	OAuth2ClientID     string   `json:"oauth2_client_id,omitempty"      yaml:"oauth2_client_id,omitempty"`
	OAuth2ClientSecret string   `json:"oauth2_client_secret,omitempty"  yaml:"oauth2_client_secret,omitempty"`
	OAuth2TokenURL     string   `json:"oauth2_token_url,omitempty"      yaml:"oauth2_token_url,omitempty"`
	OAuth2Scopes       []string `json:"oauth2_scopes,omitempty"         yaml:"oauth2_scopes,omitempty"`
}

// RetryConfig captures reconnect / exponential-backoff settings.
type RetryConfig struct {
	Enabled        bool    `json:"enabled"          yaml:"enabled"`
	MaxRetries     int     `json:"max_retries"      yaml:"max_retries"`
	InitialDelayMs int64   `json:"initial_delay_ms" yaml:"initial_delay_ms"`
	MaxDelayMs     int64   `json:"max_delay_ms"     yaml:"max_delay_ms"`
	BackoffFactor  float64 `json:"backoff_factor"   yaml:"backoff_factor"`
}

// ServerConfig is the canonical MCP server configuration value object. HTTP
// handlers and infrastructure adapters share this type so the request payload
// shape is owned by the domain rather than infrastructure.
type ServerConfig struct {
	ID           string            `json:"id"                 yaml:"id"`
	Name         string            `json:"name"               yaml:"name"`
	Version      string            `json:"version"            yaml:"version"`
	Transport    string            `json:"transport"          yaml:"transport"`
	Command      string            `json:"command"            yaml:"command"`
	Args         []string          `json:"args"               yaml:"args"`
	URL          string            `json:"url"                yaml:"url"`
	Env          map[string]string `json:"env"                yaml:"env"`
	Headers      map[string]string `json:"headers,omitempty"  yaml:"headers,omitempty"`
	Capabilities []string          `json:"capabilities"       yaml:"capabilities"`
	Timeout      time.Duration     `json:"timeout"            yaml:"timeout"`
	Auth         *AuthConfig       `json:"auth,omitempty"     yaml:"auth,omitempty"`
	Retry        *RetryConfig      `json:"retry,omitempty"    yaml:"retry,omitempty"`
}

// Resource represents an MCP resource in the domain.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ServerInfo is the read model for a connected MCP server.
type ServerInfo struct {
	ID          string
	Name        string
	Version     string
	Protocol    string
	Transport   string
	Tools       []*Tool
	Resources   []*Resource
	Status      string
	LastUpdated time.Time
	Error       string
}

// SkillSummary is the read model for a registered MCP skill.
type SkillSummary struct {
	ID          string
	Name        string
	Description string
	Type        string
}
