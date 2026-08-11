package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// RAGSearchProvider is the consumer-side port the agent application layer
// uses to query the knowledge bounded context. Implementations live in
// api/wiring as a thin adapter over knowledge.RAGService so handler /
// application never import internal/knowledge/application directly.
type RAGSearchProvider interface {
	// SearchKnowledge fans out the query across the supplied workspace IDs
	// for the given tenant and returns a single concatenated context block
	// suitable for injection into an LLM prompt. Returns ("", nil) when no
	// workspaces are bound or no chunks are retrieved. viewerID is the end
	// user whose document whitelist scopes the search.
	SearchKnowledge(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int, viewerID string) (string, error)
}

type KnowledgeRevisionSearchProvider interface {
	SearchKnowledgeRevision(
		ctx context.Context,
		tenantID string,
		revision KnowledgeRetrievalRevision,
		query string,
		viewerID string,
	) (string, error)
}

// RAGSearchSource is an alias of domain.RAGSearchSource: the canonical shape
// lives in domain (domain/agent.go) so AgentResult can carry it without an
// import cycle (port already imports domain for other types).
type RAGSearchSource = domain.RAGSearchSource

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
	SearchKnowledgeWithEvidence(ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int, viewerID string) (RAGSearchEvidence, error)
}
