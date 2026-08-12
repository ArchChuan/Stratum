package persistence_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type countingMCPExecutor struct{ calls int }

func (e *countingMCPExecutor) ExecuteMCPTool(context.Context, string, string, map[string]any) (port.MCPToolResult, error) {
	e.calls++
	return port.MCPToolResult{}, nil
}

type atomicCountingMCPExecutor struct{ calls atomic.Int32 }

func (e *atomicCountingMCPExecutor) ExecuteMCPTool(context.Context, string, string, map[string]any) (port.MCPToolResult, error) {
	e.calls.Add(1)
	return port.MCPToolResult{}, nil
}

type unknownOutcomeMCPExecutor struct{ err error }

func (e unknownOutcomeMCPExecutor) ExecuteMCPTool(context.Context, string, string, map[string]any) (port.MCPToolResult, error) {
	return port.MCPToolResult{}, &port.MCPToolExecutionError{Outcome: port.ToolExecutionOutcomeUnknown, Err: e.err}
}

func TestToolApprovalEncryptedDecisionAndExactlyOnceExecution(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_approval_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "admin", Role: postgres.RoleTenantAdmin})
	approvals := persistence.NewPgToolApprovalStore(pool)
	checkpoints := persistence.NewPgCheckpointStore(pool)
	svc := agentapp.NewToolApprovalService(approvals, checkpoints, pkgcrypto.DeriveAESKey("integration-key"))
	payload := agentapp.ToolApprovalPayload{TenantID: tenantID, ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1", ConversationID: uuid.NewString(), ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive, Query: "delete", Arguments: map[string]any{"secret": "plain-secret"}}
	id, err := svc.Request(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted, status, subjectKind, convID string
	if err := pool.QueryRow(ctx,
		`SELECT encrypted_payload,status,subject_kind,conversation_id FROM "`+schema+`".agent_tool_approvals WHERE id=$1`,
		id).Scan(&encrypted, &status, &subjectKind, &convID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "plain-secret") || status != "pending" {
		t.Fatalf("payload was not safely persisted")
	}
	// 回归防护（review B1）：service.Create 必须显式落库 subject_kind/conversation_id，级联与泛化依赖它们。
	if subjectKind != domain.SubjectKindMCPTool || convID != payload.ConversationID {
		t.Fatalf("subject_kind=%q conversation_id=%q not persisted", subjectKind, convID)
	}
	if err := svc.Decide(ctx, tenantID, id, "approved", "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, tenantID, id, "approved", "admin", ""); err == nil {
		t.Fatal("duplicate decision succeeded")
	}
	executor := &countingMCPExecutor{}
	if _, err := svc.ExecuteApproved(ctx, tenantID, id, "orders", "delete", payload.Arguments, executor); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteApproved(ctx, tenantID, id, "orders", "delete", payload.Arguments, executor); err == nil {
		t.Fatal("duplicate execution succeeded")
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls=%d", executor.calls)
	}

	concurrentPayload := payload
	concurrentPayload.ExecutionID = "exec-concurrent"
	concurrentPayload.ToolCallID = "call-concurrent"
	concurrentID, err := svc.Request(ctx, concurrentPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, tenantID, concurrentID, "approved", "admin", ""); err != nil {
		t.Fatal(err)
	}
	concurrentExecutor := &atomicCountingMCPExecutor{}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, callErr := svc.ExecuteApproved(
				ctx, tenantID, concurrentID, "orders", "delete", concurrentPayload.Arguments, concurrentExecutor,
			)
			errs <- callErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for callErr := range errs {
		if callErr == nil {
			successes++
		}
	}
	if successes != 1 || concurrentExecutor.calls.Load() != 1 {
		t.Fatalf("concurrent execution successes=%d executor_calls=%d", successes, concurrentExecutor.calls.Load())
	}

	unknownPayload := payload
	unknownPayload.ExecutionID = "exec-unknown"
	unknownPayload.ToolCallID = "call-unknown"
	unknownID, err := svc.Request(ctx, unknownPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, tenantID, unknownID, "approved", "admin", ""); err != nil {
		t.Fatal(err)
	}
	dispatchErr := fmt.Errorf("response timeout")
	if _, err := svc.ExecuteApproved(
		ctx, tenantID, unknownID, "orders", "delete", unknownPayload.Arguments,
		unknownOutcomeMCPExecutor{err: dispatchErr},
	); err == nil {
		t.Fatal("unknown outcome execution unexpectedly succeeded")
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM "`+schema+`".agent_tool_approvals WHERE id=$1`, unknownID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unknown_outcome" {
		t.Fatalf("unknown outcome status=%q", status)
	}
	if err := approvals.ClaimExecution(ctx, tenantID, unknownID); err == nil {
		t.Fatal("unknown outcome approval was claimable")
	}
}

func TestToolApprovalInvalidateVoidAndCascade(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_approval_inval_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "admin", Role: postgres.RoleTenantAdmin})
	store := persistence.NewPgToolApprovalStore(pool)

	create := func(status, convID string) string {
		// Create 固定写入 pending（生产语义：service 只创建 pending）；非 pending 用例经显式 UPDATE 设置。
		id, err := store.Create(ctx, tenantID, domain.ToolApproval{
			DecisionID: uuid.NewString(), ExecutionID: uuid.NewString(), TraceID: "t", AgentID: "a",
			UserID: "user-1", ToolCallID: uuid.NewString(), ServerID: "srv", ToolName: "tool",
			RiskLevel: "unclassified", EncryptedPayload: "enc",
			ExpiresAt: time.Now().Add(time.Hour), SubjectKind: domain.SubjectKindMCPTool,
			ConversationID: convID,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if status != "pending" {
			if _, err := pool.Exec(ctx,
				`UPDATE "`+schema+`".agent_tool_approvals SET status=$2 WHERE id=$1`, id, status); err != nil {
				t.Fatalf("set status %s: %v", status, err)
			}
		}
		return id
	}
	convID := "conv-cascade-" + uuid.NewString()[:8]
	approvedID := create("approved", convID)
	pendingID := create("pending", convID)
	executedID := create("executed", convID)
	otherID := create("approved", "other-conv")

	if err := store.Invalidate(ctx, tenantID, approvedID, "policy_changed"); err != nil {
		t.Fatalf("invalidate approved: %v", err)
	}
	// CAS：invalidated 终态再 Invalidate 必须失败
	if err := store.Invalidate(ctx, tenantID, approvedID, "again"); err == nil {
		t.Fatal("expected invalidate on terminal to fail")
	}
	if err := store.Void(ctx, tenantID, approvedID, "conversation_deleted"); err == nil {
		t.Fatal("expected void on invalidated to fail")
	}
	if err := store.Void(ctx, tenantID, otherID, "conversation_deleted"); err != nil {
		t.Fatalf("void other: %v", err)
	}
	// fail closed（review major）：空 conversationID 必须拒绝，禁止批量命中未关联会话。
	if err := store.CascadeByConversation(ctx, tenantID, ""); err == nil {
		t.Fatal("expected empty conversation id cascade to fail")
	}
	// 级联：pending→cancelled、approved→voided；executed/其他会话不动
	if err := store.CascadeByConversation(ctx, tenantID, convID); err != nil {
		t.Fatalf("cascade: %v", err)
	}
	for _, tc := range []struct {
		id     string
		status string
	}{
		{approvedID, "invalidated"}, // 已被 Invalidate 先行抢占
		{pendingID, "cancelled"},
		{executedID, "executed"},
		{otherID, "voided"},
	} {
		var got string
		if err := pool.QueryRow(ctx, `SELECT status FROM "`+schema+`".agent_tool_approvals WHERE id=$1`, tc.id).Scan(&got); err != nil {
			t.Fatalf("query %s: %v", tc.id, err)
		}
		if got != tc.status {
			t.Fatalf("id %s: expected %s, got %s", tc.id, tc.status, got)
		}
	}
	var reason string
	if err := pool.QueryRow(ctx,
		`SELECT invalidation_reason FROM "`+schema+`".agent_tool_approvals WHERE id=$1`, otherID,
	).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "conversation_deleted" {
		t.Fatalf("expected invalidation_reason conversation_deleted, got %q", reason)
	}
}

func TestToolApprovalListHistoryPaged(t *testing.T) {
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
	tenantID := fmt.Sprintf("tmp_approval_hist_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "admin", Role: postgres.RoleTenantAdmin})
	store := persistence.NewPgToolApprovalStore(pool)
	for i := 0; i < 5; i++ {
		// Create 固定写入 pending；历史用例经显式 UPDATE 置为 executed。
		id, err := store.Create(ctx, tenantID, domain.ToolApproval{
			DecisionID: uuid.NewString(), ExecutionID: uuid.NewString(), TraceID: "t", AgentID: "a",
			UserID: "user-1", ToolCallID: uuid.NewString(), ServerID: "srv", ToolName: "tool",
			RiskLevel: "unclassified", EncryptedPayload: "enc",
			ExpiresAt: time.Now().Add(time.Hour), SubjectKind: domain.SubjectKindMCPTool,
			ConversationID: "c",
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE "`+schema+`".agent_tool_approvals SET status='executed' WHERE id=$1`, id); err != nil {
			t.Fatalf("set executed %d: %v", i, err)
		}
	}
	rows, total, err := store.ListHistory(ctx, tenantID, 1, 2)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(rows) != 2 {
		t.Fatalf("expected page size 2, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Status != "executed" {
			t.Fatalf("expected history rows executed, got %q", r.Status)
		}
	}
}
