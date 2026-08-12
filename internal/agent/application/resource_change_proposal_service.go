package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
)

const proposalReviewTTL = 24 * time.Hour

type CreateProposalInput struct {
	TenantID       string
	ConversationID string
	ActorID        string
	Kind           domain.ResourceKind
	Operation      domain.ProposalOperation
	ResourceID     string
	Payload        json.RawMessage
}

type UpdateProposalInput struct {
	TenantID, ActorID, ProposalID string
	Payload                       json.RawMessage
}

type ResourceChangeProposalService struct {
	repo       port.ProposalRepo
	authorizer port.ProposalAuthorizer
	baseline   port.BaselineResolver
	appliers   map[domain.ResourceKind]port.ResourceChangeApplier
	metrics    observability.MetricsProvider
	now        func() time.Time
	newID      func() string
}

const proposalApplyingRecoveryLease = 5 * time.Minute

func NewResourceChangeProposalService(
	repo port.ProposalRepo,
	authorizer port.ProposalAuthorizer,
	baseline port.BaselineResolver,
	appliers map[domain.ResourceKind]port.ResourceChangeApplier,
	metrics observability.MetricsProvider,
) *ResourceChangeProposalService {
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	return &ResourceChangeProposalService{
		repo: repo, authorizer: authorizer, baseline: baseline, appliers: appliers, metrics: metrics,
		now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString,
	}
}

func (s *ResourceChangeProposalService) CreateProposal(
	ctx context.Context,
	in CreateProposalInput,
) (domain.ResourceChangeProposal, error) {
	if err := s.authorize(ctx, in.TenantID, in.ActorID, in.Kind, in.Operation); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	now := s.now()
	proposal := domain.ResourceChangeProposal{
		ID: s.newID(), TenantID: in.TenantID, ConversationID: in.ConversationID, ProposerID: in.ActorID,
		ResourceKind: in.Kind, ResourceID: in.ResourceID, Operation: in.Operation,
		Status: domain.StatusReadyForReview, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(proposalReviewTTL),
		Summary: fmt.Sprintf("%s %s", in.Operation, in.Kind),
	}
	decoded, err := domain.DecodeProposalPayload(in.Kind, in.Operation, in.Payload)
	if err == nil {
		err = validateProposalPayload(decoded)
	}
	if err != nil {
		proposal.Status = domain.StatusInvalid
		proposal.Payload = json.RawMessage(`{}`)
		createErr := s.repo.Create(ctx, proposal, domain.ProposalEvent{
			ActorID: in.ActorID, ToStatus: domain.StatusInvalid, Code: "proposal_invalid",
			Summary: "Proposal payload rejected by strict validation.", CreatedAt: now,
		})
		if createErr != nil {
			return domain.ResourceChangeProposal{}, fmt.Errorf("persist invalid proposal: %w", createErr)
		}
		s.metrics.IncResourceProposal(string(in.Kind), string(in.Operation), string(domain.StatusInvalid))
		return proposal, fmt.Errorf("%w: payload rejected", domain.ErrProposalInvalid)
	}
	proposal.Payload = append(json.RawMessage(nil), in.Payload...)
	if in.Operation == domain.OperationUpdate {
		if in.ResourceID == "" || s.baseline == nil {
			return s.persistInvalid(ctx, proposal, "Update requires a target resource and baseline resolver.")
		}
		baseline, baselineErr := s.baseline.ResolveBaseline(ctx, proposal)
		if baselineErr != nil {
			return domain.ResourceChangeProposal{}, fmt.Errorf("resolve proposal baseline: %w", baselineErr)
		}
		if baseline.Fingerprint == "" || len(baseline.Projection) == 0 {
			return s.persistInvalid(ctx, proposal, "Target resource has no stable baseline.")
		}
		proposal.BaselineFingerprint = baseline.Fingerprint
		proposal.BaselineProjection = append(json.RawMessage(nil), baseline.Projection...)
	}
	if err := proposal.Validate(now); err != nil {
		return s.persistInvalid(ctx, proposal, "Proposal envelope rejected by validation.")
	}
	if err := s.repo.Create(ctx, proposal, domain.ProposalEvent{
		ActorID: in.ActorID, ToStatus: domain.StatusReadyForReview, CreatedAt: now,
	}); err != nil {
		return domain.ResourceChangeProposal{}, fmt.Errorf("create resource proposal: %w", err)
	}
	s.metrics.IncResourceProposal(string(in.Kind), string(in.Operation), string(domain.StatusReadyForReview))
	return proposal, nil
}

