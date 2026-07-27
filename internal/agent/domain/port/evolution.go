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
