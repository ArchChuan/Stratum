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

func TestProposeGrantEditor(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)

	t.Run("member proposes editor access for an agent", func(t *testing.T) {
		err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "agent", "agent-1", "My Agent")
		require.NoError(t, err)
		require.Len(t, repo.proposals, 1)
		var p domain.OperationProposal
		for _, v := range repo.proposals {
			p = v
		}
		require.Equal(t, string(port.OpGrantEditor), p.OpType)
		require.Equal(t, "grant_editor|agent|agent-1|member-1", p.Fingerprint)
		require.Equal(t, domain.OpProposed, p.Status)
		require.Equal(t, "member-1", p.ProposerID)
		require.JSONEq(t, `{"resourceType":"agent","resourceId":"agent-1","resourceName":"My Agent","applicant":"member-1","action":"grant_editor_access"}`,
			string(p.PayloadSummary))
	})

	t.Run("duplicate pending request is rejected", func(t *testing.T) {
		err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "agent", "agent-1", "My Agent")
		require.ErrorIs(t, err, domain.ErrOperationProposalPending)
	})

	t.Run("invalid inputs fail closed", func(t *testing.T) {
		err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "agent", "", "")
		require.ErrorIs(t, err, domain.ErrProposalInvalid)
	})
}

func TestOperationProposalListMine(t *testing.T) {
	repo := newOperationProposalRepoFake()
	repo.proposals["p1"] = domain.OperationProposal{ID: "p1", TenantID: "tenant-1", AgentID: "a", OpType: "self_modify", Status: domain.OpProposed, ProposerID: "member-1"}
	repo.proposals["p2"] = domain.OperationProposal{ID: "p2", TenantID: "tenant-1", AgentID: "a", OpType: string(port.OpGrantEditor), Status: domain.OpExecuted, ProposerID: "member-1"}
	repo.proposals["p3"] = domain.OperationProposal{ID: "p3", TenantID: "tenant-1", AgentID: "a", OpType: "self_modify", Status: domain.OpProposed, ProposerID: "member-2"}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)

	mine, err := service.ListMine(context.Background(), "tenant-1", "member-1")
	require.NoError(t, err)
	require.Len(t, mine, 2)
	for _, p := range mine {
		require.Equal(t, "member-1", p.ProposerID)
	}

	t.Run("empty identity fails closed", func(t *testing.T) {
		_, err := service.ListMine(context.Background(), "tenant-1", "")
		require.ErrorIs(t, err, domain.ErrProposalForbidden)
	})
}

func TestApproveGrantEditorGrantsAndExecutes(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	metrics := &gateMetricsFake{}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, metrics)

	var gotTenant, gotKind, gotResource, gotEditor string
	service.WithGrantEditor(func(_ context.Context, tenantID, resourceType, resourceID, editorID string) error {
		gotTenant, gotKind, gotResource, gotEditor = tenantID, resourceType, resourceID, editorID
		return nil
	})

	require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "skill", "skill-1", "My Skill"))
	var pid string
	for id := range repo.proposals {
		pid = id
	}
	require.NoError(t, service.Approve(ctx, "tenant-1", "admin-1", pid, "granted"))
	require.Equal(t, "tenant-1", gotTenant)
	require.Equal(t, "skill", gotKind)
	require.Equal(t, "skill-1", gotResource)
	require.Equal(t, "member-1", gotEditor)
	p := repo.proposals[pid]
	require.Equal(t, domain.OpExecuted, p.Status)
	require.Equal(t, "admin-1", p.ReviewedBy)
	require.Equal(t, "granted", p.ReviewNote)
	require.Nil(t, p.ExpiresAt)
	require.Equal(t, []string{"grant_editor|approved"}, metrics.calls)

	t.Run("knowledge_doc grant dispatches to the doc whitelist", func(t *testing.T) {
		repo2 := newOperationProposalRepoFake()
		svc := newOperationProposalServiceForTest(repo2, roleResolverFake{role: "admin"}, nil)
		var dispatched string
		svc.WithGrantEditor(func(_ context.Context, _, resourceType, _, _ string) error {
			dispatched = resourceType
			return nil
		})
		require.NoError(t, svc.ProposeGrantEditor(ctx, "tenant-1", "member-9", "knowledge_doc", "doc-1", "docs/annual.pdf"))
		var id2 string
		for id := range repo2.proposals {
			id2 = id
		}
		require.NoError(t, svc.Approve(ctx, "tenant-1", "admin-1", id2, "ok"))
		require.Equal(t, "knowledge_doc", dispatched)
		require.Equal(t, domain.OpExecuted, repo2.proposals[id2].Status)
	})
}

