package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type ProposalRepo interface {
	Create(context.Context, domain.ResourceChangeProposal, domain.ProposalEvent) error
	Get(context.Context, string) (domain.ResourceChangeProposal, error)
	UpdateDraft(context.Context, domain.ResourceChangeProposal, domain.ProposalEvent) error
	Cancel(context.Context, string, string, time.Time) error
	Confirm(context.Context, string, string, time.Time) error
	ClaimApplying(context.Context, string, string, time.Time) (domain.ResourceChangeProposal, error)
	Finish(context.Context, string, domain.ProposalStatus, domain.ApplyResult, domain.ProposalEvent) error
	ListEvents(context.Context, string) ([]domain.ProposalEvent, error)
}

type ProposalAuthorizer interface {
	AuthorizeProposal(context.Context, string, string, domain.ResourceKind, domain.ProposalOperation) error
}

type BaselineResolver interface {
	ResolveBaseline(context.Context, domain.ResourceChangeProposal) (string, error)
}

type ResourceChangeApplier interface {
	ApplyResourceChange(context.Context, domain.ProposalEnvelope) (domain.ApplyResult, error)
}
