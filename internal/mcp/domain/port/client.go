package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

type ClientManager interface {
	ListTools(ctx context.Context, serverID string) ([]domain.Tool, error)
	Invoke(ctx context.Context, serverID, tool string, args map[string]any) (map[string]any, error)
	Close(ctx context.Context, serverID string) error
}

// RevisionClientManager executes against an immutable, decrypted revision
// config without registering it as the tenant's mutable live server.
type RevisionClientManager interface {
	ListToolsWithConfig(context.Context, *domain.ServerConfig) ([]*domain.Tool, error)
	CallToolWithConfig(context.Context, *domain.ServerConfig, string, any) (any, error)
}
