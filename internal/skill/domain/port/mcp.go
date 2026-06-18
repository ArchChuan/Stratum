package port

import "context"

// MCPInvoker exposes synchronous MCP tool invocation by (server, tool, args).
type MCPInvoker interface {
	Invoke(ctx context.Context, serverID, tool string, args map[string]any) (map[string]any, error)
}

// MCPAdapterReader is the minimal read surface skill providers need from the
// MCP context — list registered skills, check membership, and execute one by
// id. Implemented in api/wiring as a thin adapter over the MCP infrastructure.
type MCPAdapterReader interface {
	SkillIDs() []string
	Has(skillID string) bool
	Execute(ctx context.Context, skillID string, input any) (any, error)
}