func (s *ResourceChangeProposalService) Get(ctx context.Context, tenantID, actorID, id string) (domain.ResourceChangeProposal, error) {
	if err := s.authorize(ctx, tenantID, actorID, "", ""); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	proposal, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	if proposal.TenantID != "" && proposal.TenantID != tenantID {
		return domain.ResourceChangeProposal{}, domain.ErrProposalForbidden
	}
	return proposal, nil
}

func (s *ResourceChangeProposalService) UpdateDraft(
	ctx context.Context,
	in UpdateProposalInput,
) (domain.ResourceChangeProposal, error) {
	proposal, err := s.Get(ctx, in.TenantID, in.ActorID, in.ProposalID)
	if err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	if err := s.authorize(ctx, in.TenantID, in.ActorID, proposal.ResourceKind, proposal.Operation); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	if proposal.Status != domain.StatusDraft && proposal.Status != domain.StatusReadyForReview {
		return domain.ResourceChangeProposal{}, domain.ErrProposalAlreadyClaimed
	}
	decoded, err := domain.DecodeProposalPayload(proposal.ResourceKind, proposal.Operation, in.Payload)
	if err != nil {
		return domain.ResourceChangeProposal{}, domain.ErrProposalInvalid
	}
	if err := validateProposalPayload(decoded); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	fromStatus := proposal.Status
	proposal.Payload = append(json.RawMessage(nil), in.Payload...)
	proposal.UpdatedAt = s.now()
	proposal.Status = domain.StatusReadyForReview
	if proposal.Operation == domain.OperationUpdate {
		baseline, err := s.baseline.ResolveBaseline(ctx, proposal)
		if err != nil {
			return domain.ResourceChangeProposal{}, fmt.Errorf("resolve edited proposal baseline: %w", err)
		}
		proposal.BaselineFingerprint = baseline.Fingerprint
		proposal.BaselineProjection = append(json.RawMessage(nil), baseline.Projection...)
	}
	if err := s.repo.UpdateDraft(ctx, proposal, domain.ProposalEvent{
		ActorID: in.ActorID, FromStatus: fromStatus, ToStatus: domain.StatusReadyForReview,
		Summary: "Proposal draft updated.", CreatedAt: proposal.UpdatedAt,
	}); err != nil {
		return domain.ResourceChangeProposal{}, fmt.Errorf("update proposal draft: %w", err)
	}
	proposal.EditCount++
	return proposal, nil
}

func (s *ResourceChangeProposalService) Cancel(ctx context.Context, tenantID, actorID, id string) error {
	proposal, err := s.Get(ctx, tenantID, actorID, id)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, tenantID, actorID, proposal.ResourceKind, proposal.Operation); err != nil {
		return err
	}
	return s.repo.Cancel(ctx, id, actorID, s.now())
}

func (s *ResourceChangeProposalService) ListEvents(
	ctx context.Context,
	tenantID, actorID, id string,
) ([]domain.ProposalEvent, error) {
	if _, err := s.Get(ctx, tenantID, actorID, id); err != nil {
		return nil, err
	}
	return s.repo.ListEvents(ctx, id)
}

