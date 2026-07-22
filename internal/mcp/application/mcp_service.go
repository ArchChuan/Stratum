// Package application implements MCP bounded context use-cases.
package application

import (
	"context"
	"errors"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	"go.uber.org/zap"
)

// ServerStatusBreakdown summarises connection state across all known servers.
type ServerStatusBreakdown struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
	Error        int `json:"error"`
}

// MCPService orchestrates MCP HTTP use-cases on top of port interfaces.
type MCPService struct {
	toolRegistry port.ToolRegistry
	manager      port.ServerManager
	toolPolicies port.ToolPolicyRepo
	logger       *zap.Logger
}

func (s *MCPService) SetToolPolicyRepo(repo port.ToolPolicyRepo) { s.toolPolicies = repo }

func (s *MCPService) GetToolRisk(ctx context.Context, serverID, toolName string) (domain.ToolRiskLevel, error) {
	if s.toolPolicies == nil {
		return domain.ToolRiskUnclassified, nil
	}
	policy, ok, err := s.toolPolicies.Get(ctx, serverID, toolName)
	if err != nil || !ok {
		return domain.ToolRiskUnclassified, err
	}
	return policy.RiskLevel, nil
}

func (s *MCPService) ListToolPolicies(ctx context.Context) ([]domain.ToolPolicy, error) {
	if s.toolPolicies == nil {
		return []domain.ToolPolicy{}, nil
	}
	return s.toolPolicies.List(ctx)
}

func (s *MCPService) SetToolPolicy(ctx context.Context, policy domain.ToolPolicy) error {
	if err := policy.RiskLevel.Validate(); err != nil {
		return err
	}
	if policy.ServerID == "" || policy.ToolName == "" {
		return errors.New("serverId and toolName are required")
	}
	if s.toolPolicies == nil {
		return errors.New("MCP tool policy repository not configured")
	}
	return s.toolPolicies.Upsert(ctx, policy)
}

// NewMCPService wires the dependencies. Both registry and manager are required.
func NewMCPService(toolRegistry port.ToolRegistry, manager port.ServerManager, logger *zap.Logger) *MCPService {
	return &MCPService{
		toolRegistry: toolRegistry,
		manager:      manager,
		logger:       logger.Named("mcp.service"),
	}
}

// ListServers returns metadata for every known MCP server.
func (s *MCPService) ListServers(ctx context.Context) []*domain.ServerInfo {
	return s.manager.GetAllServerInfo(ctx)
}

// GetServer returns server info for id, or domain.ErrServerNotFound when absent.
func (s *MCPService) GetServer(ctx context.Context, id string) (*domain.ServerInfo, error) {
	info := s.manager.GetServerInfo(ctx, id)
	if info == nil {
		return nil, domain.ErrServerNotFound
	}
	return info, nil
}

// ListTools fetches the live tool catalogue for serverID.
func (s *MCPService) ListTools(ctx context.Context, serverID string) ([]*domain.Tool, error) {
	return s.manager.ListTools(ctx, serverID)
}

// ListResources fetches the live resource catalogue for serverID.
func (s *MCPService) ListResources(ctx context.Context, serverID string) ([]*domain.Resource, error) {
	return s.manager.ListResources(ctx, serverID)
}

// ServerStatus aggregates connection counts across all servers.
func (s *MCPService) ServerStatus(ctx context.Context) ServerStatusBreakdown {
	servers := s.manager.GetAllServerInfo(ctx)
	out := ServerStatusBreakdown{Total: len(servers)}
	for _, srv := range servers {
		switch srv.Status {
		case "connected":
			out.Connected++
		case "disconnected":
			out.Disconnected++
		case "error":
			out.Error++
		}
	}
	return out
}

