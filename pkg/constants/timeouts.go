package constants

import "time"

const (
	// HTTP server
	HTTPReadHeaderTimeout = 10 * time.Second
	HTTPShutdownTimeout   = 10 * time.Second

	// Agent execution — kept under Cloudflare's 100s proxy read timeout so the
	// origin always closes before CF fires a 524.
	AgentExecTimeout = 90 * time.Second

	// SSE heartbeat interval — must be shorter than Cloudflare proxy read timeout.
	SSEHeartbeatInterval = 15 * time.Second

	// LLM per-request
	LLMRequestTimeout = 60 * time.Second

	// Router health-check probe
	RouterHealthTimeout = 3 * time.Second

	// MCP client connection idle
	MCPIdleTimeout = 5 * time.Minute

	// Gateway cache entry TTL
	GatewayCacheTTL = 5 * time.Minute
)
