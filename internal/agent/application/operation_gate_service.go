package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
)

// operationFingerprintPrefix version-marks canonical payload digests. The full
// fingerprint is sha256(agentID | opType | canonicalJSON(payload)); the digest
// itself carries the prefix so the hash cannot be confused with other digests.
const operationFingerprintPrefix = "operation-gate:v1:sha256:"

// OperationGateService implements port.OperationGate. It short-circuits on a
// single-use approved replay, then decides whether the operation may run
// policy-allowed or must wait on a human approval proposal. All fingerprints
// are computed server-side; clients never supply them.
type OperationGateService struct {
	repo      port.OperationProposalRepo
	usageRepo port.OperationUsageRepo
	metrics   observability.MetricsProvider
	now       func() time.Time
	newID     func() string
}

// NewOperationGateService wires the gate with its persistence and metrics.
func NewOperationGateService(
	repo port.OperationProposalRepo,
	usageRepo port.OperationUsageRepo,
	metrics observability.MetricsProvider,
) *OperationGateService {
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	return &OperationGateService{
		repo: repo, usageRepo: usageRepo, metrics: metrics,
		now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString,
	}
}

// ComputeFingerprint derives the server-side content hash binding an operation
// to its agent, type, and canonical payload. Payload changes → fingerprint
// changes, so an approval can never be replayed against different content.
func (s *OperationGateService) ComputeFingerprint(agentID string, opType port.OperationType, payload any) (string, error) {
	if agentID == "" || opType == "" {
		return "", domain.ErrProposalInvalid
	}
	digest, err := canonicalJSONDigest(operationFingerprintPrefix, payload)
	if err != nil {
		return "", err
	}
	return agentID + "|" + string(opType) + "|" + digest, nil
}

// CheckWithProposal runs the full gate decision table. payload is the typed
// operation input; when approval is required the gate persists it (de-sensitised)
// as the proposal's reviewable summary and returns the new proposal ID.
func (s *OperationGateService) CheckWithProposal(ctx context.Context, req port.OperationRequest, payload any) (port.GateDecision, error) {
	return s.check(ctx, req, payload)
}

// Check is the thin port implementation for callers that pre-computed the
// fingerprint and have no payload to review (e.g. replay-only flows). Branches
// that would create a proposal are denied without one.
func (s *OperationGateService) Check(ctx context.Context, req port.OperationRequest) (bool, string) {
	decision, err := s.check(ctx, req, nil)
	if err != nil {
		return false, GateReasonGateError
	}
	return decision.Allowed, decision.Reason
}

// RecordUsage increments the daily usage counters after a gated operation
// actually ran. The caller owns rollback semantics: usage is best-effort
// accounting and must not fail the mutation itself.
func (s *OperationGateService) RecordUsage(ctx context.Context, tenantID, agentID string, opType port.OperationType, costUSD float64) error {
	if err := s.usageRepo.AddUsage(ctx, tenantID, agentID, opType, s.now().UTC(), costUSD, 1); err != nil {
		return fmt.Errorf("record operation usage: %w", err)
	}
	s.metrics.IncOperationProposal(string(opType), "usage_recorded")
	return nil
}

// check is the decision table. Short-circuit order matters: a consumable
// approval bypasses every later gate, duplicate and delegation checks run
// before any proposal is created, and only approval-requiring branches query
// the budget (a proposal is created either way once one is required).
func (s *OperationGateService) check(ctx context.Context, req port.OperationRequest, payload any) (port.GateDecision, error) {
	if reason := validateOperationRequest(req); reason != "" {
		return port.GateDecision{Reason: reason}, nil
	}
	// 1-5. Short-circuit gates: replay consumption, duplicate pending,
	// delegation declaration, and operation types that always need approval.
	if decision, done, err := s.preApprovalDecision(ctx, req, payload); done || err != nil {
		return decision, err
	}
	// 6. Budget caps (per-dimension opt-in; zero disables that dimension).
	exceeded, err := s.budgetExceeded(ctx, req)
	if err != nil {
		return port.GateDecision{}, err
	}
	if exceeded {
		return s.propose(ctx, req, payload, GateReasonBudgetExceeded)
	}
	// 7. No gate requires approval.
	return port.GateDecision{Allowed: true, Reason: GateReasonPolicyAllowed}, nil
}

// preApprovalDecision runs the gates that short-circuit before the budget
// check. done=true means the decision is final (allowed replay, or a rejected
// or proposed outcome); err carries an unexpected persistence failure.
func (s *OperationGateService) preApprovalDecision(ctx context.Context, req port.OperationRequest, payload any) (port.GateDecision, bool, error) {
	// 1. Single-use replay consumption (actor-bound, TTL-enforced by the repo).
	consumed, err := s.repo.ConsumeApproved(ctx, req.TenantID, req.Fingerprint, req.ProposerID)
	if err != nil {
		return port.GateDecision{}, false, fmt.Errorf("consume approved operation: %w", err)
	}
	if consumed {
		return port.GateDecision{Allowed: true, Reason: GateReasonApprovedReplay}, true, nil
	}
	// 2. Duplicate pending proposal for the same fingerprint.
	pending, err := s.repo.HasPending(ctx, req.TenantID, req.Fingerprint)
	if err != nil {
		return port.GateDecision{}, false, fmt.Errorf("check pending operation proposal: %w", err)
	}
	if pending {
		return port.GateDecision{Reason: GateReasonDuplicatePending}, true, nil
	}
	// 3. Delegation must declare its data-sharing policy.
	if req.OpType == port.OpCrossAgentDelegate && req.Delegation == port.DelegationNone {
		return port.GateDecision{Reason: GateReasonDelegationRequired}, true, nil
	}
	// 4-5. Delegation and self-modify always require human approval.
	if reason, ok := approvalRequiringReason(req.OpType); ok {
		decision, err := s.propose(ctx, req, payload, reason)
		return decision, true, err
	}
	return port.GateDecision{}, false, nil
}

