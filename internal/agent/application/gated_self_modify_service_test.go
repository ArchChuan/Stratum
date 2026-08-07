package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// gateAgentRepoFake backs AgentService.Update with the smallest possible
// in-memory repo: Get/Update only, everything else inert.
type gateAgentRepoFake struct {
	agents map[string]*domain.AgentConfig
}

func (f *gateAgentRepoFake) Register(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (f *gateAgentRepoFake) Get(_ context.Context, id string) (*domain.AgentConfig, bool, error) {
	cfg, ok := f.agents[id]
	return cfg, ok, nil
}
func (f *gateAgentRepoFake) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (f *gateAgentRepoFake) GetAll(context.Context) ([]*domain.AgentConfig, error) { return nil, nil }
func (f *gateAgentRepoFake) Remove(_ context.Context, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (f *gateAgentRepoFake) Update(_ context.Context, cfg *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	f.agents[cfg.ID] = cfg
	return nil
}
func (f *gateAgentRepoFake) UpdateSystemAssistantModel(_ context.Context, _ string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (f *gateAgentRepoFake) UpdateSystemAssistantAll(_ context.Context, _ string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return nil, nil
}
func (f *gateAgentRepoFake) UpdateSystemAssistantBindings(context.Context, []string, []string, []string) (*domain.AgentConfig, error) {
	return nil, nil
}

func newGatedServiceForTest(t *testing.T, repo *operationProposalRepoFake, usage *operationUsageRepoFake, seedAgent *domain.AgentConfig) (*AgentService, *gateAgentRepoFake, *operationProposalRepoFake, *operationUsageRepoFake, *gateMetricsFake) {
	t.Helper()
	agents := map[string]*domain.AgentConfig{}
	if seedAgent != nil {
		agents[seedAgent.ID] = seedAgent
	}
	agentRepo := &gateAgentRepoFake{agents: agents}
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(agentRepo, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantRoleResolver: stubTenantRole{role: "owner"},
		Logger:             zap.NewNop(),
	})
	metrics := &gateMetricsFake{}
	gate := newGateServiceForTest(repo, usage, metrics)
	svc.SetOperationGate(gate)
	return svc, agentRepo, repo, usage, metrics
}

func selfModifyRequest(name string) SelfModifyRequest {
	return SelfModifyRequest{Name: name, Description: "desc", MaxContextTokens: 8192}
}

func TestGatedSelfModifyFailsClosedWithoutGate(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	_, err := svc.GatedSelfModify(context.Background(), "tenant-1", "member-1", "agent-1", selfModifyRequest("x"))
	require.ErrorIs(t, err, ErrGateUnavailable)
}

func TestGatedSelfModifyAlwaysProposesWithoutApproval(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, newOperationUsageRepoFake(), seed)

	result, err := svc.GatedSelfModify(context.Background(), "tenant-1", "member-1", "agent-1", selfModifyRequest("renamed"))
	require.NoError(t, err)
	require.False(t, result.Decision.Allowed)
	require.Equal(t, GateReasonPendingApproval, result.Decision.Reason)
	require.NotEmpty(t, result.Decision.ProposalID)
	require.Empty(t, result.DTO.ID)

	p := repo.proposals[result.Decision.ProposalID]
	require.Equal(t, "agent-1", p.AgentID)
	require.Equal(t, "member-1", p.ProposerID)
	require.Equal(t, "self_modify", p.OpType)
	// The mutation did not land.
	require.Equal(t, "old", agentRepo.agents["agent-1"].Name)
}

func TestGatedSelfModifyReplayLandsMutationAndRecordsUsage(t *testing.T) {
	repo := newOperationProposalRepoFake()
	usage := newOperationUsageRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, usage, seed)
	ctx := context.Background()

	// Approve a proposal for the same content, same proposer.
	req := selfModifyRequest("renamed")
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "tenant-1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	result, err := svc.GatedSelfModify(ctx, "tenant-1", "member-1", "agent-1", req)
	require.NoError(t, err)
	require.True(t, result.Decision.Allowed)
	require.Equal(t, GateReasonApprovedReplay, result.Decision.Reason)
	require.Equal(t, "agent-1", result.DTO.ID)
	require.Equal(t, "renamed", agentRepo.agents["agent-1"].Name)

	// The approval was consumed once and usage recorded.
	require.Equal(t, domain.OpExecuted, repo.proposals[approved.ID].Status)
	got, err := usage.DailyUsage(ctx, "tenant-1", "agent-1", port.OpSelfModify, gateFixedNow)
	require.NoError(t, err)
	require.Equal(t, 1, got.Executions)

	// Replay is single-use: a second identical request reproposes.
	result, err = svc.GatedSelfModify(ctx, "tenant-1", "member-1", "agent-1", req)
	require.NoError(t, err)
	require.False(t, result.Decision.Allowed)
	require.Equal(t, GateReasonPendingApproval, result.Decision.Reason)
}

