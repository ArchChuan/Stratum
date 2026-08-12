package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentpersist "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/stretchr/testify/require"
)

type proposalE2EAuthorizer struct{ roles map[string]string }

func (a proposalE2EAuthorizer) AuthorizeProposal(
	_ context.Context,
	_, actorID string,
	_ domain.ResourceKind,
	_ domain.ProposalOperation,
	action domain.ProposalAction,
) error {
	switch a.roles[actorID] {
	case "admin", "owner":
		return nil
	case "member":
		if action == domain.ProposalActionCreate {
			return nil
		}
	}
	return domain.ErrProposalForbidden
}

type proposalE2EApplier struct {
	mu    sync.Mutex
	calls int
}

type proposalE2EBaseline struct{ fingerprint string }

func (b *proposalE2EBaseline) ResolveBaseline(
	context.Context,
	domain.ResourceChangeProposal,
) (agentport.ResourceBaseline, error) {
	return agentport.ResourceBaseline{
		Fingerprint: b.fingerprint,
		Projection:  json.RawMessage(`{"name":"原资源"}`),
	}, nil
}

func (a *proposalE2EApplier) ApplyResourceChange(
	_ context.Context,
	envelope domain.ProposalEnvelope,
) (domain.ApplyResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return domain.ApplyResult{
		ResourceID: "created-agent", Fingerprint: "fingerprint-1",
		Readback: json.RawMessage(`{"id":"created-agent","name":"受治理 Agent"}`),
	}, nil
}