func (s *ResourceChangeProposalService) ConfirmAndApply(
	ctx context.Context,
	tenantID, proposalID, actorID string,
) (domain.ResourceChangeProposal, error) {
	if err := s.authorize(ctx, tenantID, actorID, "", ""); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	proposal, err := s.repo.Get(ctx, proposalID)
	if err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	now := s.now()
	if err := s.repo.Confirm(ctx, proposalID, actorID, now); err != nil {
		if !errors.Is(err, domain.ErrProposalAlreadyClaimed) {
			return domain.ResourceChangeProposal{}, err
		}
		current, getErr := s.repo.Get(ctx, proposalID)
		if getErr != nil {
			return domain.ResourceChangeProposal{}, errors.Join(err, getErr)
		}
		switch current.Status {
		case domain.StatusConfirmed:
			proposal = current
		case domain.StatusApplying:
			if current.UpdatedAt.Add(proposalApplyingRecoveryLease).After(now) {
				return domain.ResourceChangeProposal{}, err
			}
			if finishErr := s.finish(ctx, current, domain.StatusUnknownOutcome, domain.ApplyResult{},
				"proposal_interrupted_applying", actorID); finishErr != nil {
				return domain.ResourceChangeProposal{}, errors.Join(domain.ErrProposalUnknownOutcome, finishErr)
			}
			s.metrics.IncResourceProposal(string(current.ResourceKind), string(current.Operation),
				string(domain.StatusUnknownOutcome))
			return domain.ResourceChangeProposal{}, domain.ErrProposalUnknownOutcome
		default:
			return domain.ResourceChangeProposal{}, err
		}
	}
	claimed, err := s.repo.ClaimApplying(ctx, proposalID, actorID, now)
	if err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	s.metrics.RecordResourceProposalDraftEdits(
		string(claimed.ResourceKind), string(claimed.Operation), claimed.EditCount,
	)
	if err := s.authorize(ctx, tenantID, actorID, claimed.ResourceKind, claimed.Operation); err != nil {
		finishErr := s.finish(ctx, claimed, domain.StatusFailed, domain.ApplyResult{}, "proposal_forbidden", actorID)
		if finishErr != nil {
			return domain.ResourceChangeProposal{}, errors.Join(domain.ErrProposalForbidden, finishErr)
		}
		return domain.ResourceChangeProposal{}, domain.ErrProposalForbidden
	}
	if claimed.Operation == domain.OperationUpdate {
		current, resolveErr := s.baseline.ResolveBaseline(ctx, claimed)
		if resolveErr != nil {
			if finishErr := s.finish(ctx, claimed, domain.StatusFailed, domain.ApplyResult{},
				"proposal_baseline_unavailable", actorID); finishErr != nil {
				return domain.ResourceChangeProposal{}, errors.Join(domain.ErrProposalApplyFailed, resolveErr, finishErr)
			}
			s.metrics.IncResourceProposal(string(claimed.ResourceKind), string(claimed.Operation), string(domain.StatusFailed))
			return domain.ResourceChangeProposal{}, fmt.Errorf("%w: recheck proposal baseline: %v",
				domain.ErrProposalApplyFailed, resolveErr)
		}
		if current.Fingerprint != claimed.BaselineFingerprint {
			if finishErr := s.finish(ctx, claimed, domain.StatusStale, domain.ApplyResult{}, "proposal_stale", actorID); finishErr != nil {
				return domain.ResourceChangeProposal{}, errors.Join(domain.ErrProposalStale, finishErr)
			}
			s.metrics.IncResourceProposal(string(claimed.ResourceKind), string(claimed.Operation), string(domain.StatusStale))
			return domain.ResourceChangeProposal{}, domain.ErrProposalStale
		}
	}
	applier := s.appliers[claimed.ResourceKind]
	if applier == nil {
		return domain.ResourceChangeProposal{}, s.finishApplyFailure(ctx, claimed, actorID, domain.ErrProposalApplyFailed)
	}
	decoded, err := domain.DecodeProposalPayload(claimed.ResourceKind, claimed.Operation, claimed.Payload)
	if err != nil {
		return domain.ResourceChangeProposal{}, s.finishApplyFailure(ctx, claimed, actorID, err)
	}
	result, applyErr := applier.ApplyResourceChange(ctx, domain.ProposalEnvelope{Proposal: claimed, Payload: decoded})
	if applyErr != nil {
		var classified *port.ResourceApplyError
		if errors.As(applyErr, &classified) && classified.Outcome == port.ResourceApplyUnknownOutcome {
			if finishErr := s.finish(ctx, claimed, domain.StatusUnknownOutcome, domain.ApplyResult{}, "proposal_unknown_outcome", actorID); finishErr != nil {
				return domain.ResourceChangeProposal{}, errors.Join(domain.ErrProposalUnknownOutcome, finishErr)
			}
			s.metrics.IncResourceProposal(string(claimed.ResourceKind), string(claimed.Operation), string(domain.StatusUnknownOutcome))
			return domain.ResourceChangeProposal{}, domain.ErrProposalUnknownOutcome
		}
		return domain.ResourceChangeProposal{}, s.finishApplyFailure(ctx, claimed, actorID, applyErr)
	}
	if err := s.finish(ctx, claimed, domain.StatusApplied, result, "", actorID); err != nil {
		return domain.ResourceChangeProposal{}, err
	}
	claimed.Status = domain.StatusApplied
	claimed.ApplyResult = result
	s.metrics.IncResourceProposal(string(claimed.ResourceKind), string(claimed.Operation), string(domain.StatusApplied))
	s.metrics.RecordResourceProposalReviewDuration(string(claimed.ResourceKind), string(claimed.Operation), now.Sub(proposal.CreatedAt).Seconds())
	return claimed, nil
}

