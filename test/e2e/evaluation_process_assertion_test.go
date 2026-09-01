package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// deterministicEvaluationAdapter 返回固定输出与工具序列，供 §6.5 过程断言确定性触发。
// ExecuteRevision 是评测 runCase 唯一调用的端口方法；ResolveRevision/SafeSummary 返回
// 最小合法值以满足接口（基线/修订服务才使用，e2e 不触及）。
type deterministicEvaluationAdapter struct {
	output any
	tools  []domain.ToolObservation
}

func (a *deterministicEvaluationAdapter) ExecuteRevision(
	_ context.Context, _, _ string, _ domain.ResourceRef, _ domain.EvalCase,
) (port.ExecutionResult, error) {
	return port.ExecutionResult{Output: a.output, Tools: a.tools}, nil
}

func (a *deterministicEvaluationAdapter) ResolveRevision(
	_ context.Context, _ string, ref domain.ResourceRef,
) (domain.ResourceRevision, error) {
	return domain.ResourceRevision{
		ResourceKind: ref.Kind, ResourceID: ref.ResourceID, Status: domain.RevisionStatusPublished,
	}, nil
}

func (a *deterministicEvaluationAdapter) SafeSummary(_ context.Context, _ string, _ domain.ResourceRef) (map[string]any, error) {
	return map[string]any{}, nil
}

// evaluationPostgresURL 解析评测 e2e 的 Postgres DSN（无 DSN 时按 harness 约定
// 跳过；REQUIRE_EVALUATION_E2E=1 时 fail closed）。
func evaluationPostgresURL(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("STRATUM_TEST_POSTGRES_URL"); value != "" {
		return value
	}
	if value := os.Getenv("TEST_POSTGRES_URL"); value != "" {
		return value
	}
	if os.Getenv("REQUIRE_EVALUATION_E2E") == "1" {
		t.Fatal("evaluation process assertion E2E requires STRATUM_TEST_POSTGRES_URL or TEST_POSTGRES_URL")
	}
	if os.Getenv("CI") != "" {
		t.Skip("evaluation process assertion E2E runs in the dedicated evaluation E2E job")
	}
	return "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
}

func setupEvaluationPostgres(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, evaluationPostgresURL(t))
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx), "PostgreSQL is required for evaluation process assertion E2E")
	require.NoError(t, pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := uuid.NewString()
	require.NoError(t, pgstorage.ProvisionTenantSchema(ctx, pool, tenantID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID))
		pool.Close()
	})
	return pool, tenantID
}

