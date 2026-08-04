package application

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// OperationProposalService runs the reviewer-facing lifecycle of operation
// proposals: listing pending work, moving a proposal into review, and
// approving or rejecting it. Every mutation re-authorizes the reviewer
// (admin/owner) so a stale client cannot act on another tenant's proposal.
type OperationProposalService struct {
	repo    port.OperationProposalRepo
	roles   port.TenantRoleResolver
	metrics observability.MetricsProvider
	now     func() time.Time
}

// NewOperationProposalService wires the proposal lifecycle with role checks
// and metrics. roles may be nil only in tests; live wiring always supplies
// the tenant role adapter, and a nil resolver fails closed.
func NewOperationProposalService(
	repo port.OperationProposalRepo,
	roles port.TenantRoleResolver,
	metrics observability.MetricsProvider,
) *OperationProposalService {
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	return &OperationProposalService{
		repo: repo, roles: roles, metrics: metrics,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// ListPending returns proposals awaiting review (proposed + reviewing).
func (s *OperationProposalService) ListPending(ctx context.Context, tenantID, userID string) ([]domain.OperationProposal, error) {
	if err := s.authorizeReviewer(ctx, tenantID, userID); err != nil {
		return nil, err
	}
	return s.repo.ListPending(ctx, tenantID)
}

// Get returns a single proposal including its de-sensitised payload summary
// for the approval screen.
func (s *OperationProposalService) Get(ctx context.Context, tenantID, userID, id string) (*domain.OperationProposal, error) {
	if err := s.authorizeReviewer(ctx, tenantID, userID); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, tenantID, id)
}

// StartReview moves a proposal from proposed to reviewing. The reviewer is
// recorded so the review trail shows who picked the proposal up.
func (s *OperationProposalService) StartReview(ctx context.Context, tenantID, userID, id string) error {
	if err := s.authorizeReviewer(ctx, tenantID, userID); err != nil {
		return err
	}
	proposal, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateStatus(ctx, tenantID, id, domain.OpReviewing, userID, ""); err != nil {
		return err
	}
	s.metrics.IncOperationProposal(proposal.OpType, "reviewing")
	return nil
}

// Approve marks a proposal approved and stamps the replay TTL (the repo
// writes expires_at = NOW() + OperationApprovalTTL). Approval does not run
// the operation — it unlocks a single replay by the original proposer.
func (s *OperationProposalService) Approve(ctx context.Context, tenantID, userID, id, note string) error {
	if err := s.authorizeReviewer(ctx, tenantID, userID); err != nil {
		return err
	}
	proposal, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateStatus(ctx, tenantID, id, domain.OpApproved, userID, note); err != nil {
		return err
	}
	s.metrics.IncOperationProposal(proposal.OpType, "approved")
	return nil
}

// Reject permanently declines a proposal. The note is mandatory and bounded
// (see OperationReviewNoteMaxRunes) so the audit trail stays legible.
func (s *OperationProposalService) Reject(ctx context.Context, tenantID, userID, id, note string) error {
	if err := s.authorizeReviewer(ctx, tenantID, userID); err != nil {
		return err
	}
	if err := validateReviewNote(note); err != nil {
		return err
	}
	proposal, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateStatus(ctx, tenantID, id, domain.OpRejected, userID, note); err != nil {
		return err
	}
	s.metrics.IncOperationProposal(proposal.OpType, "rejected")
	return nil
}

// authorizeReviewer fails closed: nil resolver, empty identity, role lookup
// failure, or a non-admin/owner role all deny the action.
func (s *OperationProposalService) authorizeReviewer(ctx context.Context, tenantID, userID string) error {
	if s.roles == nil || tenantID == "" || userID == "" {
		return domain.ErrProposalForbidden
	}
	role, err := s.roles.ResolveTenantRole(ctx, tenantID, userID)
	if err != nil || (role != "admin" && role != "owner") {
		return domain.ErrProposalForbidden
	}
	return nil
}

// validateReviewNote enforces the mandatory, bounded rejection note.
func validateReviewNote(note string) error {
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > OperationReviewNoteMaxRunes {
		return fmt.Errorf("%w: review note required, at most %d runes", domain.ErrProposalInvalid, OperationReviewNoteMaxRunes)
	}
	return nil
}
