package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

// ServerManager is the consumer-side port for MCP server lifecycle operations.
type ServerManager interface {
	Connect(ctx context.Context, cfg *domain.ServerConfig) error
	Disconnect(ctx context.Context, serverID string) error
	Reconnect(ctx context.Context, serverID string) error
	UpdateServer(ctx context.Context, cfg *domain.ServerConfig) error
	Delete(ctx context.Context, serverID string) error
	GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error)
	ListTools(ctx context.Context, serverID string) ([]*domain.Tool, error)
	ListResources(ctx context.Context, serverID string) ([]*domain.Resource, error)
	GetServerInfo(ctx context.Context, serverID string) *domain.ServerInfo
	GetAllServerInfo(ctx context.Context) []*domain.ServerInfo
}

// SkillRegistry is the consumer-side port for MCP skill registration and execution.
type SkillRegistry interface {
	RegisterServer(ctx context.Context, serverID string) error
	ExecuteSkill(skillID string, input any) (any, error)
	GetSkill(id string) SkillAccessor
	GetAllSkills() []SkillAccessor
	RefreshSkills(ctx context.Context) error
}

// SkillAccessor provides read-only access to a registered MCP skill.
type SkillAccessor interface {
	GetID() string
	GetName() string
	GetDescription() string
	GetType() string
}
