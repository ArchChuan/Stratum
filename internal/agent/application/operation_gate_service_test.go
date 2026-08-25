package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
)

var gateFixedNow = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// testSelfModifyPayload mirrors the gated self-modify request shape; api_key
// must be masked out of the reviewable payload summary.
type testSelfModifyPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	APIKey      string `json:"api_key"`
}

func gateRequest(tenantID, agentID string) port.OperationRequest {
	return port.OperationRequest{
		TenantID: tenantID, AgentID: agentID, ProposerID: "member-1",
		OpType: port.OpSelfModify, Delegation: port.DelegationNone,
		Fingerprint: "fp-test",
	}
}

func newGateServiceForTest(
	repo *operationProposalRepoFake,
	usage *operationUsageRepoFake,
	metrics *gateMetricsFake,
) *OperationGateService {
	var provider observability.MetricsProvider
	if metrics != nil {
		provider = metrics
	}
	service := NewOperationGateService(repo, usage, provider)
	service.now = func() time.Time { return gateFixedNow }
	var counter int
	service.newID = func() string {
		counter++
		return fmt.Sprintf("proposal-%d", counter)
	}
	return service
}

func TestOperationGateReplayConsumption(t *testing.T) {
	t.Run("approved replay is allowed once then reproposed", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
		expires := gateFixedNow.Add(10 * time.Hour)
		repo.proposals["p1"] = domain.OperationProposal{
			ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
			Fingerprint: "fp-test", Status: domain.OpApproved, ProposerID: "member-1",
			ExpiresAt: &expires,
		}
		req := gateRequest("tenant-1", "agent-1")

		decision, err := service.CheckWithProposal(context.Background(), req, testSelfModifyPayload{Name: "a"})
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		require.Equal(t, GateReasonApprovedReplay, decision.Reason)
		require.Equal(t, domain.OpExecuted, repo.proposals["p1"].Status)

		decision, err = service.CheckWithProposal(context.Background(), req, testSelfModifyPayload{Name: "a"})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, GateReasonPendingApproval, decision.Reason)
	})

	t.Run("expired approval does not consume", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
		expired := gateFixedNow.Add(-time.Hour)
		repo.proposals["p1"] = domain.OperationProposal{
			ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
			Fingerprint: "fp-test", Status: domain.OpApproved, ProposerID: "member-1",
			ExpiresAt: &expired,
		}
		decision, err := service.CheckWithProposal(context.Background(), gateRequest("tenant-1", "agent-1"), testSelfModifyPayload{})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, GateReasonPendingApproval, decision.Reason)
		require.Equal(t, domain.OpApproved, repo.proposals["p1"].Status)
	})

	t.Run("actor mismatch does not consume", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
		expires := gateFixedNow.Add(10 * time.Hour)
		repo.proposals["p1"] = domain.OperationProposal{
			ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
			Fingerprint: "fp-test", Status: domain.OpApproved, ProposerID: "member-1",
			ExpiresAt: &expires,
		}
		req := gateRequest("tenant-1", "agent-1")
		req.ProposerID = "member-2"
		decision, err := service.CheckWithProposal(context.Background(), req, testSelfModifyPayload{})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, domain.OpApproved, repo.proposals["p1"].Status)
	})

	t.Run("pending approval is not consumable", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
		repo.proposals["p1"] = domain.OperationProposal{
			ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
			Fingerprint: "fp-test", Status: domain.OpProposed, ProposerID: "member-1",
		}
		decision, err := service.CheckWithProposal(context.Background(), gateRequest("tenant-1", "agent-1"), testSelfModifyPayload{})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, GateReasonDuplicatePending, decision.Reason)
	})
}

func TestOperationGateDuplicatePending(t *testing.T) {
	repo := newOperationProposalRepoFake()
	service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
	repo.proposals["p1"] = domain.OperationProposal{
		ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
		Fingerprint: "fp-test", Status: domain.OpReviewing, ProposerID: "member-1",
	}
	decision, err := service.CheckWithProposal(context.Background(), gateRequest("tenant-1", "agent-1"), testSelfModifyPayload{Name: "x"})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, GateReasonDuplicatePending, decision.Reason)
	require.Empty(t, decision.ProposalID)
	require.Len(t, repo.proposals, 1)
}

