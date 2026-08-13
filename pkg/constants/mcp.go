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

	// MCPIdleEvictionInterval governs how often the idle-eviction scanner runs.
	MCPIdleEvictionInterval = 60 * time.Second

	// MCPHealthCheckInterval is the default health-check period.
	MCPHealthCheckInterval = 30 * time.Second
)

// MCP outbound HTTP hardening: SSRF guard + resource bounds for the
// streamable-http transport. These numbers are never inlined in the client.
const (
	// MCPDefaultDialTimeout bounds DNS resolution and TCP connect for outbound
	// MCP traffic.
	MCPDefaultDialTimeout = 10 * time.Second
	// MCPTLSHandshakeTimeout bounds the TLS handshake for https endpoints.
	MCPTLSHandshakeTimeout = 10 * time.Second
	// MCPResponseHeaderTimeout bounds the wait for response headers on JSON
	// responses. The SSE stream itself is long-lived by design and unbounded.
	MCPResponseHeaderTimeout = 30 * time.Second
	// MCPHTTPMaxResponseBytes caps JSON response bodies (16 MiB). SSE bodies
	// are not capped in total; they are bounded per line by MCPSSEFrameMaxBytes.
	MCPHTTPMaxResponseBytes = 16 << 20
	// MCPSSEFrameMaxBytes caps a single SSE data line. The SDK reads SSE
	// frames line-by-line without its own bound; without this guard a
	// malicious server could stream one unbounded line and OOM the process.
	MCPSSEFrameMaxBytes = 16 << 20
	// MCPMaxRedirects bounds redirect chains for MCP endpoints.
	MCPMaxRedirects = 3
	// MCPMinReconnectInterval gates per-server health-check reconnects so a
	// burst of N simultaneous failures causes one reconnect per server, not a
	// reconnect storm.
	MCPMinReconnectInterval = 30 * time.Second
	// MCPMaxRetries is the SDK StreamableClientTransport reconnect budget
	// (connectSSE attempts plus no-progress handleSSE reconnects, exponential
	// backoff 1s x1.5 capped at 30s; transient 429/502/503/504 responses do
	// not consume the budget because the session stays alive).
	MCPMaxRetries = 2
)
