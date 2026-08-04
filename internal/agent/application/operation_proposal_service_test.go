package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
)

func newOperationProposalServiceForTest(
	repo *operationProposalRepoFake,
	roles port.TenantRoleResolver,
	metrics *gateMetricsFake,
) *OperationProposalService {
	var provider observability.MetricsProvider
	if metrics != nil {
		provider = metrics
	}
	service := NewOperationProposalService(repo, roles, provider)
	service.now = func() time.Time { return gateFixedNow }
	return service
}

type roleResolverFake struct {
	role string
	err  error
}

func (r roleResolverFake) ResolveTenantRole(_ context.Context, _, _ string) (string, error) {
	return r.role, r.err
}

func seededProposal(t *testing.T, repo *operationProposalRepoFake) domain.OperationProposal {
	t.Helper()
	p := domain.OperationProposal{
		ID: "p1", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
		Fingerprint: "fp-test", Status: domain.OpProposed, ProposerID: "member-1",
		CreatedAt: gateFixedNow, UpdatedAt: gateFixedNow,
	}
	repo.proposals[p.ID] = p
	return p
}

func TestOperationProposalReviewerAuthorization(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		roles port.TenantRoleResolver
	}{
		{name: "member cannot approve", roles: roleResolverFake{role: "member"}},
		{name: "nil resolver fails closed", roles: nil},
		{name: "role lookup failure fails closed", roles: roleResolverFake{err: context.DeadlineExceeded}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOperationProposalRepoFake()
			seededProposal(t, repo)
			service := newOperationProposalServiceForTest(repo, tc.roles, nil)
			err := service.Approve(ctx, "tenant-1", "admin-1", "p1", "")
			require.ErrorIs(t, err, domain.ErrProposalForbidden)
			require.Equal(t, domain.OpProposed, repo.proposals["p1"].Status)
		})
	}
}

func TestOperationProposalListPendingAndGet(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seededProposal(t, repo)
	repo.proposals["p2"] = domain.OperationProposal{
		ID: "p2", TenantID: "tenant-1", AgentID: "agent-1", OpType: "self_modify",
		Fingerprint: "fp-2", Status: domain.OpRejected, ProposerID: "member-1",
	}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
	ctx := context.Background()

	pending, err := service.ListPending(ctx, "tenant-1", "admin-1")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "p1", pending[0].ID)

	got, err := service.Get(ctx, "tenant-1", "admin-1", "p1")
	require.NoError(t, err)
	require.Equal(t, "member-1", got.ProposerID)

	_, err = service.Get(ctx, "tenant-1", "admin-1", "missing")
	require.ErrorIs(t, err, domain.ErrOperationProposalNotFound)
}

func TestOperationProposalStartReview(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seededProposal(t, repo)
	metrics := &gateMetricsFake{}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "owner"}, metrics)

	require.NoError(t, service.StartReview(context.Background(), "tenant-1", "admin-1", "p1"))
	p := repo.proposals["p1"]
	require.Equal(t, domain.OpReviewing, p.Status)
	require.Equal(t, "admin-1", p.ReviewedBy)
	require.Equal(t, []string{"self_modify|reviewing"}, metrics.calls)
}

func TestOperationProposalApprove(t *testing.T) {
	repo := newOperationProposalRepoFake()
	seededProposal(t, repo)
	metrics := &gateMetricsFake{}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, metrics)

	require.NoError(t, service.Approve(context.Background(), "tenant-1", "admin-1", "p1", "looks fine"))
	p := repo.proposals["p1"]
	require.Equal(t, domain.OpApproved, p.Status)
	require.Equal(t, "admin-1", p.ReviewedBy)
	require.Equal(t, "looks fine", p.ReviewNote)
	require.NotNil(t, p.ExpiresAt)
	require.Equal(t, gateFixedNow.Add(constants.OperationApprovalTTL), *p.ExpiresAt)
	require.Nil(t, p.ResolvedAt)
	require.Equal(t, []string{"self_modify|approved"}, metrics.calls)

	t.Run("second approval on resolved proposal fails", func(t *testing.T) {
		err := service.Approve(context.Background(), "tenant-1", "admin-1", "p1", "")
		require.ErrorIs(t, err, domain.ErrOperationProposalResolved)
	})
}

func TestOperationProposalReject(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	seededProposal(t, repo)
	metrics := &gateMetricsFake{}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, metrics)

	t.Run("reject requires a note", func(t *testing.T) {
		err := service.Reject(ctx, "tenant-1", "admin-1", "p1", "  ")
		require.ErrorIs(t, err, domain.ErrProposalInvalid)
		require.Equal(t, domain.OpProposed, repo.proposals["p1"].Status)
	})

	t.Run("reject bounds the note length", func(t *testing.T) {
		err := service.Reject(ctx, "tenant-1", "admin-1", "p1", strings.Repeat("x", OperationReviewNoteMaxRunes+1))
		require.ErrorIs(t, err, domain.ErrProposalInvalid)
	})

	t.Run("reject succeeds with a bounded note", func(t *testing.T) {
		require.NoError(t, service.Reject(ctx, "tenant-1", "admin-1", "p1", "out of scope"))
		p := repo.proposals["p1"]
		require.Equal(t, domain.OpRejected, p.Status)
		require.Equal(t, "admin-1", p.ReviewedBy)
		require.Equal(t, "out of scope", p.ReviewNote)
		require.NotNil(t, p.ResolvedAt)
		require.Equal(t, gateFixedNow, *p.ResolvedAt)
		require.Nil(t, p.ExpiresAt)
		require.Equal(t, []string{"self_modify|rejected"}, metrics.calls)
	})
}

func TestOperationProposalReviewFromReviewingState(t *testing.T) {
	repo := newOperationProposalRepoFake()
	p := seededProposal(t, repo)
	p.Status = domain.OpReviewing
	repo.proposals[p.ID] = p
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)

	require.NoError(t, service.Approve(context.Background(), "tenant-1", "admin-1", "p1", "ok"))
	require.Equal(t, domain.OpApproved, repo.proposals["p1"].Status)
}