func TestOperationGateDelegationPolicy(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		delegation  port.DelegationPolicy
		wantAllowed bool
		wantReason  string
	}{
		{name: "no delegation policy is rejected without a proposal", delegation: port.DelegationNone, wantReason: GateReasonDelegationRequired},
		{name: "read-only delegation requires approval", delegation: port.DelegationReadOnly, wantReason: GateReasonDelegationRequiresApproval},
		{name: "full delegation requires approval", delegation: port.DelegationFull, wantReason: GateReasonDelegationRequiresApproval},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOperationProposalRepoFake()
			service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
			req := port.OperationRequest{
				TenantID: "tenant-1", AgentID: "agent-1", TargetAgentID: "agent-2", ProposerID: "member-1",
				OpType: port.OpCrossAgentDelegate, Delegation: tc.delegation, Fingerprint: "fp-delegate",
			}
			decision, err := service.CheckWithProposal(ctx, req, testSelfModifyPayload{Name: "x"})
			require.NoError(t, err)
			require.Equal(t, tc.wantAllowed, decision.Allowed)
			require.Equal(t, tc.wantReason, decision.Reason)
			if tc.delegation == port.DelegationNone {
				require.Len(t, repo.proposals, 0)
			} else {
				require.Len(t, repo.proposals, 1)
				proposal := repo.proposals[decision.ProposalID]
				require.Equal(t, "agent-2", proposal.TargetAgentID)
				require.Equal(t, string(tc.delegation), proposal.Delegation)
			}
		})
	}
}

func TestOperationGateSelfModifyAlwaysProposes(t *testing.T) {
	repo := newOperationProposalRepoFake()
	metrics := &gateMetricsFake{}
	service := newGateServiceForTest(repo, newOperationUsageRepoFake(), metrics)
	req := gateRequest("tenant-1", "agent-1")
	fingerprint, err := service.ComputeFingerprint(req.AgentID, req.OpType, testSelfModifyPayload{Name: "renamed", APIKey: "secret-value"})
	require.NoError(t, err)
	req.Fingerprint = fingerprint

	decision, err := service.CheckWithProposal(context.Background(), req, testSelfModifyPayload{Name: "renamed", APIKey: "secret-value"})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, GateReasonPendingApproval, decision.Reason)
	require.NotEmpty(t, decision.ProposalID)

	proposal := repo.proposals[decision.ProposalID]
	require.Equal(t, "tenant-1", proposal.TenantID)
	require.Equal(t, "agent-1", proposal.AgentID)
	require.Equal(t, "member-1", proposal.ProposerID)
	require.Equal(t, domain.OpProposed, proposal.Status)
	require.Equal(t, fingerprint, proposal.Fingerprint)
	require.Contains(t, string(proposal.PayloadSummary), `"name":"renamed"`)
	require.NotContains(t, string(proposal.PayloadSummary), "secret-value")
	require.Contains(t, string(proposal.PayloadSummary), "***")
	require.Equal(t, []string{"self_modify|proposed"}, metrics.calls)
}

