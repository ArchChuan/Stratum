package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// newLeaseTestStore 起一个隔离租户 schema 并写入一条 running checkpoint。
func newLeaseTestStore(t *testing.T, executionID string) (*PgCheckpointStore, string) {
	t.Helper()
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_lease_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	store := NewPgCheckpointStore(pool)
	if err := store.Upsert(ctx, tenantID, domain.AgentExecutionCheckpoint{
		ExecutionID: executionID, TraceID: "t", AgentID: "a", UserID: "u",
		Status: "running", ExpiresAt: time.Now().Add(time.Hour), RunGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return store, tenantID
}

func TestLeaseStampThenStatusActive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-stamp")
	ctx := context.Background()
	gen, err := store.StampLease(ctx, tenantID, "exec-stamp", time.Minute)
	if err != nil {
		t.Fatalf("StampLease: %v", err)
	}
	if gen != 1 {
		t.Fatalf("StampLease gen = %d, want 1 (stamp must not advance generation)", gen)
	}
	status, err := store.LeaseStatus(ctx, tenantID, "exec-stamp")
	if err != nil {
		t.Fatalf("LeaseStatus: %v", err)
	}
	if status.Generation != 1 || !status.Active {
		t.Fatalf("LeaseStatus = %+v, want {1 true}", status)
	}
}

func TestLeaseStampOnMissingCheckpointFails(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-present")
	// 断言领域哨兵而不是「有 error」：连接断开、search_path 未设、SQL 语法错
	// 都满足后者，会让这条用例绿着通过。
	_, err := store.StampLease(context.Background(), tenantID, "exec-absent", time.Minute)
	if !errors.Is(err, port.ErrCheckpointNotFound) {
		t.Fatalf("StampLease on missing checkpoint = %v, want ErrCheckpointNotFound", err)
	}
}

func TestLeaseClaimIsExclusive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-claim")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-claim", time.Minute); err != nil {
		t.Fatal(err)
	}
	gen, err := store.ClaimLease(ctx, tenantID, "exec-claim", 1, time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease: %v", err)
	}
	if gen != 2 {
		t.Fatalf("ClaimLease gen = %d, want 2", gen)
	}
	// 第二个 claimant 拿同样的 expect=1 必须失败——这就是唯一的互斥来源。
	if _, err := store.ClaimLease(ctx, tenantID, "exec-claim", 1, time.Minute); !errors.Is(err, port.ErrLeaseConflict) {
		t.Fatalf("second ClaimLease = %v, want ErrLeaseConflict", err)
	}
}

func TestLeaseRenewFencesStaleRunner(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-renew")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-renew", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, tenantID, "exec-renew", 1, time.Minute); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	// 抢占者把 generation 推到 2，僵尸 runner 的续租必须被 fence 掉。
	if _, err := store.ClaimLease(ctx, tenantID, "exec-renew", 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, tenantID, "exec-renew", 1, time.Minute); !errors.Is(err, port.ErrLeaseConflict) {
		t.Fatalf("stale RenewLease = %v, want ErrLeaseConflict", err)
	}
}

func TestLeaseReleaseClearsActive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-release")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-release", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseLease(ctx, tenantID, "exec-release", 1); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	status, err := store.LeaseStatus(ctx, tenantID, "exec-release")
	if err != nil {
		t.Fatal(err)
	}
	if status.Active {
		t.Fatalf("LeaseStatus.Active = true after release, want false")
	}
}

// provisionExtraTenant 再建一个隔离租户 schema，供跨租户可见性用例使用。
// 必须真的建表：否则查询报的是「表不存在」，用例就退化成在验证 schema 缺失。
func provisionExtraTenant(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	schema := "tenant_" + tenantID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	if err := postgres.ProvisionTenantSchema(context.Background(), pool, tenantID); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseStatusIsTenantScoped(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-scoped")
	pool := store.pool.(*pgxpool.Pool)
	other := tenantID + "_other"
	provisionExtraTenant(t, pool, other)
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-scoped", time.Minute); err != nil {
		t.Fatal(err)
	}
	// 阳性对照：异租户 schema 自身可用（同一路径写进去、读得出来）。缺了这一步，
	// 下面「查不到」也可能是「schema 根本没建 / search_path 没生效」，用例就退化
	// 成在验证 schema 缺失而不是租户隔离。
	if err := store.Upsert(ctx, other, domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-scoped-other", TraceID: "t", AgentID: "a", UserID: "u",
		Status: "running", ExpiresAt: time.Now().Add(time.Hour), RunGeneration: 1,
	}); err != nil {
		t.Fatalf("sanity: foreign tenant cannot write its own checkpoint: %v", err)
	}
	own, err := store.LeaseStatus(ctx, other, "exec-scoped-other")
	if err != nil {
		t.Fatalf("sanity: foreign tenant cannot read its own checkpoint: %v", err)
	}
	if own.Generation != 1 {
		t.Fatalf("sanity: foreign tenant generation = %d, want 1", own.Generation)
	}
	// 另一个租户读同一个 execution_id 必须报「行不存在」这一确定原因，而不是
	// 「有 error 就算通过」——那是跨租户隔离（风险红线第 3 条）的守门断言。
	if _, err := store.LeaseStatus(ctx, other, "exec-scoped"); !errors.Is(err, port.ErrCheckpointNotFound) {
		t.Fatalf("LeaseStatus(foreign tenant) = %v, want ErrCheckpointNotFound", err)
	}
}
