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

// ToolRegistry registers live MCP tools discovered from a server.
type ToolRegistry interface {
	RegisterServer(ctx context.Context, serverID string) error
	UnregisterServer(serverID string) error
}
