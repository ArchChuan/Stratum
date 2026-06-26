// Package application implements MCP bounded context use-cases.
package application

import (
	"context"
	"errors"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	"go.uber.org/zap"
)

// SkillSummary is the read model returned to HTTP for skill listings.
type SkillSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// ServerStatusBreakdown summarises connection state across all known servers.
type ServerStatusBreakdown struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
	Error        int `json:"error"`
}

// MCPService orchestrates MCP HTTP use-cases on top of port interfaces.
type MCPService struct {
	skillRegistry port.SkillRegistry
	manager       port.ServerManager
	logger        *zap.Logger
}

// NewMCPService wires the dependencies. Both registry and manager are required.
func NewMCPService(skillRegistry port.SkillRegistry, manager port.ServerManager, logger *zap.Logger) *MCPService {
	return &MCPService{
		skillRegistry: skillRegistry,
		manager:       manager,
		logger:        logger.Named("mcp.service"),
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

// ExecuteTool invokes the named tool against the registry and returns its result.
func (s *MCPService) ExecuteTool(toolID string, input any) (any, error) {
	return s.skillRegistry.ExecuteSkill(toolID, input)
}

// ListSkills returns summaries of every registered MCP skill.
func (s *MCPService) ListSkills() []SkillSummary {
	skills := s.skillRegistry.GetAllSkills()
	out := make([]SkillSummary, 0, len(skills))
	for _, skill := range skills {
		out = append(out, SkillSummary{
			ID:          skill.GetID(),
			Name:        skill.GetName(),
			Description: skill.GetDescription(),
			Type:        skill.GetType(),
		})
	}
	return out
}

// GetSkill returns a single skill summary, or domain.ErrSkillNotFound.
func (s *MCPService) GetSkill(id string) (*SkillSummary, error) {
	skill := s.skillRegistry.GetSkill(id)
	if skill == nil {
		return nil, domain.ErrSkillNotFound
	}
	return &SkillSummary{
		ID:          skill.GetID(),
		Name:        skill.GetName(),
		Description: skill.GetDescription(),
		Type:        skill.GetType(),
	}, nil
}

// RefreshSkills repopulates the skill registry from connected servers.
func (s *MCPService) RefreshSkills(ctx context.Context) error {
	return s.skillRegistry.RefreshSkills(ctx)
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

// ConnectServer registers a new MCP server config and bootstraps its skills.
// Returns domain.ErrNameConflict on duplicate name.
func (s *MCPService) ConnectServer(ctx context.Context, cfg *domain.ServerConfig) error {
	if err := s.manager.Connect(ctx, cfg); err != nil {
		return err
	}
	s.logger.Info("mcp.server_connected",
		zap.String("server_id", cfg.ID),
		zap.String("server_name", cfg.Name),
	)
	if err := s.skillRegistry.RegisterServer(ctx, cfg.ID); err != nil {
		s.logger.Warn("failed to register MCP skills", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

// DeleteServer permanently removes an MCP server config and cascades to agent relations.
func (s *MCPService) DeleteServer(ctx context.Context, serverID string) error {
	if err := s.manager.Delete(ctx, serverID); err != nil {
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
	if err := s.skillRegistry.RegisterServer(ctx, serverID); err != nil {
		s.logger.Warn("failed to register MCP skills after reconnect", zap.String("server_id", serverID), zap.Error(err))
	}
	return nil
}

// UpdateServer disconnects and reconnects an existing MCP server with new config.
func (s *MCPService) UpdateServer(ctx context.Context, cfg *domain.ServerConfig) error {
	if err := s.manager.UpdateServer(ctx, cfg); err != nil {
		return err
	}
	s.logger.Info("mcp.server_updated", zap.String("server_id", cfg.ID))
	if err := s.skillRegistry.RegisterServer(ctx, cfg.ID); err != nil {
		s.logger.Warn("failed to re-register MCP skills", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

// GetServerConfig returns the full configuration for serverID.
func (s *MCPService) GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error) {
	return s.manager.GetServerConfig(ctx, serverID)
}

// IsNameConflict reports whether err is the canonical mcp name-conflict sentinel.
func IsNameConflict(err error) bool {
	return errors.Is(err, domain.ErrNameConflict)
}
