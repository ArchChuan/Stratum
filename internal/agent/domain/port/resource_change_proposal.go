package port

import (
	"context"
	"encoding/json"
	"fmt"
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
	ResolveBaseline(context.Context, domain.ResourceChangeProposal) (ResourceBaseline, error)
}

type ResourceBaseline struct {
	Fingerprint string
	Projection  json.RawMessage
}

type ResourceChangeApplier interface {
	ApplyResourceChange(context.Context, domain.ProposalEnvelope) (domain.ApplyResult, error)
}

type ResourceApplyOutcome string

const (
	ResourceApplyDefiniteFailure ResourceApplyOutcome = "definite_failure"
	ResourceApplyUnknownOutcome  ResourceApplyOutcome = "unknown_outcome"
)

type ResourceApplyError struct {
	Outcome ResourceApplyOutcome
	Err     error
}

func (e *ResourceApplyError) Error() string {
	return fmt.Sprintf("resource apply %s: %v", e.Outcome, e.Err)
}

func (e *ResourceApplyError) Unwrap() error { return e.Err }
