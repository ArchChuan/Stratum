package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestTaskLifecycleRealPostgres(t *testing.T) {
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
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_task_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	repo := NewPgTaskRepo(pool)
	conv := "11111111-1111-1111-1111-111111111111"
	task := domain.Task{
		ID: "plan-1", AgentID: "agent-1", UserID: "user-1", Goal: "迁移订单服务",
		CurrentPhase: "1/2 完成", CompletedSteps: []string{"n1"}, NextAction: "验证迁移",
		Status: domain.TaskStatusActive, ClaimedBy: conv,
		LeaseExpiresAt: time.Now().Add(time.Hour), LastConversationID: conv,
		LastExecutionID: "exec-1", FailCount: 0, Generation: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	// 新建：Save expectedGeneration=0
	if err := repo.Save(ctx, tenantID, task, 0); err != nil {
		t.Fatalf("save new: %v", err)
	}

	// GetLatestActiveForOwner
	latest, err := repo.GetLatestActiveForOwner(ctx, tenantID, "agent-1", "user-1")
	if err != nil {
		t.Fatalf("get latest active: %v", err)
	}
	if latest == nil || latest.ID != "plan-1" || latest.NextAction != "验证迁移" {
		t.Fatalf("latest mismatch: %+v", latest)
	}

	// 原持有会话(conv)的 lease 必须过期，另一会话(otherConv)才能合法接管；
	// 活跃 lease 被其他会话占用时 Claim 必须失败（下方单独断言）。
	if _, err := pool.Exec(context.Background(),
		`UPDATE tenant_`+tenantID+`.agent_tasks
		   SET lease_expires_at = NOW() - INTERVAL '1 minute' WHERE id='plan-1'`); err != nil {
		t.Fatal(err)
	}

	// Claim 由另一会话接管 → generation bump
	otherConv := "22222222-2222-2222-2222-222222222222"
	claimed, ok, err := repo.Claim(ctx, tenantID, "plan-1", otherConv, 30*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim should succeed: ok=%v err=%v", ok, err)
	}
	if claimed.Generation != 1 || claimed.ClaimedBy != otherConv {
		t.Fatalf("claim bump failed: gen=%d claimed_by=%s", claimed.Generation, claimed.ClaimedBy)
	}

	// stale 写被拒：旧 generation=0 不再匹配
	stale := task
	stale.Generation = 0
	if err := repo.Save(ctx, tenantID, stale, 0); !errors.Is(err, domain.ErrGenerationConflict) {
		t.Fatalf("stale save should conflict, got %v", err)
	}

	// 新会话 Save 用 claim 后 generation 成功；last_conversation_id 必须同步为
	// 新会话，后续 DetachConversation(otherConv) 才能命中该 task。
	task.Generation = claimed.Generation
	task.ClaimedBy = otherConv
	task.LastConversationID = otherConv
	task.NextAction = "完成"
	if err := repo.Save(ctx, tenantID, task, claimed.Generation); err != nil {
		t.Fatalf("save with fresh generation: %v", err)
	}

	// Claim 被活跃会话占用 → 失败（不接管活跃 lease）
	if _, ok, err := repo.Claim(ctx, tenantID, "plan-1", conv, 30*time.Minute); ok {
		t.Fatal("claim by idle conversation on live lease must fail")
	} else if err != nil {
		t.Fatalf("claim conflict err: %v", err)
	}

	// DetachConversation：会话删除解除引用，task 保留
	if err := repo.DetachConversation(ctx, tenantID, otherConv); err != nil {
		t.Fatalf("detach: %v", err)
	}
	after, err := repo.Get(ctx, tenantID, "plan-1")
	if err != nil || after == nil {
		t.Fatalf("get after detach: %v", err)
	}
	if after.ClaimedBy != "" || after.LeaseExpiresAt != (time.Time{}) {
		t.Fatalf("detach should clear claim: claimed_by=%q lease=%s", after.ClaimedBy, after.LeaseExpiresAt)
	}
	if after.Status != domain.TaskStatusActive {
		t.Fatalf("detach must keep task active, got %s", after.Status)
	}

	// Claim 过期接管：lease 过期后可被新会话接管
	if err := repo.Save(ctx, tenantID, *after, after.Generation); err != nil {
		t.Fatalf("re-save detached: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE tenant_`+tenantID+`.agent_tasks
		   SET lease_expires_at = NOW() - INTERVAL '1 minute' WHERE id='plan-1'`); err != nil {
		t.Fatal(err)
	}
	claimed2, ok, err := repo.Claim(ctx, tenantID, "plan-1", conv, 30*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim expired lease should succeed: ok=%v err=%v", ok, err)
	}

	// DeleteExpired：claim 已把 generation bump 到 claimed2.Generation，
	// pre-cleanup save 必须携带该 fresh generation，否则 stale 写被拒。
	task.Generation = claimed2.Generation
	if err := repo.Save(ctx, tenantID, task, task.Generation); err != nil {
		t.Fatalf("pre-cleanup save: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE tenant_`+tenantID+`.agent_tasks SET expires_at = NOW() - INTERVAL '1 second' WHERE id='plan-1'`); err != nil {
		t.Fatal(err)
	}
	if deleted, err := repo.DeleteExpired(ctx, tenantID); err != nil || deleted < 1 {
		t.Fatalf("delete expired: deleted=%d err=%v", deleted, err)
	}
	if got, err := repo.Get(ctx, tenantID, "plan-1"); err != nil || got != nil {
		t.Fatalf("task should be reclaimed: got=%+v err=%v", got, err)
	}
}
