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

type KnowledgeRevisionSearchProvider interface {
	SearchKnowledgeRevision(
		ctx context.Context,
		tenantID string,
		revision KnowledgeRetrievalRevision,
		query string,
	) (string, error)
}

// RAGSearchSource is per-chunk retrieval provenance attached to a knowledge
// search result. Score is only meaningful when HasScore is true (vector
// retrieval); keyword-mode results carry no score.
type RAGSearchSource struct {
	WorkspaceID   string
	WorkspaceName string
	ChunkID       string
	Score         float64
	HasScore      bool
}

// RAGSearchEvidence is the structured result of a knowledge search: the
// concatenated context block plus the chunk-level provenance that produced
// it. Tool observations merge the provenance into their metadata so traces
// record retrieval evidence alongside the injected context.
type RAGSearchEvidence struct {
	Content string
	Sources []RAGSearchSource
}

// RAGSearchEvidenceProvider is an optional capability of wiring adapters
// backed by a RAG engine that returns chunk-level provenance. Agent
// execution prefers it over RAGSearchProvider when both are available; the
// base SearchKnowledge contract is untouched for adapters that cannot
// provide evidence.
type RAGSearchEvidenceProvider interface {
	SearchKnowledgeWithEvidence(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int) (RAGSearchEvidence, error)
}
