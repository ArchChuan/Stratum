package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ownershipAgentRepoFake is a minimal in-memory AgentRepo that captures every
// audit event handed to a write method, so ownership tests can assert the
// change-audit contract (mockAgentRepo discards the audit payload).
type ownershipAgentRepoFake struct {
	agents    map[string]*domain.AgentConfig
	audits    []*auditdomain.ResourceChangeAuditEvent
	updateErr error
}

func newOwnershipAgentRepoFake() *ownershipAgentRepoFake {
	return &ownershipAgentRepoFake{agents: map[string]*domain.AgentConfig{}}
}

func (f *ownershipAgentRepoFake) recordAudit(audit *auditdomain.ResourceChangeAuditEvent) {
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
}

func (f *ownershipAgentRepoFake) Register(_ context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.recordAudit(audit)
	f.agents[cfg.ID] = cfg
	return nil
}
func (f *ownershipAgentRepoFake) Get(_ context.Context, id string) (*domain.AgentConfig, bool, error) {
	cfg, ok := f.agents[id]
	return cfg, ok, nil
}
func (f *ownershipAgentRepoFake) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (f *ownershipAgentRepoFake) GetAll(context.Context) ([]*domain.AgentConfig, error) {
	return nil, nil
}
func (f *ownershipAgentRepoFake) Remove(_ context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.recordAudit(audit)
	delete(f.agents, id)
	return nil
}
func (f *ownershipAgentRepoFake) Update(_ context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent) error {
	f.recordAudit(audit)
	if f.updateErr != nil {
		return f.updateErr
	}
	f.agents[cfg.ID] = cfg
	return nil
}
func (f *ownershipAgentRepoFake) UpdateSystemAssistantModel(_ context.Context, _ string, _ string, _ bool, _ int, _ int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	f.recordAudit(audit)
	return nil, nil
}
func (f *ownershipAgentRepoFake) UpdateSystemAssistantAll(_ context.Context, _ string, _ string, _ bool, _ int, _ int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	f.recordAudit(audit)
	return nil, nil
}

var _ port.AgentRepo = (*ownershipAgentRepoFake)(nil)

// failingTenantRoleResolver resolves no role at all so ownership checks fail
// closed on resolver failure (IAM outage must not default-open writes).
type failingTenantRoleResolver struct{ err error }

func (f failingTenantRoleResolver) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "", f.err
}

// newOwnershipAgentService wires a service around the audit-capturing fake;
// the role resolver is injected per test row.
func newOwnershipAgentService(repo port.AgentRepo) *application.AgentService {
	return application.NewAgentService(application.AgentServiceDeps{
		Registry: application.NewRegistry(repo, application.BuiltinSystemAssistantProfileSource(), zap.NewNop()),
		Logger:   zap.NewNop(),
	})
}

// TestOwnershipMatrixAgentUpdate pins the ownership matrix for Update: owner
// may manage the whole tenant (including historical resources with empty
// created_by), admin only their own, everyone else — and every resolution
// failure, missing resolver and empty actor — is denied. Fail closed.
func TestOwnershipMatrixAgentUpdate(t *testing.T) {
	const (
		actor = "user-1"
		other = "other-user"
	)
	rows := []struct {
		name        string
		role        string
		noResolver  bool
		resolveErr  error
		actorID     string
		createdBy   string
		wantAllowed bool
	}{
		{"owner edits another user's resource", "owner", false, nil, actor, other, true},
		{"owner edits empty-owner resource", "owner", false, nil, actor, "", true},
		{"admin edits own resource", "admin", false, nil, actor, actor, true},
		{"admin edits another user's resource", "admin", false, nil, actor, other, false},
		{"admin edits empty-owner resource", "admin", false, nil, actor, "", false},
		{"member edits own resource", "member", false, nil, actor, actor, false},
		{"resolver failure fails closed", "", false, errors.New("iam unavailable"), actor, actor, false},
		{"missing resolver fails closed", "", true, nil, actor, actor, false},
		{"empty actor fails closed", "owner", false, nil, "", actor, false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOwnershipAgentRepoFake()
			repo.agents["agent-1"] = &domain.AgentConfig{
				ID: "agent-1", Name: "original", Type: domain.ReActAgent, CreatedBy: tc.createdBy,
			}
			svc := newOwnershipAgentService(repo)
			if !tc.noResolver {
				svc.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			}
			if tc.resolveErr != nil {
				svc.SetTenantRoleResolver(failingTenantRoleResolver{err: tc.resolveErr})
			}
			_, err := svc.Update(context.Background(), "agent-1", application.UpdateAgentInput{
				ActorID: tc.actorID, Name: "renamed", Type: "react", MaxContextTokens: 4096,
			})
			if tc.wantAllowed {
				require.NoError(t, err)
				require.NotEmpty(t, repo.audits)
				return
			}
			require.ErrorIs(t, err, domain.ErrForbidden)
			require.Empty(t, repo.audits, "denied write must not produce an audit event")
		})
	}
}

