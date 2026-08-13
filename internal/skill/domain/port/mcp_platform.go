package port

import "context"

// MCPPlatformGuard reports whether an MCP server is platform-managed. Defined
// by the consuming context (skill) and implemented by the wiring composition
// root against mcp/application — skill never imports mcp.
//
// Fail closed: an un-wired guard rejects every MCP binding.
type MCPPlatformGuard interface {
	IsPlatformManagedMCPServer(ctx context.Context, tenantID, serverID string) (bool, error)
}
