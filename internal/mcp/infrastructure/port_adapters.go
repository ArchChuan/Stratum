package infrastructure

import (
	"context"

	mcpport "github.com/byteBuilderX/stratum/internal/mcp/domain/port"
)

// ToolRegistryAsPort wraps MCPToolRegistry to satisfy port.ToolRegistry.
func ToolRegistryAsPort(r *MCPToolRegistry) mcpport.ToolRegistry {
	return &toolRegistryPortAdapter{r: r}
}

type toolRegistryPortAdapter struct {
	r *MCPToolRegistry
}

func (a *toolRegistryPortAdapter) RegisterServer(ctx context.Context, serverID string) error {
	return a.r.RegisterServer(ctx, serverID)
}

func (a *toolRegistryPortAdapter) UnregisterServer(serverID string) error {
	return a.r.UnregisterServer(serverID)
}

// ServerManagerAsPort wraps ClientManager to satisfy port.ServerManager.
func ServerManagerAsPort(m *ClientManager) mcpport.ServerManager {
	return m
}