func (s *ResourceChangeProposalService) persistInvalid(
	ctx context.Context,
	proposal domain.ResourceChangeProposal,
	summary string,
) (domain.ResourceChangeProposal, error) {
	proposal.Status = domain.StatusInvalid
	proposal.Payload = json.RawMessage(`{}`)
	if err := s.repo.Create(ctx, proposal, domain.ProposalEvent{
		ActorID: proposal.ProposerID, ToStatus: domain.StatusInvalid, Code: "proposal_invalid",
		Summary: summary, CreatedAt: proposal.CreatedAt,
	}); err != nil {
		return domain.ResourceChangeProposal{}, fmt.Errorf("persist invalid proposal: %w", err)
	}
	return proposal, domain.ErrProposalInvalid
}

func (s *ResourceChangeProposalService) finishApplyFailure(
	ctx context.Context,
	proposal domain.ResourceChangeProposal,
	actorID string,
	_ error,
) error {
	if err := s.finish(ctx, proposal, domain.StatusFailed, domain.ApplyResult{}, "proposal_apply_failed", actorID); err != nil {
		return errors.Join(domain.ErrProposalApplyFailed, err)
	}
	s.metrics.IncResourceProposal(string(proposal.ResourceKind), string(proposal.Operation), string(domain.StatusFailed))
	return domain.ErrProposalApplyFailed
}

func (s *ResourceChangeProposalService) finish(
	ctx context.Context,
	proposal domain.ResourceChangeProposal,
	status domain.ProposalStatus,
	result domain.ApplyResult,
	code, actorID string,
) error {
	return s.repo.Finish(ctx, proposal.ID, status, result, domain.ProposalEvent{
		ActorID: actorID, FromStatus: domain.StatusApplying, ToStatus: status, Code: code, CreatedAt: s.now(),
	})
}

func (s *ResourceChangeProposalService) authorize(
	ctx context.Context,
	tenantID, actorID string,
	kind domain.ResourceKind,
	operation domain.ProposalOperation,
) error {
	if s.authorizer == nil || tenantID == "" || actorID == "" {
		return domain.ErrProposalForbidden
	}
	if err := s.authorizer.AuthorizeProposal(ctx, tenantID, actorID, kind, operation); err != nil {
		return domain.ErrProposalForbidden
	}
	return nil
}

func validateProposalPayload(payload any) error {
	switch value := payload.(type) {
	case *domain.AgentChange:
		if strings.TrimSpace(value.Name) == "" || value.MaxIterations < 1 || value.MaxIterations > 20 || value.MaxContextTokens < 1 {
			return domain.ErrProposalInvalid
		}
	case *domain.SkillDraftChange:
		if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Instructions) == "" {
			return domain.ErrProposalInvalid
		}
	case *domain.MCPConfigChange:
		if !validMCPConfigChange(value) {
			return domain.ErrProposalInvalid
		}
	case *domain.KnowledgeWorkspaceChange:
		if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.Description) == "" {
			return domain.ErrProposalInvalid
		}
	default:
		return domain.ErrProposalInvalid
	}
	return nil
}

func validMCPConfigChange(value *domain.MCPConfigChange) bool {
	if strings.TrimSpace(value.Name) == "" ||
		value.TimeoutSec < minProposalMCPTimeoutSec || value.TimeoutSec > maxProposalMCPTimeoutSec {
		return false
	}
	switch value.Transport {
	case "stdio":
		// stdio 已全链禁用（mcp doConnect 唯一权威拒绝）：proposal 一律拒绝，
		// 避免申请一批准即被服务端 400 拒绝（承诺能力与实现不一致）。
		return false
	case "streamable-http":
		if !validProposalMCPURL(value.URL) || strings.TrimSpace(value.Command) != "" || len(value.Args) > 0 {
			return false
		}
	default:
		return false
	}
	if value.Retry == nil {
		return true
	}
	retry := value.Retry
	return retry.MaxRetries >= minProposalMCPRetryCount && retry.MaxRetries <= maxProposalMCPRetryCount &&
		retry.InitialDelayMs >= minProposalMCPRetryInitialDelayMs &&
		retry.InitialDelayMs <= maxProposalMCPRetryInitialDelayMs &&
		retry.MaxDelayMs >= minProposalMCPRetryMaxDelayMs && retry.MaxDelayMs <= maxProposalMCPRetryMaxDelayMs &&
		retry.MaxDelayMs >= retry.InitialDelayMs &&
		retry.BackoffFactor >= minProposalMCPRetryBackoffFactor &&
		retry.BackoffFactor <= maxProposalMCPRetryBackoffFactor
}

func validProposalMCPURL(raw string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return false
	}
	for key := range parsed.Query() {
		if proposalSecretLikeKey(key) {
			return false
		}
	}
	return true
}

func proposalSecretLikeKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	for _, marker := range []string{"token", "apikey", "authorization", "password", "secret", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