// approvalRequiringReason maps operation types that never bypass the approval
// gate to the gate reason they carry.
func approvalRequiringReason(opType port.OperationType) (string, bool) {
	switch opType {
	case port.OpCrossAgentDelegate:
		return GateReasonDelegationRequiresApproval, true
	case port.OpSelfModify:
		return GateReasonPendingApproval, true
	default:
		return "", false
	}
}

// propose persists an approval request with the server-computed fingerprint
// and a de-sensitised payload summary, so an approval is always bound to the
// reviewed content. A concurrent duplicate insert collapses to duplicate_pending.
func (s *OperationGateService) propose(ctx context.Context, req port.OperationRequest, payload any, reason string) (port.GateDecision, error) {
	if payload == nil {
		return port.GateDecision{Reason: reason}, nil
	}
	summary, err := buildOperationPayloadSummary(payload)
	if err != nil {
		return port.GateDecision{}, err
	}
	now := s.now().UTC()
	proposal := domain.OperationProposal{
		ID: s.newID(), TenantID: req.TenantID, AgentID: req.AgentID, TargetAgentID: req.TargetAgentID,
		OpType: string(req.OpType), Delegation: string(req.Delegation),
		MaxDailyCostUSD: req.Budget.MaxDailyCostUSD, MaxDailyExecutions: req.Budget.MaxDailyExecutions,
		Fingerprint: req.Fingerprint, PayloadSummary: summary, Status: domain.OpProposed,
		ProposerID: req.ProposerID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.Insert(ctx, proposal); err != nil {
		if errors.Is(err, domain.ErrOperationProposalPending) {
			return port.GateDecision{Reason: GateReasonDuplicatePending}, nil
		}
		return port.GateDecision{}, fmt.Errorf("insert operation proposal: %w", err)
	}
	s.metrics.IncOperationProposal(string(req.OpType), "proposed")
	return port.GateDecision{Reason: reason, ProposalID: proposal.ID}, nil
}

// budgetExceeded reports whether either non-zero daily cap is already reached.
// MaxDailyCostUSD=0 skips the cost dimension; MaxDailyExecutions=0 skips the
// execution count (see OperationBudgetZeroLimit).
func (s *OperationGateService) budgetExceeded(ctx context.Context, req port.OperationRequest) (bool, error) {
	if req.Budget.MaxDailyCostUSD <= 0 && req.Budget.MaxDailyExecutions <= 0 {
		return false, nil
	}
	usage, err := s.usageRepo.DailyUsage(ctx, req.TenantID, req.AgentID, req.OpType, s.now().UTC())
	if err != nil {
		return false, fmt.Errorf("query daily operation usage: %w", err)
	}
	if req.Budget.MaxDailyExecutions > 0 && usage.Executions >= req.Budget.MaxDailyExecutions {
		return true, nil
	}
	return req.Budget.MaxDailyCostUSD > 0 && usage.CostUSD >= req.Budget.MaxDailyCostUSD, nil
}

// validateOperationRequest returns the rejection reason for an invalid
// request, or "" when the request passes gate validation.
func validateOperationRequest(req port.OperationRequest) string {
	if req.TenantID == "" || req.AgentID == "" || req.ProposerID == "" || req.Fingerprint == "" {
		return GateReasonInvalidRequest
	}
	switch req.OpType {
	case port.OpRevisionApply, port.OpCrossAgentDelegate, port.OpScheduleCreate, port.OpSelfModify:
		return ""
	default:
		return GateReasonInvalidRequest
	}
}

// buildOperationPayloadSummary serialises the typed operation input as a
// reviewable diff surface, recursively masking secret-like fields so
// credentials never reach the approval screen (aligns with the
// resource_change_proposals summary rules).
func buildOperationPayloadSummary(payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal operation payload summary: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("unmarshal operation payload summary: %w", err)
	}
	redactOperationSecrets(value)
	out, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal redacted operation payload summary: %w", err)
	}
	return json.RawMessage(out), nil
}

// redactOperationSecrets walks a decoded JSON tree and replaces any value
// whose key looks secret (token, key, password, secret, credential) with "***".
func redactOperationSecrets(value any) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if proposalSecretLikeKey(key) {
				node[key] = "***"
				continue
			}
			redactOperationSecrets(child)
		}
	case []any:
		for _, child := range node {
			redactOperationSecrets(child)
		}
	}
}
