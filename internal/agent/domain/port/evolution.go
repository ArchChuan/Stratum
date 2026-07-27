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
