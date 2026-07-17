//go:build integration

package graph_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentpersist "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPlanCheckpointPersistsExecutionIdentityInRealPostgres(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; this integration test must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := "checkpoint_" + uuid.NewString()[:8]
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DROP SCHEMA IF EXISTS "tenant_`+tenantID+`" CASCADE`) }()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	executionID, conversationID := uuid.NewString(), uuid.NewString()
	writer := agentpersist.NewPgCheckpointStore(pool)
	stub := &stuckThenPlan{stuckRounds: 1, plan: []domain.PlanStep{{Goal: "finish"}}, stepAnswers: []string{"done"}}
	compiled, err := graph.BuildPlanExecuteGraph(stub, graph.NoopTokenRecorder{}, writer, nil, zap.NewNop())
	require.NoError(t, err)
	_, err = compiled.Invoke(tenantCtx, graph.ReActState{
		TenantID: tenantID, TraceID: "trace-1", ExecutionID: executionID, ConversationID: conversationID,
		Model: "model", Messages: []port.LLMMessage{{Role: "user", Content: "task"}}, StuckThreshold: 1, MaxLLMSteps: 6,
	}, graph.RunConfig{MaxSteps: 20})
	require.NoError(t, err)
	checkpoint, err := writer.GetLatest(tenantCtx, tenantID, executionID)
	require.NoError(t, err)
	require.Equal(t, executionID, checkpoint.ExecutionID)
	require.Equal(t, "running", checkpoint.Status)
}
