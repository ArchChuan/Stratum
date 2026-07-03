package port

import "context"

// MemoryInjector builds a memory-context string (summaries + entities + long-term)
// for injection into the system prompt. Implemented by infrastructure (e.g. memory/pipeline).
type MemoryInjector interface {
	BuildContext(ctx context.Context, ic InjectionContext) (string, error)
}

// InjectionContext carries the identifiers needed to look up relevant memory
// for a given conversation turn. Pure VO, no behavior.
type InjectionContext struct {
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	Query          string
	Scope          string
}

// RecallMemoryFn executes the recall_memory tool. The infrastructure-side handler
// is constructed in wiring and bound here as a function so the application layer
// stays free of pipeline / pgx / vector dependencies.
type RecallMemoryFn func(ctx context.Context, tenantID, userID, agentID, scope string, input map[string]any) (string, error)

// MemoryRecaller / MemoryWriter are reserved for future agent-side memory ops.
type MemoryRecaller interface {
	Recall(ctx context.Context, tenantID, userID, query string, limit int) ([]string, error)
}

type MemoryWriter interface {
	Write(ctx context.Context, tenantID, userID, content string, importance float32) error
}

// AgentMemoryCleaner is the consumer-side port for clearing all memories tied to an agent.
// Implemented by memory.MemoryService.
type AgentMemoryCleaner interface {
	ClearAgentMemories(ctx context.Context, tenantID, agentID string) error
}

// BufferMemoryFn buffers a single conversation message for async memory extraction.
// Wiring constructs this as a closure over memory.MemoryService so application stays free
// of cross-context imports.
type BufferMemoryFn func(ctx context.Context, tenantID, userID, agentID, conversationID, scope, role, content string) error
