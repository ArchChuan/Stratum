package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var errBindingRejected = errors.New("binding rejected")

// recordingBindingValidator implements port.WorkspaceBindingValidator for
// tests: fails when err is set, otherwise records the validated names.
type recordingBindingValidator struct {
	got  [][]string
	err  error
	call int
}

func (v *recordingBindingValidator) ValidateWorkspaceBindings(_ context.Context, _ string, names []string) error {
	v.call++
	v.got = append(v.got, names)
	return v.err
}

func newBindingTestService(t *testing.T, validator port.WorkspaceBindingValidator) *AgentService {
	t.Helper()
	return NewAgentService(AgentServiceDeps{
		Registry:                  NewRegistry(&gateAgentRepoFake{agents: map[string]*domain.AgentConfig{}}, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantRoleResolver:        stubTenantRole{role: "owner"},
		WorkspaceBindingValidator: validator,
		Logger:                    zap.NewNop(),
	})
}

func TestCreateRejectsUnknownWorkspaceBinding(t *testing.T) {
	validator := &recordingBindingValidator{err: errBindingRejected}
	svc := newBindingTestService(t, validator)

	_, err := svc.Create(context.Background(), CreateAgentInput{
		ActorID: "u1", TenantID: "t1", Name: "Research",
		KnowledgeWorkspaceIDs: []string{"legal", "hr"},
	})
	require.ErrorIs(t, err, errBindingRejected)
	require.Equal(t, [][]string{{"legal", "hr"}}, validator.got)
}

func TestCreateFailsClosedWithoutValidator(t *testing.T) {
	// Un-wired validator + non-empty bindings → rejected (D10 fail closed).
	svc := NewAgentService(AgentServiceDeps{
		Registry:           NewRegistry(&gateAgentRepoFake{agents: map[string]*domain.AgentConfig{}}, BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		TenantRoleResolver: stubTenantRole{role: "owner"},
		Logger:             zap.NewNop(),
	})
	_, err := svc.Create(context.Background(), CreateAgentInput{
		ActorID: "u1", TenantID: "t1", Name: "Research",
		KnowledgeWorkspaceIDs: []string{"legal"},
	})
	require.ErrorContains(t, err, "workspace binding validation unavailable")
}

func TestCreateSkipsValidationWhenUnbound(t *testing.T) {
	validator := &recordingBindingValidator{}
	svc := newBindingTestService(t, validator)

	// No bindings → validator never called, create succeeds.
	_, err := svc.Create(context.Background(), CreateAgentInput{
		ActorID: "u1", TenantID: "t1", Name: "Research",
	})
	require.NoError(t, err)
	require.Zero(t, validator.call)
}

func TestUpdateRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newOperationProposalRepoFake()
	usage := newOperationUsageRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old", CreatedBy: "u1"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, usage, seed)
	svc.deps.WorkspaceBindingValidator = &recordingBindingValidator{err: errBindingRejected}
	ctx := reqctx.WithTenantID(context.Background(), "t1")

	// Update flows through buildUpdateConfig, which validates bindings.
	_, err := svc.Update(ctx, "agent-1", UpdateAgentInput{
		ActorID: "u1", Name: "new",
		KnowledgeWorkspaceIDs: []string{"legal"},
	})
	require.ErrorIs(t, err, errBindingRejected)
	require.Equal(t, "old", agentRepo.agents["agent-1"].Name) // no partial write
}

func TestGatedSelfModifyRejectsUnknownWorkspaceBinding(t *testing.T) {
	repo := newOperationProposalRepoFake()
	usage := newOperationUsageRepoFake()
	seed := &domain.AgentConfig{ID: "agent-1", Name: "old", CreatedBy: "u1"}
	svc, agentRepo, _, _, _ := newGatedServiceForTest(t, repo, usage, seed)
	svc.deps.WorkspaceBindingValidator = &recordingBindingValidator{err: errBindingRejected}
	ctx := reqctx.WithTenantID(context.Background(), "t1")

	// Approved replay → s.Update → buildUpdateConfig: the binding check runs
	// after the gate, and its failure aborts the mutation.
	req := selfModifyRequest("renamed")
	req.KnowledgeWorkspaceIDs = []string{"legal"}
	fingerprint, err := svc.deps.OperationGate.ComputeFingerprint("agent-1", port.OpSelfModify, req)
	require.NoError(t, err)
	approved := seededProposal(t, repo)
	approved.Fingerprint = fingerprint
	repo.proposals[approved.ID] = approved
	require.NoError(t, repo.UpdateStatus(ctx, "t1", approved.ID, domain.OpApproved, "admin-1", "ok"))

	// The gate approved the replay; the binding failure surfaces as the
	// operation error and the mutation never lands (DTO stays empty).
	result, err := svc.GatedSelfModify(ctx, "t1", "member-1", "agent-1", req)
	require.ErrorIs(t, err, errBindingRejected)
	require.Empty(t, result.DTO.ID)
	require.True(t, result.Decision.Allowed) // gate decision is preserved
	require.Equal(t, "old", agentRepo.agents["agent-1"].Name)
}
