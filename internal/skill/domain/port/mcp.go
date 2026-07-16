package port

import "context"

// MCPInvoker exposes synchronous MCP tool invocation by (server, tool, args).
type MCPInvoker interface {
	Invoke(ctx context.Context, serverID, tool string, args map[string]any) (map[string]any, error)
}
