package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/api/http/handler"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
)

type platformAssistantDocsAdapter struct{}

func (platformAssistantDocsAdapter) Search(ctx context.Context, query string) ([]domain.Citation, error) {
	return officialdocs.Search(ctx, query)
}

type platformAssistantProposalAdapter struct {
	service *agentapp.ResourceChangeProposalService
}

type platformAssistantDiagnosticAdapter struct {
	provider agentport.DiagnosticEvidenceProvider
}

func (a platformAssistantDiagnosticAdapter) Collect(
	ctx context.Context,
	request domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	authorization, err := a.provider.Authorize(ctx, request)
	if err != nil {
		return domain.DiagnosticEvidence{}, err
	}
	return a.provider.CollectAuthorized(ctx, authorization.Request)
}

func (a platformAssistantProposalAdapter) Create(
	ctx context.Context,
	input handler.PlatformAssistantProposalInput,
) (domain.ResourceChangeProposalArtifact, error) {
	proposal, err := a.service.CreateProposal(ctx, agentapp.CreateProposalInput{
		TenantID: input.TenantID, ActorID: input.ActorID, Kind: input.Kind,
		Operation: input.Operation, ResourceID: input.ResourceID, Payload: input.Payload,
	})
	artifact := domain.ResourceChangeProposalArtifact{
		ID: proposal.ID, ResourceKind: proposal.ResourceKind, Operation: proposal.Operation,
		Status: proposal.Status, Summary: proposal.Summary, ExpiresAt: proposal.ExpiresAt,
	}
	return artifact, err
}
