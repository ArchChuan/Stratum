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
	var encrypted, status string
	if err := pool.QueryRow(ctx, `SELECT encrypted_payload,status FROM "`+schema+`".agent_tool_approvals WHERE id=$1`, id).Scan(&encrypted, &status); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "plain-secret") || status != "pending" {
		t.Fatalf("payload was not safely persisted")
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