func TestOperationGateBudget(t *testing.T) {
	ctx := context.Background()
	revisionReq := func() port.OperationRequest {
		return port.OperationRequest{
			TenantID: "tenant-1", AgentID: "agent-1", ProposerID: "member-1",
			OpType: port.OpRevisionApply, Delegation: port.DelegationNone, Fingerprint: "fp-revision",
		}
	}

	t.Run("zero budget skips usage lookup entirely", func(t *testing.T) {
		usage := &operationUsageRepoFake{err: errors.New("must not be queried")}
		service := newGateServiceForTest(newOperationProposalRepoFake(), usage, nil)
		decision, err := service.CheckWithProposal(ctx, revisionReq(), testSelfModifyPayload{})
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		require.Equal(t, GateReasonPolicyAllowed, decision.Reason)
	})

	t.Run("execution cap reached creates a proposal", func(t *testing.T) {
		usage := newOperationUsageRepoFake()
		usage.usage[usageKey("tenant-1", "agent-1", port.OpRevisionApply, gateFixedNow)] = port.DailyOperationUsage{Executions: 5}
		repo := newOperationProposalRepoFake()
		service := newGateServiceForTest(repo, usage, nil)
		req := revisionReq()
		req.Budget.MaxDailyExecutions = 5
		decision, err := service.CheckWithProposal(ctx, req, testSelfModifyPayload{})
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, GateReasonBudgetExceeded, decision.Reason)
		require.NotEmpty(t, decision.ProposalID)
		proposal := repo.proposals[decision.ProposalID]
		require.Equal(t, 5, proposal.MaxDailyExecutions)
	})

	t.Run("cost cap reached creates a proposal", func(t *testing.T) {
		usage := newOperationUsageRepoFake()
		usage.usage[usageKey("tenant-1", "agent-1", port.OpRevisionApply, gateFixedNow)] = port.DailyOperationUsage{CostUSD: 100}
		service := newGateServiceForTest(newOperationProposalRepoFake(), usage, nil)
		req := revisionReq()
		req.Budget.MaxDailyCostUSD = 100
		decision, err := service.CheckWithProposal(ctx, req, testSelfModifyPayload{})
		require.NoError(t, err)
		require.Equal(t, GateReasonBudgetExceeded, decision.Reason)
	})

	t.Run("below caps is policy allowed", func(t *testing.T) {
		usage := newOperationUsageRepoFake()
		usage.usage[usageKey("tenant-1", "agent-1", port.OpRevisionApply, gateFixedNow)] =
			port.DailyOperationUsage{CostUSD: 90, Executions: 4}
		service := newGateServiceForTest(newOperationProposalRepoFake(), usage, nil)
		req := revisionReq()
		req.Budget = port.OperationBudget{MaxDailyCostUSD: 100, MaxDailyExecutions: 5}
		decision, err := service.CheckWithProposal(ctx, req, testSelfModifyPayload{})
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		require.Equal(t, GateReasonPolicyAllowed, decision.Reason)
	})

	t.Run("cost dimension skipped when zero", func(t *testing.T) {
		usage := newOperationUsageRepoFake()
		usage.usage[usageKey("tenant-1", "agent-1", port.OpRevisionApply, gateFixedNow)] = port.DailyOperationUsage{CostUSD: 1000}
		service := newGateServiceForTest(newOperationProposalRepoFake(), usage, nil)
		req := revisionReq()
		req.Budget.MaxDailyExecutions = 5
		decision, err := service.CheckWithProposal(ctx, req, testSelfModifyPayload{})
		require.NoError(t, err)
		require.True(t, decision.Allowed)
	})
}

func TestOperationGateInvalidRequest(t *testing.T) {
	repo := newOperationProposalRepoFake()
	service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
	tests := []struct {
		name string
		mut  func(*port.OperationRequest)
	}{
		{name: "empty tenant", mut: func(r *port.OperationRequest) { r.TenantID = "" }},
		{name: "empty agent", mut: func(r *port.OperationRequest) { r.AgentID = "" }},
		{name: "empty proposer", mut: func(r *port.OperationRequest) { r.ProposerID = "" }},
		{name: "empty fingerprint", mut: func(r *port.OperationRequest) { r.Fingerprint = "" }},
		{name: "unknown op type", mut: func(r *port.OperationRequest) { r.OpType = port.OperationType("delete_everything") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := gateRequest("tenant-1", "agent-1")
			tc.mut(&req)
			decision, err := service.CheckWithProposal(context.Background(), req, testSelfModifyPayload{})
			require.NoError(t, err)
			require.False(t, decision.Allowed)
			require.Equal(t, GateReasonInvalidRequest, decision.Reason)
			require.Len(t, repo.proposals, 0)
		})
	}
}

func TestOperationGateInsertConflictCollapsesToDuplicate(t *testing.T) {
	repo := newOperationProposalRepoFake()
	repo.insertErr = domain.ErrOperationProposalPending
	service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
	decision, err := service.CheckWithProposal(context.Background(), gateRequest("tenant-1", "agent-1"), testSelfModifyPayload{})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Equal(t, GateReasonDuplicatePending, decision.Reason)
	require.Empty(t, decision.ProposalID)
}

