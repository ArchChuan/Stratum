package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
)

func TestResourceChangeProposalCreateAuthorizationAndInvalidPayload(t *testing.T) {
	repo := newProposalRepoFake()
	authorizer := &proposalAuthorizerFake{err: domain.ErrProposalForbidden}
	service := newProposalServiceForTest(repo, authorizer, &baselineFake{}, nil)
	_, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "member", Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: json.RawMessage(`{"name":"agent"}`),
	})
	require.ErrorIs(t, err, domain.ErrProposalForbidden)
	require.Empty(t, repo.proposals)

	authorizer.err = nil
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceMCPConfig,
		Operation: domain.OperationCreate,
		Payload:   json.RawMessage(`{"name":"mcp","transport":"streamable_http","headers":{"Authorization":"secret"}}`),
	})
	require.ErrorIs(t, err, domain.ErrProposalInvalid)
	require.Equal(t, domain.StatusInvalid, proposal.Status)
	require.JSONEq(t, `{}`, string(repo.proposals[proposal.ID].Payload))
	require.NotContains(t, string(repo.proposals[proposal.ID].Payload), "secret")
}

func TestResourceChangeProposalConfirmReauthorizesAndChecksBaseline(t *testing.T) {
	repo := newProposalRepoFake()
	authorizer := &proposalAuthorizerFake{}
	baseline := &baselineFake{value: "baseline-a"}
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "agent-1", Fingerprint: "baseline-b", Readback: json.RawMessage(`{"id":"agent-1"}`)}}
	service := newProposalServiceForTest(repo, authorizer, baseline, map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationUpdate,
		ResourceID: "agent-1", Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	require.Equal(t, "baseline-a", proposal.BaselineFingerprint)

	authorizer.failAt = 2
	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalForbidden)
	require.Zero(t, applier.calls)

	authorizer.failAt = 0
	authorizer.calls = 0
	proposal, err = service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationUpdate,
		ResourceID: "agent-1", Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	baseline.value = "changed"
	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalStale)
	require.Zero(t, applier.calls)
	require.Equal(t, domain.StatusStale, repo.proposals[proposal.ID].Status)
}

func TestResourceChangeProposalBaselineReadFailureFinishesApplyingProposal(t *testing.T) {
	repo := newProposalRepoFake()
	baseline := &baselineFake{value: "baseline-a"}
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, baseline, nil)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationUpdate,
		ResourceID: "agent-1", Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)

	baseline.err = errors.New("resource unavailable")
	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalApplyFailed)
	require.Equal(t, domain.StatusFailed, repo.proposals[proposal.ID].Status)
	require.Equal(t, "proposal_baseline_unavailable", repo.proposals[proposal.ID].ErrorCode)
}

func TestResourceChangeProposalApplyOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		applyErr   error
		wantStatus domain.ProposalStatus
		wantErr    error
	}{
		{"success", nil, domain.StatusApplied, nil},
		{"known failure", &port.ResourceApplyError{Outcome: port.ResourceApplyDefiniteFailure, Err: errors.New("failed")}, domain.StatusFailed, domain.ErrProposalApplyFailed},
		{"unknown outcome", &port.ResourceApplyError{Outcome: port.ResourceApplyUnknownOutcome, Err: errors.New("timeout")}, domain.StatusUnknownOutcome, domain.ErrProposalUnknownOutcome},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newProposalRepoFake()
			applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}, err: tc.applyErr}
			service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{}, map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
			proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
				TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
				Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
			})
			require.NoError(t, err)
			got, err := service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, "created", got.ApplyResult.ResourceID)
			}
			require.Equal(t, tc.wantStatus, repo.proposals[proposal.ID].Status)
			require.Equal(t, 1, applier.calls)
		})
	}
}

func TestResourceChangeProposalRetryResumesConfirmedWithoutReconfirming(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	stored := repo.proposals[proposal.ID]
	stored.Status = domain.StatusConfirmed
	repo.proposals[proposal.ID] = stored

	result, err := service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.NoError(t, err)
	require.Equal(t, domain.StatusApplied, result.Status)
	require.Equal(t, 1, applier.calls)
}

