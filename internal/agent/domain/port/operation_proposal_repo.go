package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// OperationProposalRepo persists operation approvals in the per-tenant schema.
type OperationProposalRepo interface {
	Insert(ctx context.Context, p domain.OperationProposal) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.OperationProposal, error)
	ListPending(ctx context.Context, tenantID string) ([]domain.OperationProposal, error)
	UpdateStatus(ctx context.Context, tenantID, id string, status domain.OpProposalStatus, reviewerID, note string) error
	// HasPending reports whether an open (proposed/reviewing/approved)
	// proposal already exists for the fingerprint.
	HasPending(ctx context.Context, tenantID, fingerprint string) (bool, error)
	// ConsumeApproved atomically consumes a single approved proposal for the
	// fingerprint: transitions approved → executed. Returns true only when a
	// matching unexpired approval owned by proposerID existed — single-use,
	// actor-bound, TTL-enforced replay.
	ConsumeApproved(ctx context.Context, tenantID, fingerprint, proposerID string) (bool, error)
	// ListByProposer returns every proposal (any status) raised by a proposer,
	// newest first. Backs the member-side "my requests" view in the permission
	// approvals tab; carries no reviewer gate.
	ListByProposer(ctx context.Context, tenantID, proposerID string) ([]domain.OperationProposal, error)
	// ListHistory returns a paginated slice of proposals (any status, newest
	// first) plus the total count matching the filter. proposerID == "" returns
	// the whole tenant (admin/owner view); otherwise only that proposer's
	// proposals are returned (member view). Backs the permission-approvals
	// history tab.
	ListHistory(ctx context.Context, tenantID, proposerID string, page, pageSize int) ([]domain.OperationProposal, int, error)
}