func TestOperationGateComputeFingerprint(t *testing.T) {
	service := newGateServiceForTest(newOperationProposalRepoFake(), newOperationUsageRepoFake(), nil)
	payload := testSelfModifyPayload{Name: "renamed", Description: "d"}
	first, err := service.ComputeFingerprint("agent-1", port.OpSelfModify, payload)
	require.NoError(t, err)

	t.Run("deterministic for identical input", func(t *testing.T) {
		again, err := service.ComputeFingerprint("agent-1", port.OpSelfModify, payload)
		require.NoError(t, err)
		require.Equal(t, first, again)
	})
	t.Run("payload change changes fingerprint", func(t *testing.T) {
		changed, err := service.ComputeFingerprint("agent-1", port.OpSelfModify, testSelfModifyPayload{Name: "other"})
		require.NoError(t, err)
		require.NotEqual(t, first, changed)
	})
	t.Run("agent change changes fingerprint", func(t *testing.T) {
		changed, err := service.ComputeFingerprint("agent-2", port.OpSelfModify, payload)
		require.NoError(t, err)
		require.NotEqual(t, first, changed)
	})
	t.Run("op type change changes fingerprint", func(t *testing.T) {
		changed, err := service.ComputeFingerprint("agent-1", port.OpRevisionApply, payload)
		require.NoError(t, err)
		require.NotEqual(t, first, changed)
	})
	t.Run("rejects empty agent", func(t *testing.T) {
		_, err := service.ComputeFingerprint("", port.OpSelfModify, payload)
		require.ErrorIs(t, err, domain.ErrProposalInvalid)
	})
}

func TestOperationGateRecordUsage(t *testing.T) {
	usage := newOperationUsageRepoFake()
	metrics := &gateMetricsFake{}
	service := newGateServiceForTest(newOperationProposalRepoFake(), usage, metrics)
	ctx := context.Background()

	require.NoError(t, service.RecordUsage(ctx, "tenant-1", "agent-1", port.OpSelfModify, 1.5))
	require.NoError(t, service.RecordUsage(ctx, "tenant-1", "agent-1", port.OpSelfModify, 0.5))
	got := usage.usage[usageKey("tenant-1", "agent-1", port.OpSelfModify, gateFixedNow)]
	require.Equal(t, 2.0, got.CostUSD)
	require.Equal(t, 2, got.Executions)
	require.Equal(t, []string{"self_modify|usage_recorded", "self_modify|usage_recorded"}, metrics.calls)

	usage.err = errors.New("usage db down")
	require.Error(t, service.RecordUsage(ctx, "tenant-1", "agent-1", port.OpSelfModify, 1))
}

func TestOperationGateCheckThinWrapperWithoutPayload(t *testing.T) {
	service := newGateServiceForTest(newOperationProposalRepoFake(), newOperationUsageRepoFake(), nil)
	allowed, reason := service.Check(context.Background(), gateRequest("tenant-1", "agent-1"))
	require.False(t, allowed)
	require.Equal(t, GateReasonPendingApproval, reason)

	req := gateRequest("tenant-1", "agent-1")
	req.OpType = port.OpRevisionApply
	allowed, reason = service.Check(context.Background(), req)
	require.True(t, allowed)
	require.Equal(t, GateReasonPolicyAllowed, reason)
}

func TestOperationGatePropagatesRepoFailureFailClosed(t *testing.T) {
	repo := newOperationProposalRepoFake()
	repo.consumeErr = errors.New("consume db down")
	service := newGateServiceForTest(repo, newOperationUsageRepoFake(), nil)
	allowed, reason := service.Check(context.Background(), gateRequest("tenant-1", "agent-1"))
	require.False(t, allowed)
	require.Equal(t, GateReasonGateError, reason)
}

// --- fakes ---

type gateMetricsFake struct {
	observability.NoopMetrics
	calls []string
}

func (m *gateMetricsFake) IncOperationProposal(kind, outcome string) {
	m.calls = append(m.calls, kind+"|"+outcome)
}

type operationProposalRepoFake struct {
	mu         sync.Mutex
	proposals  map[string]domain.OperationProposal
	insertErr  error
	consumeErr error
	lastTenant string
	now        func() time.Time
}

func newOperationProposalRepoFake() *operationProposalRepoFake {
	return &operationProposalRepoFake{
		proposals: map[string]domain.OperationProposal{},
		now:       func() time.Time { return gateFixedNow },
	}
}

