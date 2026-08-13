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

func (a *toolRegistryPortAdapter) RegisterServer(ctx context.Context, tenantID, serverID string) error {
	return a.r.RegisterServer(ctx, tenantID, serverID)
}

func (a *toolRegistryPortAdapter) UnregisterServer(tenantID, serverID string) error {
	return a.r.UnregisterServer(tenantID, serverID)
}

// ServerManagerAsPort wraps ClientManager to satisfy port.ServerManager.
func ServerManagerAsPort(m *ClientManager) mcpport.ServerManager {
	return m
}