// TestEvaluationProcessAssertionMustNotCallEscalatesToReviewPool 是 §6.5 R3 e2e 场景：
// agent 评测含 tool_spec 用例（must_not_call=["delete"]），执行命中禁用工具 →
// 输出断言通过但过程断言失败（process:must_not_call:delete）→ run 失败 + 入评审池
// （process_output_conflict）。
func TestEvaluationProcessAssertionMustNotCallEscalatesToReviewPool(t *testing.T) {
	pool, tenantID := setupEvaluationPostgres(t)
	ctx := context.Background()
	userID := uuid.NewString()

	adapter := &deterministicEvaluationAdapter{
		output: "已解决",
		tools:  []domain.ToolObservation{{ToolName: "delete", StepIndex: 0}},
	}
	suiteRepo := persistence.NewPgSuiteRepository(pool)
	runRepo := persistence.NewPgRunRepository(pool)
	reviewRepo := persistence.NewPgReviewRepository(pool)
	reviewSvc := application.NewReviewService(application.ReviewServiceDeps{
		Repo:   reviewRepo,
		Suites: suiteRepo,
		Logger: zap.NewNop(),
		Cfg:    domain.ReviewConfig{LowConfidenceThreshold: 0.5, JudgePassThreshold: 0.7},
	})
	service := application.NewService(adapter, runRepo, nil, nil, suiteRepo)
	service.SetReviewEscalator(reviewSvc, domain.ReviewConfig{LowConfidenceThreshold: 0.5, JudgePassThreshold: 0.7})
	service.SetObservability(zap.NewNop(), nil)

	// 建套件（draft revision 携带 tool_spec）并发布。
	suiteID, revisionID, caseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	suite := domain.EvalSuite{ID: suiteID, Name: "§6.5 过程断言", DraftRevisionID: revisionID}
	revision := domain.EvalSuiteRevision{
		ID: revisionID, SuiteID: suiteID, Status: domain.SuiteRevisionDraft,
		ResourceKind: domain.ResourceKindAgent,
		Cases: []domain.EvalCase{{
			ID: caseID, Name: "禁用删除工具", Input: "清理数据", ExpectedOutput: "已解决",
			AssertionMode: domain.AssertionExact, Enabled: true,
			ToolSpec: &domain.ToolSpec{MustNotCall: []string{"delete"}},
		}},
	}
	require.NoError(t, suiteRepo.CreateSuite(ctx, tenantID, suite, revision))
	published, err := suiteRepo.PublishRevision(ctx, tenantID, suiteID, revisionID, 1)
	require.NoError(t, err)
	require.Len(t, published.Cases, 1)
	require.NotNil(t, published.Cases[0].ToolSpec, "tool_spec must survive publish round-trip")

	ref := domain.ResourceRef{Kind: domain.ResourceKindAgent, ResourceID: "agent-e2e", RevisionID: revisionID}
	run, err := service.Run(ctx, application.RunInput{
		TenantID: tenantID, RequestedBy: userID, Resource: ref, Suite: published,
	})
	require.NoError(t, err)

	// 过程断言归因：输出断言通过（exact 命中）但过程断言失败。
	require.False(t, run.Passed, "run must fail when a process assertion fails")
	require.Equal(t, 1, run.TotalCases)
	require.Zero(t, run.PassedCases)
	require.Len(t, run.Results, 1)
	got := run.Results[0]
	require.Equal(t, "已解决", got.Actual)
	require.False(t, got.ProcessPass, "must_not_call=delete hit should fail the process assertion")
	require.Contains(t, got.ProcessFailure, "process:must_not_call:delete")
	require.False(t, got.Passed, "output pass AND process fail must not pass the case")
	require.Empty(t, got.FailureReason, "no output assertion failure: process attribution is separate")

	// 评审池：输出通过 + 过程失败 → process_output_conflict 条目（§6.6）。
	items, total, err := reviewSvc.List(ctx, tenantID, port.ReviewFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "exactly one review item expected")
	require.Len(t, items, 1)
	item := items[0]
	require.Equal(t, domain.ReviewSourceCaseResult, item.SourceType)
	require.Equal(t, domain.TriggerProcessOutputConflict, item.TriggerReason)
	require.Equal(t, domain.ReviewStatusPending, item.Status)
	require.Equal(t, ref.Kind, item.ResourceKind)
	require.Equal(t, ref.ResourceID, item.ResourceID)
	require.Equal(t, run.ID, item.RunID)

	snap, ok := item.Snapshot.(map[string]any)
	require.True(t, ok, "snapshot must be a map: %T", item.Snapshot)
	processPass, ok := snap["process_pass"].(bool)
	require.True(t, ok, "snapshot process_pass must be a bool")
	require.False(t, processPass)
	require.Equal(t, "process:must_not_call:delete", snap["process_failure"])
	// tool_sequence 经 JSONB 落库后读回为 []any of map[string]any（与 review 详情
	// API 消费形状一致），逐字段断言工具序列归因。
	tools, ok := snap["tool_sequence"].([]any)
	require.True(t, ok, "snapshot tool_sequence must decode as []any: %T", snap["tool_sequence"])
	require.Len(t, tools, 1)
	first, ok := tools[0].(map[string]any)
	require.True(t, ok, "snapshot tool_sequence[0] must be a map: %T", tools[0])
	require.Equal(t, "delete", first["tool_name"])
	require.EqualValues(t, 0, first["step_index"])
}
