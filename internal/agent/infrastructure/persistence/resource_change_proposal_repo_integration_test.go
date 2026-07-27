package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestResourceChangeProposalIntegrationTenantIsolationAndSingleClaim(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, pgstore.ProvisionPublicSchema(context.Background(), pool, zap.NewNop()))
	suffix := time.Now().UnixNano()
	tenantA := fmt.Sprintf("proposal_e2e_a_%d", suffix)
	tenantB := fmt.Sprintf("proposal_e2e_b_%d", suffix)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "tenant_`+tenantA+`" CASCADE`)
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "tenant_`+tenantB+`" CASCADE`)
	})
	require.NoError(t, pgstore.ProvisionTenantSchema(context.Background(), pool, tenantA))
	require.NoError(t, pgstore.ProvisionTenantSchema(context.Background(), pool, tenantB))
	repo := NewPgResourceChangeProposalRepo(pool)
	ctxA := tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{TenantID: tenantA, UserID: "admin", Role: tenantdb.RoleTenantAdmin})
	ctxB := tenantdb.WithTenant(context.Background(), &tenantdb.TenantContext{TenantID: tenantB, UserID: "admin", Role: tenantdb.RoleTenantAdmin})

	now := time.Now().UTC()
	proposal := domain.ResourceChangeProposal{
		ID: "proposal-integration", TenantID: tenantA, ProposerID: "admin", ResourceKind: domain.ResourceAgent,
		Operation: domain.OperationCreate, Payload: json.RawMessage(`{"name":"agent"}`), Status: domain.StatusReadyForReview,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.Create(ctxA, proposal, domain.ProposalEvent{ActorID: "admin", ToStatus: domain.StatusReadyForReview, CreatedAt: now}))
	stored, err := repo.Get(ctxA, proposal.ID)
	require.NoError(t, err)
	require.Equal(t, tenantA, stored.TenantID)
	_, err = repo.Get(ctxB, proposal.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, repo.Confirm(ctxA, proposal.ID, "admin", now))

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, claimErr := repo.ClaimApplying(ctxA, proposal.ID, "admin", time.Now().UTC()); claimErr == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	_, err = repo.ClaimApplying(ctxA, proposal.ID, "admin", time.Now().UTC())
	require.ErrorIs(t, err, domain.ErrProposalAlreadyClaimed)
	events, err := repo.ListEvents(ctxA, proposal.ID)
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Equal(t, []domain.ProposalStatus{
		domain.StatusReadyForReview, domain.StatusConfirmed, domain.StatusApplying,
	}, []domain.ProposalStatus{events[0].ToStatus, events[1].ToStatus, events[2].ToStatus})

	expired := proposal
	expired.ID = "proposal-expired"
	expired.ExpiresAt = now.Add(-time.Second)
	require.NoError(t, repo.Create(ctxA, expired, domain.ProposalEvent{ActorID: "admin", ToStatus: domain.StatusReadyForReview, CreatedAt: now}))
	err = repo.Confirm(ctxA, expired.ID, "admin", now)
	require.ErrorIs(t, err, domain.ErrProposalExpired)

	confirm := proposal
	confirm.ID = "proposal-confirm-race"
	require.NoError(t, repo.Create(ctxA, confirm, domain.ProposalEvent{ActorID: "admin", ToStatus: domain.StatusReadyForReview, CreatedAt: now}))
	wins.Store(0)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if confirmErr := repo.Confirm(ctxA, confirm.ID, "admin", now); confirmErr == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
}