func (a *proposalE2EApplier) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func TestSystemAssistantProposalPostgresAuthorizationSecretsAndConcurrency(t *testing.T) {
	pool, tenants := setupSystemAssistantPostgres(t)
	repo := agentpersist.NewPgResourceChangeProposalRepo(pool)
	adminID, memberID := "proposal-admin", "proposal-member"
	applier := &proposalE2EApplier{}
	service := agentapp.NewResourceChangeProposalService(
		repo,
		proposalE2EAuthorizer{roles: map[string]string{adminID: "admin", memberID: "member"}},
		nil,
		map[domain.ResourceKind]agentport.ResourceChangeApplier{
			domain.ResourceAgent: applier, domain.ResourceSkillDraft: applier,
			domain.ResourceMCPConfig: applier, domain.ResourceKnowledgeWorkspace: applier,
		},
		nil,
	)

	adminCtx := assistantTenantContext(tenants[0], adminID, tenantdb.RoleTenantAdmin)
	memberCtx := assistantTenantContext(tenants[0], memberID, tenantdb.RoleTenantUser)
	otherTenantCtx := assistantTenantContext(tenants[1], adminID, tenantdb.RoleTenantAdmin)
	payload := json.RawMessage(`{"name":"受治理 Agent","description":"端到端验收","model":"test-model","maxIterations":4,"maxContextTokens":4096}`)

	// D6：member 可创建提案（进入待审流），但不能确认/应用（decide 仍 fail closed）。
	memberProposal, err := service.CreateProposal(memberCtx, agentapp.CreateProposalInput{
		TenantID: tenants[0], ActorID: memberID, Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: payload,
	})
	require.NoError(t, err)
	require.Equal(t, domain.StatusReadyForReview, memberProposal.Status)
	_, err = service.ConfirmAndApply(memberCtx, tenants[0], memberProposal.ID, memberID)
	require.ErrorIs(t, err, domain.ErrProposalForbidden)

	invalid, err := service.CreateProposal(adminCtx, agentapp.CreateProposalInput{
		TenantID: tenants[0], ActorID: adminID, Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate,
		Payload:   json.RawMessage(`{"name":"marker-secret","description":"x","model":"m","maxIterations":2,"maxContextTokens":1024,"token":"marker-secret"}`),
	})
	require.ErrorIs(t, err, domain.ErrProposalInvalid)
	storedInvalid, err := repo.Get(adminCtx, invalid.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(storedInvalid.Payload))
	require.NotContains(t, string(storedInvalid.Payload), "marker-secret")

	for _, field := range []string{"apiKey", "Authorization", "password", "env", "headers"} {
		marker := "marker-" + field
		secretPayload := json.RawMessage(`{"name":"blocked","description":"x","model":"m","maxIterations":2,"maxContextTokens":1024,"` +
			field + `":"` + marker + `"}`)
		invalidProposal, createErr := service.CreateProposal(adminCtx, agentapp.CreateProposalInput{
			TenantID: tenants[0], ActorID: adminID, Kind: domain.ResourceAgent,
			Operation: domain.OperationCreate, Payload: secretPayload,
		})
		require.ErrorIs(t, createErr, domain.ErrProposalInvalid)
		stored, getErr := repo.Get(adminCtx, invalidProposal.ID)
		require.NoError(t, getErr)
		require.NotContains(t, string(stored.Payload), marker)
	}

	proposal, err := service.CreateProposal(adminCtx, agentapp.CreateProposalInput{
		TenantID: tenants[0], ActorID: adminID, Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: payload,
	})
	require.NoError(t, err)
	_, err = service.Get(otherTenantCtx, tenants[1], adminID, proposal.ID)
	require.True(t, errors.Is(err, domain.ErrProposalNotFound) || errors.Is(err, domain.ErrProposalForbidden))

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, confirmErr := service.ConfirmAndApply(adminCtx, tenants[0], proposal.ID, adminID)
			results <- confirmErr
		}()
	}
	var successes int
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, applier.callCount())
	stored, err := repo.Get(adminCtx, proposal.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusApplied, stored.Status)

	otherCreates := []struct {
		kind    domain.ResourceKind
		payload json.RawMessage
	}{
		{domain.ResourceSkillDraft, json.RawMessage(`{"name":"检索 Skill","description":"检索资料","instructions":"只引用已核验资料"}`)},
		{domain.ResourceMCPConfig, json.RawMessage(`{"name":"docs","version":"1","transport":"streamable-http","url":"https://example.test/mcp","timeoutSec":30}`)},
		{domain.ResourceKnowledgeWorkspace, json.RawMessage(`{"name":"官方资料","description":"已核验资料","embeddingModel":"text-embedding-v3"}`)},
	}
	for _, tc := range otherCreates {
		created, createErr := service.CreateProposal(adminCtx, agentapp.CreateProposalInput{
			TenantID: tenants[0], ActorID: adminID, Kind: tc.kind,
			Operation: domain.OperationCreate, Payload: tc.payload,
		})
		require.NoError(t, createErr)
		applied, applyErr := service.ConfirmAndApply(adminCtx, tenants[0], created.ID, adminID)
		require.NoError(t, applyErr)
		require.Equal(t, domain.StatusApplied, applied.Status)
	}
	require.Equal(t, 4, applier.callCount())

	baseline := &proposalE2EBaseline{fingerprint: "baseline-v1"}
	updateService := agentapp.NewResourceChangeProposalService(
		repo,
		proposalE2EAuthorizer{roles: map[string]string{adminID: "admin"}},
		baseline,
		map[domain.ResourceKind]agentport.ResourceChangeApplier{
			domain.ResourceAgent: applier, domain.ResourceSkillDraft: applier,
			domain.ResourceMCPConfig: applier, domain.ResourceKnowledgeWorkspace: applier,
		},
		nil,
	)
	updates := []struct {
		kind    domain.ResourceKind
		payload json.RawMessage
	}{
		{domain.ResourceAgent, payload},
		{domain.ResourceSkillDraft, json.RawMessage(`{"name":"检索 Skill","description":"新说明","instructions":"只引用已核验资料"}`)},
		{domain.ResourceMCPConfig, json.RawMessage(`{"name":"docs","version":"2","transport":"streamable-http","url":"https://example.test/mcp","timeoutSec":30}`)},
		{domain.ResourceKnowledgeWorkspace, json.RawMessage(`{"name":"官方资料","description":"新说明","embeddingModel":"text-embedding-v3"}`)},
	}
	for _, tc := range updates {
		created, createErr := updateService.CreateProposal(adminCtx, agentapp.CreateProposalInput{
			TenantID: tenants[0], ActorID: adminID, Kind: tc.kind, ResourceID: "resource-1",
			Operation: domain.OperationUpdate, Payload: tc.payload,
		})
		require.NoError(t, createErr)
		require.Equal(t, "baseline-v1", created.BaselineFingerprint)
		require.JSONEq(t, `{"name":"原资源"}`, string(created.BaselineProjection))
		applied, applyErr := updateService.ConfirmAndApply(adminCtx, tenants[0], created.ID, adminID)
		require.NoError(t, applyErr)
		require.Equal(t, domain.StatusApplied, applied.Status)
	}

	stale, err := updateService.CreateProposal(adminCtx, agentapp.CreateProposalInput{
		TenantID: tenants[0], ActorID: adminID, Kind: domain.ResourceAgent, ResourceID: "resource-stale",
		Operation: domain.OperationUpdate, Payload: payload,
	})
	require.NoError(t, err)
	baseline.fingerprint = "baseline-v2"
	beforeStale := applier.callCount()
	_, err = updateService.ConfirmAndApply(adminCtx, tenants[0], stale.ID, adminID)
	require.ErrorIs(t, err, domain.ErrProposalStale)
	require.Equal(t, beforeStale, applier.callCount())
	storedStale, err := repo.Get(adminCtx, stale.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusStale, storedStale.Status)
}
