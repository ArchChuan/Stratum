package constants

import "time"

// MCPProtocolVersion is the MCP protocol version string sent by the client
// during initialize and returned by test servers. All MCP servers and clients
// in this project must use the same version for consistency.
//
// 2025-06-18 adds streamable HTTP transport as the recommended transport,
// while keeping the JSON-RPC initialize handshake model.
const MCPProtocolVersion = "2025-06-18"

// MCP connection limits.
const (
	// MCPMaxTotalConnections is the global cap across all tenants and transports.
	MCPMaxTotalConnections = 100

	// MCPMaxConnectionsPerTenant caps per-tenant concurrent MCP connections.
	MCPMaxConnectionsPerTenant = 10

	// MCPStdioMessageMaxBytes is the per-message read limit for stdio responses.
	MCPStdioMessageMaxBytes = 8 << 20 // 8 MiB

	// MCPIdleEvictionInterval governs how often the idle-eviction scanner runs.
	MCPIdleEvictionInterval = 60 * time.Second

	// MCPHealthCheckInterval is the default health-check period.
	MCPHealthCheckInterval = 30 * time.Second
)
