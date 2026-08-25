package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
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
	// grantEditor 批准即授予的落库闭包（按 resourceType 分发 agent/skill/
	// knowledge_doc），由 api/wiring 注入；nil 时 grant_editor 批准 fail-closed。
	grantEditor func(ctx context.Context, tenantID, resourceType, resourceID, editorID string) error
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

// WithGrantEditor injects the grant dispatch used by approveGrantEditor. The
// closure is wired in api/wiring by resource type (agent / skill /
// knowledge_doc); without it grant_editor approvals fail closed.
func (s *OperationProposalService) WithGrantEditor(fn func(ctx context.Context, tenantID, resourceType, resourceID, editorID string) error) {
	s.grantEditor = fn
}

// ListMine returns every proposal raised by the actor (any status, newest
// first). Unlike ListPending there is no reviewer gate: any tenant member can
// see their own requests in the permission approvals tab.
func (s *OperationProposalService) ListMine(ctx context.Context, tenantID, userID string) ([]domain.OperationProposal, error) {
	if tenantID == "" || userID == "" {
		return nil, domain.ErrProposalForbidden
	}
	return s.repo.ListByProposer(ctx, tenantID, userID)
}

// ProposeGrantEditor raises a grant_editor proposal: a tenant member requests
// editor (agent / skill) or view (knowledge_doc) whitelist access to a
// resource. Any member may apply; dedupe reuses the fingerprint HasPending
// check so a second request for the same resource/actor is rejected while one
// is open. Approval grants the whitelist directly (see approveGrantEditor).
func (s *OperationProposalService) ProposeGrantEditor(ctx context.Context, tenantID, actorID, resourceType, resourceID, resourceName string) error {
	if tenantID == "" || actorID == "" || resourceType == "" || resourceID == "" {
		return domain.ErrProposalInvalid
	}
	// The handler derives resourceType from the route; this server-side
	// whitelist is a second gate so a stale or hand-written client cannot
	// raise an unknown-kind proposal (deep defence behind the route check).
	switch resourceType {
	case "agent", "skill", "knowledge_doc":
	default:
		return domain.ErrProposalInvalid
	}
	fingerprint := fmt.Sprintf("grant_editor|%s|%s|%s", resourceType, resourceID, actorID)
	hasPending, err := s.repo.HasPending(ctx, tenantID, fingerprint)
	if err != nil {
		return err
	}
	if hasPending {
		return domain.ErrOperationProposalPending
	}
	summary, err := json.Marshal(map[string]any{
		"resourceType": resourceType,
		"resourceId":   resourceID,
		"resourceName": resourceName,
		"applicant":    actorID,
		"action":       "grant_editor_access",
	})
	if err != nil {
		return fmt.Errorf("%w: marshal grant payload: %v", domain.ErrProposalInvalid, err)
	}
	p := domain.OperationProposal{
		ID:             uuid.NewString(),
		TenantID:       tenantID,
		AgentID:        resourceID, // agent_id 列承载任意资源 id（无外键约束）
		OpType:         string(port.OpGrantEditor),
		Delegation:     string(port.DelegationNone),
		Fingerprint:    fingerprint,
		PayloadSummary: summary,
		Status:         domain.OpProposed,
		ProposerID:     actorID,
		CreatedAt:      s.now(),
		UpdatedAt:      s.now(),
	}
	return s.repo.Insert(ctx, p)
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
	// grant_editor 批准即授予：不进入 approved→proposer-replay 两段式，直接
	// 执行授予并落 executed 终态（授予失败则批准不生效，提案保持 pending）。
	if proposal.OpType == string(port.OpGrantEditor) {
		return s.approveGrantEditor(ctx, tenantID, userID, id, note, proposal)
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

// approveGrantEditor executes a grant_editor approval: the whitelist grant is
// applied immediately (approval IS the grant — no proposer replay, unlike
// self_modify), then the proposal is moved straight to the terminal executed
// state. A failed grant fails the approval and leaves the proposal pending so
// the reviewer can retry or reject.
func (s *OperationProposalService) approveGrantEditor(ctx context.Context, tenantID, reviewerID, id, note string, proposal *domain.OperationProposal) error {
	if s.grantEditor == nil {
		return fmt.Errorf("approve grant editor: %w", ErrGateUnavailable)
	}
	// A rejected or already-resolved proposal is terminal: never run the grant
	// on a stale approval. The atomic UpdateStatus guard below is the backstop;
	// this check keeps the grant itself off a resolved proposal.
	if proposal.Status != domain.OpProposed && proposal.Status != domain.OpReviewing {
		return domain.ErrOperationProposalResolved
	}
	var payload struct {
		ResourceType string `json:"resourceType"`
		ResourceID   string `json:"resourceId"`
	}
	if err := json.Unmarshal(proposal.PayloadSummary, &payload); err != nil || payload.ResourceType == "" || payload.ResourceID == "" {
		return fmt.Errorf("%w: malformed grant_editor payload", domain.ErrProposalInvalid)
	}
	if proposal.ProposerID == "" {
		return fmt.Errorf("%w: grant_editor proposal missing applicant", domain.ErrProposalInvalid)
	}
	if err := s.grantEditor(ctx, tenantID, payload.ResourceType, payload.ResourceID, proposal.ProposerID); err != nil {
		return fmt.Errorf("approve grant editor: grant %s on %s: %w", proposal.ProposerID, payload.ResourceType, err)
	}
	if err := s.repo.UpdateStatus(ctx, tenantID, id, domain.OpExecuted, reviewerID, note); err != nil {
		return err
	}
	s.metrics.IncOperationProposal(proposal.OpType, "approved")
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