// TestGatedSelfModifyReplayLandsForMemberWithoutOwnership locks the operation
// gate contract: a member (non-owner, non-admin) who is NOT the resource
// owner may still land an approved replay. Ownership is adjudicated by the
// human approver at proposal time; the replay executes as a system actor.
func TestGatedSelfModifyReplayLandsForMemberWithoutOwnership(t *testing.T) {
	repo := newOperationProposalRepoFake()
	usage := newOperationUsageRepoFake()
	// The agent belongs to an admin; the proposing member is unrelated.
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old", CreatedBy: "admin-1"}
	agents := map[string]*domain.AgentConfig{"agent-1": seed}
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(&gateAgentRepoFake{agents: agents}, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantRoleResolver: stubTenantRole{role: "member"},
		Logger:             zap.NewNop(),
	})
	gate := newGateServiceForTest(repo, usage, &gateMetricsFake{})
	svc.SetOperationGate(gate)
	ctx := context.Background()

	req := selfModifyRequest("renamed")
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "tenant-1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	result, err := svc.GatedSelfModify(ctx, "tenant-1", "member-1", "agent-1", req)
	require.NoError(t, err)
	require.True(t, result.Decision.Allowed)
	require.Equal(t, GateReasonApprovedReplay, result.Decision.Reason)
	require.Equal(t, "renamed", agents["agent-1"].Name)
	require.Equal(t, domain.OpExecuted, repo.proposals[approved.ID].Status)
}

func TestGatedSelfModifyReplayBoundToProposer(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, newOperationUsageRepoFake(), seed)
	ctx := context.Background()

	req := selfModifyRequest("renamed")
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "tenant-1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	// A different actor cannot consume the approval; the open approval still
	// blocks a duplicate proposal for the same content.
	result, err := svc.GatedSelfModify(ctx, "tenant-1", "intruder", "agent-1", req)
	require.NoError(t, err)
	require.False(t, result.Decision.Allowed)
	require.Equal(t, GateReasonDuplicatePending, result.Decision.Reason)
	require.Equal(t, "old", agentRepo.agents["agent-1"].Name)
}

func TestGatedSelfModifySurfacesUsageFailure(t *testing.T) {
	repo := newOperationProposalRepoFake()
	usage := newOperationUsageRepoFake()
	usage.err = errors.New("disk full")
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, usage, seed)
	ctx := context.Background()

	req := selfModifyRequest("renamed")
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "tenant-1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	// The mutation landed but the accounting failure is surfaced, not hidden.
	result, err := svc.GatedSelfModify(ctx, "tenant-1", "member-1", "agent-1", req)
	require.NoError(t, err)
	require.True(t, result.Decision.Allowed)
	require.Equal(t, "renamed", agentRepo.agents["agent-1"].Name)
	require.Error(t, result.UsageErr)
}

func TestGatedSelfModifyFingerprintBindsPayload(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old"}
	svc, _, _, _, _ := newGatedServiceForTest(t, repo, newOperationUsageRepoFake(), seed)
	ctx := context.Background()

	req := selfModifyRequest("renamed")
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "tenant-1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	// Same agent, same proposer, different content: the approval does not fit
	// and a new proposal is created instead of a replay.
	result, err := svc.GatedSelfModify(ctx, "tenant-1", "member-1", "agent-1", selfModifyRequest("different"))
	require.NoError(t, err)
	require.False(t, result.Decision.Allowed)
	require.Equal(t, GateReasonPendingApproval, result.Decision.Reason)
	require.NotEqual(t, approved.ID, result.Decision.ProposalID)
}

// stubTenantRole resolves every actor as a fixed role so ownership tests
// control authorization via the fake, not tenant membership.
type stubTenantRole struct{ role string }

func (s stubTenantRole) ResolveTenantRole(_ context.Context, _, _ string) (string, error) {
	return s.role, nil
}