// TestAgentCreateRecordsAuditEvent pins the create 打点: the caller becomes
// the agent's owner and the write carries a user/api create audit with an
// empty before projection and a non-empty after projection.
func TestAgentCreateRecordsAuditEvent(t *testing.T) {
	repo := newOwnershipAgentRepoFake()
	svc := newOwnershipAgentService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
	dto, err := svc.Create(ctx, application.CreateAgentInput{
		TenantID: "tenant-1", ActorID: "user-1", Name: "matrix-agent", Type: "react",
		MaxContextTokens: 4096,
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindAgent, audit.ResourceKind)
	require.Equal(t, dto.ID, audit.ResourceID)
	require.Equal(t, auditdomain.ChangeOpCreate, audit.Operation)
	require.Equal(t, "user-1", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, audit.Source)
	require.Empty(t, audit.Before) // persistence normalizes nil before -> `{}` (agent_repo.go)
	require.NotEmpty(t, audit.After)
	var after map[string]any
	require.NoError(t, json.Unmarshal(audit.After, &after))
	require.Equal(t, "matrix-agent", after["name"])
}

// TestAgentUpdateAuditFields pins the update audit payload: exact resource
// identity, operation, actor, source and actor type, with before/after
// projections reflecting the change.
func TestAgentUpdateAuditFields(t *testing.T) {
	repo := newOwnershipAgentRepoFake()
	repo.agents["agent-1"] = &domain.AgentConfig{
		ID: "agent-1", Name: "original", Type: domain.ReActAgent, CreatedBy: "user-1",
	}
	svc := newOwnershipAgentService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.Update(context.Background(), "agent-1", application.UpdateAgentInput{
		ActorID: "user-1", Name: "renamed", Type: "react", MaxContextTokens: 4096,
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, auditdomain.ResourceKindAgent, audit.ResourceKind)
	require.Equal(t, "agent-1", audit.ResourceID)
	require.Equal(t, auditdomain.ChangeOpUpdate, audit.Operation)
	require.Equal(t, "user-1", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorUser, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceAPI, audit.Source)
	require.Empty(t, audit.ProposalID)
	require.NotEmpty(t, audit.Before)
	var before map[string]any
	require.NoError(t, json.Unmarshal(audit.Before, &before))
	require.Equal(t, "original", before["name"])
	var after map[string]any
	require.NoError(t, json.Unmarshal(audit.After, &after))
	require.Equal(t, "renamed", after["name"])
}

// Audit-construction failure: newChangeAudit only fails when json.Marshal
// fails on the projections. AgentSafeProjection emits plain strings and
// []string, so the failure path is unreachable through Update inputs —
// skipped by design. The fail-closed contract is instead pinned below by
// making the repository write itself fail: the error must propagate, never
// be swallowed.
func TestAgentUpdatePropagatesRepositoryFailure(t *testing.T) {
	repo := newOwnershipAgentRepoFake()
	repo.agents["agent-1"] = &domain.AgentConfig{
		ID: "agent-1", Name: "original", Type: domain.ReActAgent, CreatedBy: "user-1",
	}
	repo.updateErr = errors.New("audit insert failed")
	svc := newOwnershipAgentService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	_, err := svc.Update(context.Background(), "agent-1", application.UpdateAgentInput{
		ActorID: "user-1", Name: "renamed", Type: "react", MaxContextTokens: 4096,
	})
	require.ErrorIs(t, err, repo.updateErr)
}

// TestAgentUpdateSystemActorBypassesOwnership pins the evaluation worker
// path: a system actor in ctx skips the ownership matrix (member may rewrite
// another user's agent) but the write is still audited with actor_type=system
// and source=optimization.
func TestAgentUpdateSystemActorBypassesOwnership(t *testing.T) {
	repo := newOwnershipAgentRepoFake()
	repo.agents["agent-1"] = &domain.AgentConfig{
		ID: "agent-1", Name: "original", Type: domain.ReActAgent, CreatedBy: "other-user",
	}
	svc := newOwnershipAgentService(repo)
	svc.SetTenantRoleResolver(stubTenantRole{role: "member"})

	ctx := reqctx.WithSystemActor(context.Background(), "evaluation-worker")
	_, err := svc.Update(ctx, "agent-1", application.UpdateAgentInput{
		ActorID: "member-1", Name: "optimized", Type: "react", MaxContextTokens: 4096,
	})
	require.NoError(t, err)
	require.Len(t, repo.audits, 1)

	audit := repo.audits[0]
	require.Equal(t, "evaluation-worker", audit.ActorID)
	require.Equal(t, auditdomain.ChangeActorSystem, audit.ActorType)
	require.Equal(t, auditdomain.ChangeSourceOptimization, audit.Source)
	require.Equal(t, auditdomain.ChangeOpUpdate, audit.Operation)
	require.Equal(t, "agent-1", audit.ResourceID)
}
