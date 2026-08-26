package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newOperationProposalIntegrationRepo(t *testing.T) (*PgOperationProposalRepo, context.Context, string, string) {
	t.Helper()
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pgstore.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := fmt.Sprintf("tmp_op_hist_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	require.NoError(t, pgstore.ProvisionTenantSchema(ctx, pool, tenantID))
	ctx = tenantdb.WithTenant(ctx, &tenantdb.TenantContext{TenantID: tenantID, UserID: "admin", Role: tenantdb.RoleTenantAdmin})
	return NewPgOperationProposalRepo(pool), ctx, tenantID, schema
}

func opProposalRow(tenantID, id, proposerID, status string) domain.OperationProposal {
	now := time.Now().UTC()
	return domain.OperationProposal{
		ID: id, TenantID: tenantID, AgentID: "agent-1", OpType: "self_modify",
		// schema 约束 delegation NOT NULL CHECK IN ('no_delegate','read_only','full')，
		// 与业务层构造一致默认 no_delegate。
		Delegation:  string(port.DelegationNone),
		Fingerprint: "fp-" + id, PayloadSummary: json.RawMessage(`{"name":"renamed"}`),
		Status: domain.OpProposalStatus(status), ProposerID: proposerID,
		CreatedAt: now, UpdatedAt: now,
	}
}

// TestOperationProposalListHistoryPaged 覆盖分页历史：admin（proposerID=""）看全租户，
// member 只看自己的；COUNT 与 SELECT 同步过滤保证 total 与列表一致。cancelled 终态
// 必须出现在历史里（非 pending）。
func TestOperationProposalListHistoryPaged(t *testing.T) {
	repo, ctx, tenantID, _ := newOperationProposalIntegrationRepo(t)

	// 7 行非 pending 历史（6 行 member-1 + 1 行 cancelled）+ 1 行 proposed（应被历史排除）。
	for i := range 5 {
		require.NoError(t, repo.Insert(ctx, opProposalRow(tenantID, fmt.Sprintf("m1-exec-%d", i), "member-1", "executed")))
	}
	require.NoError(t, repo.Insert(ctx, opProposalRow(tenantID, "m1-cancelled", "member-1", "cancelled")))
	for i := range 2 {
		require.NoError(t, repo.Insert(ctx, opProposalRow(tenantID, fmt.Sprintf("m2-exec-%d", i), "member-2", "executed")))
	}
	require.NoError(t, repo.Insert(ctx, opProposalRow(tenantID, "m1-pending", "member-1", "proposed")))

	// admin（proposerID=""）：全租户非 pending total=8（6+2，proposed 排除）。
	_, total, err := repo.ListHistory(ctx, tenantID, "", 1, 10)
	require.NoError(t, err)
	require.Equal(t, 8, total)

	// member（proposerID="member-1"）：total=6（5 executed + 1 cancelled）与列表一致，
	// 分页 2 条，全部属于本人且不含 pending。
	rows, total, err := repo.ListHistory(ctx, tenantID, "member-1", 1, 2)
	require.NoError(t, err)
	require.Equal(t, 6, total)
	require.Len(t, rows, 2)
	for _, r := range rows {
		require.Equal(t, "member-1", r.ProposerID)
		require.NotEqual(t, domain.OpProposed, r.Status)
		require.NotEqual(t, domain.OpReviewing, r.Status)
	}
}

// TestOperationProposalCancelStampsResolvedAt 覆盖 cancelled 终态落库：UpdateStatus
// 置 resolved_at（复现 repo SQL CASE 扩展），且终态后再次更新 CAS 折叠为 Resolved。
func TestOperationProposalCancelStampsResolvedAt(t *testing.T) {
	repo, ctx, tenantID, _ := newOperationProposalIntegrationRepo(t)

	require.NoError(t, repo.Insert(ctx, opProposalRow(tenantID, "cancel-me", "member-1", "proposed")))
	require.NoError(t, repo.UpdateStatus(ctx, tenantID, "cancel-me", domain.OpCancelled, "member-1", "cancelled_by_initiator"))

	stored, err := repo.GetByID(ctx, tenantID, "cancel-me")
	require.NoError(t, err)
	require.Equal(t, domain.OpCancelled, stored.Status)
	require.NotNil(t, stored.ResolvedAt, "cancelled 是终态，必须打 resolved_at")
	require.Equal(t, "cancelled_by_initiator", stored.ReviewNote)

	// CAS：终态行再更新 → 单赢家折叠为 Resolved，不覆盖已有决定。
	err = repo.UpdateStatus(ctx, tenantID, "cancel-me", domain.OpRejected, "admin-1", "nope")
	require.ErrorIs(t, err, domain.ErrOperationProposalResolved)
	final, err := repo.GetByID(ctx, tenantID, "cancel-me")
	require.NoError(t, err)
	require.Equal(t, domain.OpCancelled, final.Status)
}
