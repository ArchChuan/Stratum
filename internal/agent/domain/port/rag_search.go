package port

import "context"

// RAGSearchProvider is the consumer-side port the agent application layer
// uses to query the knowledge bounded context. Implementations live in
// api/wiring as a thin adapter over knowledge.RAGService so handler /
// application never import internal/knowledge/application directly.
type RAGSearchProvider interface {
	// SearchKnowledge fans out the query across the supplied workspace IDs
	// for the given tenant and returns a single concatenated context block
	// suitable for injection into an LLM prompt. Returns ("", nil) when no
	// workspaces are bound or no chunks are retrieved.
	SearchKnowledge(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int) (string, error)
}
