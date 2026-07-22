package persistence_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestToolPermissionHarnessIsolatesApprovalAcrossTenantSchemas(t *testing.T) {
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
	suffix := time.Now().UnixNano()
	tenantA, tenantB := fmt.Sprintf("tmp_permission_a_%d", suffix), fmt.Sprintf("tmp_permission_b_%d", suffix)
	for _, tenantID := range []string{tenantA, tenantB} {
		if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
			t.Fatal(err)
		}
		schema := "tenant_" + tenantID
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
		})
	}
	store := persistence.NewPgToolApprovalStore(pool)
	service := agentapp.NewToolApprovalService(store, nil, pkgcrypto.DeriveAESKey("permission-harness-key"))
	const sentinel = "approval-sensitive-sentinel"
	payload := agentapp.ToolApprovalPayload{
		TenantID: tenantA, ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"secret": sentinel},
	}
	id, err := service.Request(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Decide(ctx, tenantA, id, "approved", "admin-1", "reviewed"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.ApprovedPayload(ctx, tenantB, id); err == nil {
		t.Fatal("cross-tenant approval lookup unexpectedly succeeded")
	}
	var encrypted string
	if err := pool.QueryRow(ctx,
		`SELECT encrypted_payload FROM "tenant_`+tenantA+`".agent_tool_approvals WHERE id=$1`, id,
	).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, sentinel) {
		t.Fatal("approval plaintext leaked into tenant persistence")
	}
}
