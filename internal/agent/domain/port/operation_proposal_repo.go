package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// OperationProposalRepo persists operation approvals. Implemented in
// Phase 2 (T8) as a per-tenant PG table.
type OperationProposalRepo interface {
	Insert(ctx context.Context, p domain.OperationProposal) error
	GetByID(ctx context.Context, tenantID, id string) (*domain.OperationProposal, error)
	ListPending(ctx context.Context, tenantID string) ([]domain.OperationProposal, error)
	UpdateStatus(ctx context.Context, tenantID, id string, status domain.OpProposalStatus, reviewerID, note string) error
	HasApproved(ctx context.Context, tenantID, fingerprint string) (bool, error)
}