func (f *operationProposalRepoFake) Insert(_ context.Context, p domain.OperationProposal) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastTenant = p.TenantID
	f.proposals[p.ID] = p
	return nil
}

func (f *operationProposalRepoFake) GetByID(_ context.Context, _, id string) (*domain.OperationProposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proposals[id]
	if !ok {
		return nil, domain.ErrOperationProposalNotFound
	}
	return &p, nil
}

func (f *operationProposalRepoFake) ListPending(_ context.Context, tenantID string) ([]domain.OperationProposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.OperationProposal
	for _, p := range f.proposals {
		if p.Status == domain.OpProposed || p.Status == domain.OpReviewing {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *operationProposalRepoFake) UpdateStatus(_ context.Context, _, id string, status domain.OpProposalStatus, reviewerID, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proposals[id]
	if !ok {
		return domain.ErrOperationProposalNotFound
	}
	if p.Status != domain.OpProposed && p.Status != domain.OpReviewing {
		return domain.ErrOperationProposalResolved
	}
	now := f.now()
	p.Status = status
	p.ReviewedBy = reviewerID
	p.ReviewNote = note
	p.UpdatedAt = now
	switch status {
	case domain.OpApproved:
		expires := now.Add(constants.OperationApprovalTTL)
		p.ExpiresAt = &expires
	case domain.OpRejected:
		p.ResolvedAt = &now
	}
	f.proposals[id] = p
	return nil
}

func (f *operationProposalRepoFake) HasPending(_ context.Context, _, fingerprint string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for _, p := range f.proposals {
		if p.Fingerprint != fingerprint {
			continue
		}
		switch p.Status {
		case domain.OpProposed, domain.OpReviewing:
			return true, nil
		case domain.OpApproved:
			// An expired approval is not pending: it can never be consumed,
			// so it must not block a fresh proposal for the same content
			// (mirrors the repo's HasPending SQL expiry predicate).
			if p.ExpiresAt != nil && p.ExpiresAt.After(now) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (f *operationProposalRepoFake) ConsumeApproved(_ context.Context, _, fingerprint, proposerID string) (bool, error) {
	if f.consumeErr != nil {
		return false, f.consumeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, p := range f.proposals {
		if p.Fingerprint == fingerprint && p.ProposerID == proposerID && p.Status == domain.OpApproved {
			if p.ExpiresAt == nil || p.ExpiresAt.Before(f.now()) {
				continue
			}
			now := f.now()
			p.Status = domain.OpExecuted
			p.ResolvedAt = &now
			p.UpdatedAt = now
			f.proposals[id] = p
			return true, nil
		}
	}
	return false, nil
}

func (f *operationProposalRepoFake) ListByProposer(_ context.Context, _, proposerID string) ([]domain.OperationProposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.OperationProposal, 0, len(f.proposals))
	for _, p := range f.proposals {
		if p.ProposerID == proposerID {
			out = append(out, p)
		}
	}
	return out, nil
}

func usageKey(tenantID, agentID string, opType port.OperationType, day time.Time) string {
	return tenantID + "|" + agentID + "|" + string(opType) + "|" + day.Format("2006-01-02")
}

type operationUsageRepoFake struct {
	mu    sync.Mutex
	usage map[string]port.DailyOperationUsage
	err   error
}

func newOperationUsageRepoFake() *operationUsageRepoFake {
	return &operationUsageRepoFake{usage: map[string]port.DailyOperationUsage{}}
}

func (f *operationUsageRepoFake) AddUsage(_ context.Context, tenantID, agentID string, opType port.OperationType, day time.Time, costUSD float64, executions int) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := usageKey(tenantID, agentID, opType, day)
	current := f.usage[key]
	current.CostUSD += costUSD
	current.Executions += executions
	f.usage[key] = current
	return nil
}

func (f *operationUsageRepoFake) DailyUsage(_ context.Context, tenantID, agentID string, opType port.OperationType, day time.Time) (port.DailyOperationUsage, error) {
	if f.err != nil {
		return port.DailyOperationUsage{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage[usageKey(tenantID, agentID, opType, day)], nil
}