func TestResourceChangeProposalRetryMarksInterruptedApplyingAsUnknown(t *testing.T) {
	repo := newProposalRepoFake()
	applier := &proposalApplierFake{}
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	stored := repo.proposals[proposal.ID]
	stored.Status = domain.StatusApplying
	stored.UpdatedAt = stored.UpdatedAt.Add(-proposalApplyingRecoveryLease)
	repo.proposals[proposal.ID] = stored

	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalUnknownOutcome)
	require.Equal(t, domain.StatusUnknownOutcome, repo.proposals[proposal.ID].Status)
	require.Zero(t, applier.calls)
}

func TestResourceChangeProposalRetryAfterFinishFailureDoesNotRepeatApply(t *testing.T) {
	repo := newProposalRepoFake()
	repo.finishErr = errors.New("database unavailable")
	applier := &proposalApplierFake{result: domain.ApplyResult{ResourceID: "created"}}
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{},
		map[domain.ResourceKind]port.ResourceChangeApplier{domain.ResourceAgent: applier})
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)

	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, domain.StatusApplying, repo.proposals[proposal.ID].Status)
	require.Equal(t, 1, applier.calls)
	stored := repo.proposals[proposal.ID]
	stored.UpdatedAt = stored.UpdatedAt.Add(-proposalApplyingRecoveryLease)
	repo.proposals[proposal.ID] = stored

	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalUnknownOutcome)
	require.Equal(t, domain.StatusUnknownOutcome, repo.proposals[proposal.ID].Status)
	require.Equal(t, 1, applier.calls)
}

func TestResourceChangeProposalConcurrentConfirmDoesNotFinalizeActiveClaim(t *testing.T) {
	repo := newProposalRepoFake()
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{}, nil)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	stored := repo.proposals[proposal.ID]
	stored.Status = domain.StatusApplying
	repo.proposals[proposal.ID] = stored

	_, err = service.ConfirmAndApply(context.Background(), "tenant-1", proposal.ID, "admin")
	require.ErrorIs(t, err, domain.ErrProposalAlreadyClaimed)
	require.Equal(t, domain.StatusApplying, repo.proposals[proposal.ID].Status)
}