// ConnectServer registers a new MCP server config and discovers its tools.
// Returns domain.ErrNameConflict on duplicate name.
func (s *MCPService) ConnectServer(ctx context.Context, cfg *domain.ServerConfig) error {
	if err := s.manager.Connect(ctx, cfg); err != nil {
		return err
	}
	s.logger.Info("mcp.server_connected",
		zap.String("server_id", cfg.ID),
		zap.String("server_name", cfg.Name),
	)
	if err := s.toolRegistry.RegisterServer(ctx, cfg.ID); err != nil {
		s.logger.Warn("failed to register MCP tools", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

// DeleteServer permanently removes an MCP server config and cascades to agent relations.
func (s *MCPService) DeleteServer(ctx context.Context, serverID string) error {
	if err := s.manager.Delete(ctx, serverID); err != nil {
		return err
	}
	if err := s.toolRegistry.UnregisterServer(serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_deleted", zap.String("server_id", serverID))
	return nil
}

// DisconnectServer drops the connection to serverID.
func (s *MCPService) DisconnectServer(ctx context.Context, serverID string) error {
	if err := s.manager.Disconnect(ctx, serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_disconnected", zap.String("server_id", serverID))
	return nil
}

// ReconnectServer restores a previously disconnected MCP server.
func (s *MCPService) ReconnectServer(ctx context.Context, serverID string) error {
	if err := s.manager.Reconnect(ctx, serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_reconnected", zap.String("server_id", serverID))
	if err := s.toolRegistry.RegisterServer(ctx, serverID); err != nil {
		s.logger.Warn("failed to register MCP tools after reconnect", zap.String("server_id", serverID), zap.Error(err))
	}
	return nil
}

// UpdateServer disconnects and reconnects an existing MCP server with new config.
func (s *MCPService) UpdateServer(ctx context.Context, cfg *domain.ServerConfig) error {
	stored, err := s.manager.GetServerConfig(ctx, cfg.ID)
	if err != nil {
		return err
	}
	merged := mergeProtectedConfig(stored, cfg)
	if err := s.manager.UpdateServer(ctx, merged); err != nil {
		return err
	}
	s.logger.Info("mcp.server_updated", zap.String("server_id", cfg.ID))
	if err := s.toolRegistry.RegisterServer(ctx, cfg.ID); err != nil {
		s.logger.Warn("failed to re-register MCP tools", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

func mergeProtectedConfig(stored, incoming *domain.ServerConfig) *domain.ServerConfig {
	merged := cloneServerConfig(incoming)
	if stored == nil {
		return merged
	}
	if stored.Transport == incoming.Transport && incoming.Transport == "stdio" {
		mergeSensitiveValues(merged.Env, stored.Env)
	}
	if stored.Transport == incoming.Transport && incoming.Transport != "stdio" {
		mergeSensitiveValues(merged.Headers, stored.Headers)
	}
	if stored.Auth == nil || merged.Auth == nil || stored.Auth.Type != merged.Auth.Type {
		return merged
	}
	switch merged.Auth.Type {
	case domain.AuthTypeBearer:
		if merged.Auth.Token == "" {
			merged.Auth.Token = stored.Auth.Token
		}
	case domain.AuthTypeAPIKey:
		if merged.Auth.APIKeyValue == "" {
			merged.Auth.APIKeyValue = stored.Auth.APIKeyValue
		}
	case domain.AuthTypeOAuth2:
		if merged.Auth.OAuth2ClientSecret == "" {
			merged.Auth.OAuth2ClientSecret = stored.Auth.OAuth2ClientSecret
		}
	}
	return merged
}

func cloneServerConfig(cfg *domain.ServerConfig) *domain.ServerConfig {
	cloned := *cfg
	cloned.Args = append([]string(nil), cfg.Args...)
	cloned.Capabilities = append([]string(nil), cfg.Capabilities...)
	cloned.Env = cloneStringMap(cfg.Env)
	cloned.Headers = cloneStringMap(cfg.Headers)
	if cfg.Auth != nil {
		auth := *cfg.Auth
		auth.OAuth2Scopes = append([]string(nil), cfg.Auth.OAuth2Scopes...)
		cloned.Auth = &auth
	}
	if cfg.Retry != nil {
		retry := *cfg.Retry
		cloned.Retry = &retry
	}
	return &cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func mergeSensitiveValues(target, stored map[string]string) {
	for key, value := range stored {
		if _, supplied := target[key]; !supplied && domain.IsSensitiveConfigKey(key) {
			target[key] = value
		}
	}
}

// GetServerConfig returns the full configuration for serverID.
func (s *MCPService) GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error) {
	return s.manager.GetServerConfig(ctx, serverID)
}

// IsNameConflict reports whether err is the canonical mcp name-conflict sentinel.
func IsNameConflict(err error) bool {
	return errors.Is(err, domain.ErrNameConflict)
}
