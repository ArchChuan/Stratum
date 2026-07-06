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

	// Sub-operation timeouts within a single agent execution turn.
	// These are shorter than AgentExecTimeout so one stuck sub-call
	// doesn't silently consume the entire outer budget.
	AgentDBQueryTimeout      = 5 * time.Second  // single indexed DB read/write
	AgentMemoryInjectTimeout = 10 * time.Second // memory context build (recall + prompt assembly)
	AgentRAGSearchTimeout    = 15 * time.Second // knowledge-base vector search + reranking
	AgentMemoryRecallTimeout = 15 * time.Second // memory recall tool (vector + facts retrieval)

	// LLMStreamIdleTimeout is the maximum silence between consecutive tokens before
	// the stream is aborted. Catches mid-stream network stalls without imposing a
	// flat total-duration cap that would kill legitimately slow models.
	LLMStreamIdleTimeout = 30 * time.Second

	// Router health-check probe
	RouterHealthTimeout = 3 * time.Second

	// MCP client connection idle
	MCPIdleTimeout = 5 * time.Minute

	// Gateway cache entry TTL
	GatewayCacheTTL = 5 * time.Minute

	// KnowledgeIngestTimeout caps the total wall time of a single document
	// ingest job (chunking + all embed batches + persistence) running in a
	// detached background goroutine after the handler has already returned.
	// Independent of any client-side request timeout.
	KnowledgeIngestTimeout = 10 * time.Minute

	// KnowledgeIngestStuckThreshold is how long a doc may sit in
	// ingest_status='processing' before startup recovery marks it failed.
	KnowledgeIngestStuckThreshold = 15 * time.Minute
)