func TestResourceChangeProposalUpdateDraftRevalidatesPayload(t *testing.T) {
	repo := newProposalRepoFake()
	service := newProposalServiceForTest(repo, &proposalAuthorizerFake{}, &baselineFake{}, nil)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"before","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	updated, err := service.UpdateDraft(context.Background(), UpdateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", ProposalID: proposal.ID,
		Payload: json.RawMessage(`{"name":"after","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	require.Contains(t, string(updated.Payload), `"after"`)
	require.Equal(t, domain.StatusReadyForReview, repo.events[len(repo.events)-1].FromStatus)
	_, err = service.UpdateDraft(context.Background(), UpdateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", ProposalID: proposal.ID,
		Payload: json.RawMessage(`{"name":"after","secret":"do-not-store"}`),
	})
	require.ErrorIs(t, err, domain.ErrProposalInvalid)
	require.NotContains(t, string(repo.proposals[proposal.ID].Payload), "do-not-store")
}

func TestResourceChangeProposalCancelReturnsReauthorizationFailure(t *testing.T) {
	repo := newProposalRepoFake()
	authorizer := &proposalAuthorizerFake{failAt: 2}
	service := newProposalServiceForTest(repo, authorizer, &baselineFake{}, nil)
	proposal, err := service.CreateProposal(context.Background(), CreateProposalInput{
		TenantID: "tenant-1", ActorID: "admin", Kind: domain.ResourceAgent, Operation: domain.OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
	})
	require.NoError(t, err)
	authorizer.calls = 0

	err = service.Cancel(context.Background(), "tenant-1", "admin", proposal.ID)
	require.ErrorIs(t, err, domain.ErrProposalForbidden)
	require.Equal(t, domain.StatusReadyForReview, repo.proposals[proposal.ID].Status)
}

type proposalRepoFake struct {
	proposals map[string]domain.ResourceChangeProposal
	events    []domain.ProposalEvent
	finishErr error
}

func newProposalRepoFake() *proposalRepoFake {
	return &proposalRepoFake{proposals: map[string]domain.ResourceChangeProposal{}}
}
func (f *proposalRepoFake) Create(_ context.Context, p domain.ResourceChangeProposal, _ domain.ProposalEvent) error {
	f.proposals[p.ID] = p
	return nil
}
func (f *proposalRepoFake) Get(_ context.Context, id string) (domain.ResourceChangeProposal, error) {
	p, ok := f.proposals[id]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, nil
}
func (f *proposalRepoFake) UpdateDraft(_ context.Context, p domain.ResourceChangeProposal, event domain.ProposalEvent) error {
	f.proposals[p.ID] = p
	f.events = append(f.events, event)
	return nil
}
func (f *proposalRepoFake) Cancel(_ context.Context, id, _ string, _ time.Time) error {
	p := f.proposals[id]
	p.Status = domain.StatusCancelled
	f.proposals[id] = p
	return nil
}
func (f *proposalRepoFake) Confirm(_ context.Context, id, actor string, at time.Time) error {
	p := f.proposals[id]
	if p.Status != domain.StatusReadyForReview {
		return domain.ErrProposalAlreadyClaimed
	}
	p.Status = domain.StatusConfirmed
	p.ConfirmerID = actor
	p.ConfirmedAt = &at
	f.proposals[id] = p
	return nil
}
func (f *proposalRepoFake) ClaimApplying(_ context.Context, id, _ string, _ time.Time) (domain.ResourceChangeProposal, error) {
	p := f.proposals[id]
	if p.Status != domain.StatusConfirmed {
		return p, domain.ErrProposalAlreadyClaimed
	}
	p.Status = domain.StatusApplying
	f.proposals[id] = p
	return p, nil
}
func (f *proposalRepoFake) Finish(_ context.Context, id string, status domain.ProposalStatus, result domain.ApplyResult, event domain.ProposalEvent) error {
	if f.finishErr != nil {
		err := f.finishErr
		f.finishErr = nil
		return err
	}
	p := f.proposals[id]
	p.Status = status
	p.ApplyResult = result
	p.ErrorCode = event.Code
	f.proposals[id] = p
	return nil
}
func (f *proposalRepoFake) ListEvents(context.Context, string) ([]domain.ProposalEvent, error) {
	return nil, nil
}

type proposalAuthorizerFake struct {
	calls, failAt int
	err           error
}

func (f *proposalAuthorizerFake) AuthorizeProposal(context.Context, string, string, domain.ResourceKind, domain.ProposalOperation) error {
	f.calls++
	if f.err != nil || f.calls == f.failAt {
		if f.err != nil {
			return f.err
		}
		return domain.ErrProposalForbidden
	}
	return nil
}

type baselineFake struct {
	value string
	err   error
}

func (f *baselineFake) ResolveBaseline(context.Context, domain.ResourceChangeProposal) (string, error) {
	return f.value, f.err
}

type proposalApplierFake struct {
	calls  int
	result domain.ApplyResult
	err    error
}

func (f *proposalApplierFake) ApplyResourceChange(context.Context, domain.ProposalEnvelope) (domain.ApplyResult, error) {
	f.calls++
	return f.result, f.err
}

func newProposalServiceForTest(repo port.ProposalRepo, auth port.ProposalAuthorizer, baseline port.BaselineResolver, appliers map[domain.ResourceKind]port.ResourceChangeApplier) *ResourceChangeProposalService {
	service := NewResourceChangeProposalService(repo, auth, baseline, appliers, observability.NoopMetrics{})
	service.now = func() time.Time { return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC) }
	service.newID = func() string { return "proposal-" + time.Now().Format("150405.000000000") }
	return service
}
