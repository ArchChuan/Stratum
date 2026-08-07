package dto

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

// MCPServerConfigRequest is the tenant-editable MCP configuration. Managed identity fields are never converted.
type MCPServerConfigRequest struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Transport    string              `json:"transport"`
	Command      string              `json:"command"`
	Args         []string            `json:"args"`
	URL          string              `json:"url"`
	Env          map[string]string   `json:"env"`
	Headers      map[string]string   `json:"headers,omitempty"`
	Capabilities []string            `json:"capabilities"`
	Timeout      time.Duration       `json:"timeout"`
	Auth         *domain.AuthConfig  `json:"auth,omitempty"`
	Retry        *domain.RetryConfig `json:"retry,omitempty"`
	SystemKey    json.RawMessage     `json:"system_key"`
	// Editors is the initial granted editor set (create path); each id must
	// hold role admin or owner at write time.
	Editors []string `json:"editors"`
}

func (r MCPServerConfigRequest) ServerConfig() (*domain.ServerConfig, error) {
	if len(r.SystemKey) != 0 {
		return nil, errors.New("system_key is managed by Stratum")
	}
	return &domain.ServerConfig{
		ID: r.ID, Name: r.Name, Version: r.Version, Transport: r.Transport, Command: r.Command,
		Args: r.Args, URL: r.URL, Env: r.Env, Headers: r.Headers, Capabilities: r.Capabilities,
		Timeout: r.Timeout, Auth: r.Auth, Retry: r.Retry,
	}, nil
}

// MCPAuthConfigResponse exposes authentication metadata without credential values.
type MCPAuthConfigResponse struct {
	Type                 domain.AuthType `json:"type"`
	APIKeyHeader         string          `json:"api_key_header,omitempty"`
	OAuth2ClientID       string          `json:"oauth2_client_id,omitempty"`
	OAuth2TokenURL       string          `json:"oauth2_token_url,omitempty"`
	OAuth2Scopes         []string        `json:"oauth2_scopes,omitempty"`
	CredentialConfigured bool            `json:"credential_configured"`
}

// MCPServerConfigResponse is the browser-safe projection of an MCP server config.
type MCPServerConfigResponse struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Transport      string                 `json:"transport"`
	Command        string                 `json:"command"`
	Args           []string               `json:"args"`
	URL            string                 `json:"url"`
	Env            map[string]string      `json:"env"`
	Headers        map[string]string      `json:"headers,omitempty"`
	Capabilities   []string               `json:"capabilities"`
	Timeout        time.Duration          `json:"timeout"`
	Auth           *MCPAuthConfigResponse `json:"auth,omitempty"`
	Retry          *domain.RetryConfig    `json:"retry,omitempty"`
	ManagementMode string                 `json:"management_mode"`
	Editors        []string               `json:"editors"`
}

func NewMCPServerConfigResponse(cfg *domain.ServerConfig) MCPServerConfigResponse {
	response := MCPServerConfigResponse{
		ID:             cfg.ID,
		Name:           cfg.Name,
		Version:        cfg.Version,
		Transport:      cfg.Transport,
		Command:        cfg.Command,
		Args:           append([]string(nil), cfg.Args...),
		URL:            cfg.URL,
		Env:            filterMCPConfigValues(cfg.Env),
		Headers:        filterMCPConfigValues(cfg.Headers),
		Capabilities:   append([]string(nil), cfg.Capabilities...),
		Timeout:        cfg.Timeout,
		Retry:          cfg.Retry,
		ManagementMode: cfg.ManagementMode,
	}
	if cfg.Auth != nil {
		response.Auth = &MCPAuthConfigResponse{
			Type:                 cfg.Auth.Type,
			APIKeyHeader:         cfg.Auth.APIKeyHeader,
			OAuth2ClientID:       cfg.Auth.OAuth2ClientID,
			OAuth2TokenURL:       cfg.Auth.OAuth2TokenURL,
			OAuth2Scopes:         append([]string(nil), cfg.Auth.OAuth2Scopes...),
			CredentialConfigured: authCredentialConfigured(cfg.Auth),
		}
	}
	return response
}

func IsSensitiveMCPConfigKey(key string) bool {
	return domain.IsSensitiveConfigKey(key)
}

func filterMCPConfigValues(values map[string]string) map[string]string {
	filtered := make(map[string]string)
	for key, value := range values {
		if !IsSensitiveMCPConfigKey(key) {
			filtered[key] = value
		}
	}
	return filtered
}

func authCredentialConfigured(auth *domain.AuthConfig) bool {
	switch auth.Type {
	case domain.AuthTypeBearer:
		return auth.Token != ""
	case domain.AuthTypeAPIKey:
		return auth.APIKeyValue != ""
	case domain.AuthTypeOAuth2:
		return auth.OAuth2ClientSecret != ""
	default:
		return false
	}
}
