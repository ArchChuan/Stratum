package port

import "context"

// MCPToolProvider is the consumer-side port for retrieving MCP tool definitions.
// The handler uses this to build extra tools for the ReAct loop without importing
// MCP infrastructure directly.
type MCPToolProvider interface {
	ToolsForServer(ctx context.Context, serverID string) []ToolDefinition
}

type MCPToolExecutor interface {
	ExecuteMCPTool(ctx context.Context, serverID, toolName string, input map[string]any) (any, error)
}