func TestApproveGrantEditorWithoutGateFailsClosed(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
	// No WithGrantEditor — approval must fail without granting anything.
	require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "agent", "agent-1", ""))
	var pid string
	for id := range repo.proposals {
		pid = id
	}
	err := service.Approve(ctx, "tenant-1", "admin-1", pid, "")
	require.Error(t, err)
	require.Equal(t, domain.OpProposed, repo.proposals[pid].Status)
}

func TestApproveGrantEditorGrantFailureLeavesProposalPending(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
	service.WithGrantEditor(func(_ context.Context, _, _, _, _ string) error {
		return domain.ErrForbidden
	})
	require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "knowledge_doc", "doc-9", ""))
	var pid string
	for id := range repo.proposals {
		pid = id
	}
	err := service.Approve(ctx, "tenant-1", "admin-1", pid, "")
	require.Error(t, err)
	require.Equal(t, domain.OpProposed, repo.proposals[pid].Status)
}

// TestApproveGrantEditorResolvedProposalFailsClosed pins the state-precondition
// on grant_editor approval: once a proposal has been rejected (a terminal
// state) a later Approve must fail WITHOUT running the grant — the whitelist
// grant must never ride on a stale approval.
func TestApproveGrantEditorResolvedProposalFailsClosed(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
	granted := false
	service.WithGrantEditor(func(_ context.Context, _, _, _, _ string) error {
		granted = true
		return nil
	})
	require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "agent", "agent-1", ""))
	var pid string
	for id := range repo.proposals {
		pid = id
	}
	require.NoError(t, service.Reject(ctx, "tenant-1", "admin-1", pid, "denied"))

	err := service.Approve(ctx, "tenant-1", "admin-1", pid, "granted anyway")
	require.ErrorIs(t, err, domain.ErrOperationProposalResolved)
	require.False(t, granted, "grant must not run on a rejected proposal")
	require.Equal(t, domain.OpRejected, repo.proposals[pid].Status)
}

// TestOperationProposalListHistory covers role-scoped history: admin/owner sees
// the whole tenant, member sees only their own. History is the complement of
// pending (proposed/reviewing excluded), newest first.
func TestOperationProposalListHistory(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	repo.proposals["p1"] = domain.OperationProposal{ID: "p1", TenantID: "tenant-1", AgentID: "a", OpType: "self_modify", Status: domain.OpProposed, ProposerID: "member-1", CreatedAt: gateFixedNow}
	repo.proposals["p2"] = domain.OperationProposal{ID: "p2", TenantID: "tenant-1", AgentID: "a", OpType: "self_modify", Status: domain.OpRejected, ProposerID: "member-1", CreatedAt: gateFixedNow.Add(-time.Hour)}
	repo.proposals["p3"] = domain.OperationProposal{ID: "p3", TenantID: "tenant-1", AgentID: "a", OpType: "self_modify", Status: domain.OpExecuted, ProposerID: "member-2", CreatedAt: gateFixedNow.Add(-2 * time.Hour)}

	t.Run("admin sees the whole tenant history", func(t *testing.T) {
		service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
		rows, total, err := service.ListHistory(ctx, "tenant-1", "admin-1", 1, 20)
		require.NoError(t, err)
		require.Equal(t, 2, total) // p1 proposed 不属于历史
		require.Len(t, rows, 2)
		require.Equal(t, []string{"p2", "p3"}, []string{rows[0].ID, rows[1].ID})
	})

	t.Run("member sees only their own history", func(t *testing.T) {
		service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)
		rows, total, err := service.ListHistory(ctx, "tenant-1", "member-1", 1, 20)
		require.NoError(t, err)
		require.Equal(t, 1, total)
		require.Len(t, rows, 1)
		require.Equal(t, "p2", rows[0].ID)
	})

	t.Run("nil resolver fails closed", func(t *testing.T) {
		service := newOperationProposalServiceForTest(repo, nil, nil)
		_, _, err := service.ListHistory(ctx, "tenant-1", "admin-1", 1, 20)
		require.ErrorIs(t, err, domain.ErrProposalForbidden)
	})
}

// TestOperationProposalCancel covers the withdraw path: proposer self-cancel and
// admin/owner proxy-cancel, with reason recorded on the audit trail.
func TestOperationProposalCancel(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	seededProposal(t, repo) // p1 proposed, proposer member-1
	metrics := &gateMetricsFake{}
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, metrics)

	require.NoError(t, service.Cancel(ctx, "tenant-1", "member-1", "p1"))
	p := repo.proposals["p1"]
	require.Equal(t, domain.OpCancelled, p.Status)
	require.Equal(t, "member-1", p.ReviewedBy)
	require.Equal(t, "cancelled_by_initiator", p.ReviewNote)
	require.NotNil(t, p.ResolvedAt)
	require.Equal(t, gateFixedNow, *p.ResolvedAt)
	require.Equal(t, []string{"self_modify|cancelled"}, metrics.calls)
}

