package infrastructure

import (
	"context"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

// RegistryAsAgentToolProvider wraps MCPToolRegistry to satisfy agentport.MCPToolProvider.
func RegistryAsAgentToolProvider(r *MCPToolRegistry) agentport.MCPToolProvider {
	return &mcpAgentToolAdapter{r: r}
}

type mcpAgentToolAdapter struct {
	r *MCPToolRegistry
}

func (a *mcpAgentToolAdapter) ToolsForServer(ctx context.Context, serverID string) []agentport.ToolDefinition {
	adapter := a.r.GetCatalogForServer(serverID)
	if adapter == nil {
		return nil
	}
	handles := adapter.GetAllTools()
	tools := make([]agentport.ToolDefinition, 0, len(handles))
	for _, w := range handles {
		tools = append(tools, agentport.ToolDefinition{
			Name:         w.GetID(),
			Description:  w.Tool.Description,
			InputSchema:  w.Tool.InputSchema,
			ProviderType: "mcp",
			ProviderID:   serverID,
			ServerID:     serverID,
			CapabilityID: w.Tool.Name,
			NodeType:     "mcp",
		})
	}
	return tools
}
