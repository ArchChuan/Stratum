package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type AgentRevisionAssignment struct {
	Revision     domain.AgentRevision
	RevisionID   string
	ExperimentID string
	Variant      string
}

type AgentRevisionResolver interface {
	ResolveAgentRevision(
		ctx context.Context,
		tenantID, agentID, subjectID string,
	) (assignment AgentRevisionAssignment, found bool, err error)
}

type MCPRevisionAssignment struct {
	RevisionID   string
	ExperimentID string
	Variant      string
}

type MCPRevisionResolver interface {
	ResolveMCPRevision(
		ctx context.Context,
		tenantID, serverID, subjectID string,
	) (assignment MCPRevisionAssignment, found bool, err error)
}

type KnowledgeRetrievalRevision struct {
	RevisionID     string
	WorkspaceID    string
	WorkspaceName  string
	EmbeddingModel string
	QueryMode      string
	TopK           int
	ScoreThreshold float64
	Reranking      string
	QueryRewrite   string
}

type KnowledgeRevisionAssignment struct {
	Revision     KnowledgeRetrievalRevision
	ExperimentID string
	Variant      string
}

type KnowledgeRevisionPin struct {
	RevisionID   string `json:"revision_id"`
	ExperimentID string `json:"experiment_id"`
	Variant      string `json:"variant"`
}

type KnowledgeRevisionResolver interface {
	ResolveKnowledgeRevision(
		ctx context.Context,
		tenantID, workspaceName, subjectID string,
	) (KnowledgeRevisionAssignment, bool, error)
	LoadKnowledgeRevision(
		ctx context.Context,
		tenantID, workspaceName, revisionID string,
	) (KnowledgeRetrievalRevision, error)
}