func TestOperationProposalCancelAuthorization(t *testing.T) {
	ctx := context.Background()

	t.Run("member cannot cancel another member's proposal", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		seededProposal(t, repo) // proposer member-1
		service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)
		err := service.Cancel(ctx, "tenant-1", "member-2", "p1")
		require.ErrorIs(t, err, domain.ErrProposalForbidden)
		require.Equal(t, domain.OpProposed, repo.proposals["p1"].Status)
	})

	t.Run("admin cancels on behalf of a member", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		seededProposal(t, repo)
		service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
		require.NoError(t, service.Cancel(ctx, "tenant-1", "admin-1", "p1"))
		p := repo.proposals["p1"]
		require.Equal(t, domain.OpCancelled, p.Status)
		require.Equal(t, "cancelled_by_approver", p.ReviewNote)
	})

	t.Run("resolved proposal cannot be cancelled", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		p := seededProposal(t, repo)
		p.Status = domain.OpApproved
		repo.proposals[p.ID] = p
		service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)
		err := service.Cancel(ctx, "tenant-1", "admin-1", "p1")
		require.ErrorIs(t, err, domain.ErrOperationProposalResolved)
	})

	t.Run("nil resolver fails closed", func(t *testing.T) {
		repo := newOperationProposalRepoFake()
		seededProposal(t, repo)
		service := newOperationProposalServiceForTest(repo, nil, nil)
		err := service.Cancel(ctx, "tenant-1", "member-1", "p1")
		require.ErrorIs(t, err, domain.ErrProposalForbidden)
	})
}

// TestProposeGrantEditorAllResourceTypes pins the six-kind resourceType
// whitelist on ProposeGrantEditor (agent / skill / knowledge_doc / mcp /
// knowledge_workspace / workflow); unknown kinds must fail closed.
func TestProposeGrantEditorAllResourceTypes(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "member"}, nil)

	for _, rt := range []string{"agent", "skill", "knowledge_doc", "mcp", "knowledge_workspace", "workflow"} {
		t.Run(rt, func(t *testing.T) {
			err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", rt, "res-1", "Res Name")
			require.NoError(t, err)
			found := false
			for _, p := range repo.proposals {
				if p.OpType == string(port.OpGrantEditor) && p.ProposerID == "member-1" && p.Fingerprint == "grant_editor|"+rt+"|res-1|member-1" {
					found = true
				}
			}
			require.True(t, found, "expected grant_editor proposal for %s", rt)
		})
	}

	// 非法类型 fail-closed。
	err := service.ProposeGrantEditor(ctx, "tenant-1", "member-1", "bogus", "res-1", "Res")
	require.ErrorIs(t, err, domain.ErrProposalInvalid)
}

// TestApproveGrantEditorForNewKinds locks the grantEditor dispatch contract for
// the three kinds added by the unified-resource-whitelist work: approval of an
// mcp / knowledge_workspace / workflow proposal must dispatch the grant with
// (resourceType, resourceID, editorID) intact.
func TestApproveGrantEditorForNewKinds(t *testing.T) {
	ctx := context.Background()
	repo := newOperationProposalRepoFake()
	service := newOperationProposalServiceForTest(repo, roleResolverFake{role: "admin"}, nil)

	var got []struct{ rt, rid, eid string }
	service.WithGrantEditor(func(_ context.Context, _, rt, rid, eid string) error {
		got = append(got, struct{ rt, rid, eid string }{rt, rid, eid})
		return nil
	})

	for _, rt := range []string{"mcp", "knowledge_workspace", "workflow"} {
		require.NoError(t, service.ProposeGrantEditor(ctx, "tenant-1", "member-1", rt, "res-"+rt, "Res"))
	}
	// 逐一审批 3 个提案，断言 grantEditor 收到正确分发参数。
	var pids []string
	for id := range repo.proposals {
		pids = append(pids, id)
	}
	require.Len(t, pids, 3)
	for _, pid := range pids {
		require.NoError(t, service.Approve(ctx, "tenant-1", "admin-1", pid, "granted"))
	}
	require.ElementsMatch(t, got, []struct{ rt, rid, eid string }{
		{"mcp", "res-mcp", "member-1"},
		{"knowledge_workspace", "res-knowledge_workspace", "member-1"},
		{"workflow", "res-workflow", "member-1"},
	})
}
