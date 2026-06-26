package constants

import "time"

const (
	// HTTP server
	HTTPReadHeaderTimeout = 10 * time.Second
	HTTPShutdownTimeout   = 10 * time.Second

	// Agent execution — kept under Cloudflare's 100s proxy read timeout so the
	// origin always closes before CF fires a 524.
	AgentExecTimeout = 90 * time.Second

	// SSE heartbeat interval — keep well below proxy idle-connection timeout (CF: 100s,
	// nginx default: 60s). 5s prevents slow LLMs from triggering proxy disconnects.
	SSEHeartbeatInterval = 5 * time.Second

	// LLM per-request
	LLMRequestTimeout = 60 * time.Second

	// Router health-check probe
	RouterHealthTimeout = 3 * time.Second

	// MCP client connection idle
	MCPIdleTimeout = 5 * time.Minute

	// Gateway cache entry TTL
	GatewayCacheTTL = 5 * time.Minute
)
