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
) error {
	if role := a.roles[actorID]; role != "admin" && role != "owner" {
		return domain.ErrProposalForbidden
	}
	return nil
}

type proposalE2EApplier struct {
	mu    sync.Mutex
	calls int
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
		map[domain.ResourceKind]agentport.ResourceChangeApplier{domain.ResourceAgent: applier},
		nil,
	)

	adminCtx := assistantTenantContext(tenants[0], adminID, tenantdb.RoleTenantAdmin)
	memberCtx := assistantTenantContext(tenants[0], memberID, tenantdb.RoleTenantUser)
	otherTenantCtx := assistantTenantContext(tenants[1], adminID, tenantdb.RoleTenantAdmin)
	payload := json.RawMessage(`{"name":"受治理 Agent","description":"端到端验收","model":"test-model","maxIterations":4,"maxContextTokens":4096}`)

	_, err := service.CreateProposal(memberCtx, agentapp.CreateProposalInput{
		TenantID: tenants[0], ActorID: memberID, Kind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: payload,
	})
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
}
