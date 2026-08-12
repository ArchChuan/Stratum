package wiring

import (
	"context"
	"errors"
	"testing"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/stretchr/testify/require"
)

// notExecutedError 断言 err 是 ApprovalActionNotExecutedError（预执行失败 → 审批释放回
// approved 可重试，而非烧成 unknown_outcome）。
func notExecutedError(t *testing.T, err error) {
	t.Helper()
	var notExec *port.ApprovalActionNotExecutedError
	require.ErrorAs(t, err, &notExec, "expected ApprovalActionNotExecutedError, got %v", err)
}

func TestApprovalActionExecutorSubjectDispatch(t *testing.T) {
	e := &approvalActionExecutor{}
	ctx := context.Background()

	t.Run("evaluation subject not configured fails closed", func(t *testing.T) {
		_, err := e.ExecuteApprovalAction(ctx, port.ApprovalActionRequest{
			TenantID: "t1", SubjectKind: agentdomain.SubjectKindEvaluationAction,
			Arguments: map[string]any{"operation": "pause_experiment"},
		})
		notExecutedError(t, err)
	})

	t.Run("mcp config subjects degrade to not-executed", func(t *testing.T) {
		for _, kind := range []string{agentdomain.SubjectKindMCPPolicy, agentdomain.SubjectKindMCPServer} {
			_, err := e.ExecuteApprovalAction(ctx, port.ApprovalActionRequest{
				TenantID: "t1", SubjectKind: kind, Arguments: map[string]any{},
			})
			notExecutedError(t, err)
		}
	})

	t.Run("unknown subject fails closed", func(t *testing.T) {
		_, err := e.ExecuteApprovalAction(ctx, port.ApprovalActionRequest{
			TenantID: "t1", SubjectKind: "unknown_kind", Arguments: map[string]any{},
		})
		notExecutedError(t, err)
	})
}

// TestEvaluationOperationsComplete 防 dispatch table 遗漏：11 个写 op 必须全部注册，
// 否则审批执行与直接执行路径分叉（D4 核心不变量）。
func TestEvaluationOperationsComplete(t *testing.T) {
	ops := []string{
		"create_suite", "publish_suite", "generate_suite_cases", "enqueue_run",
		"create_experiment", "pause_experiment", "promote_experiment", "rollback_experiment",
		"reject_candidate", "create_baseline", "generate_optimization",
	}
	require.Len(t, evaluationOperations, len(ops), "dispatch table must cover exactly the 11 evaluation write operations")
	for _, op := range ops {
		require.NotNil(t, evaluationOperations[op], "operation %q missing from dispatch table", op)
	}
}

func TestExecuteEvaluationUnsupportedOperation(t *testing.T) {
	e := &approvalActionExecutor{}
	_, err := e.executeEvaluation(context.Background(), port.ApprovalActionRequest{
		TenantID: "t1", SubjectKind: agentdomain.SubjectKindEvaluationAction,
		Arguments: map[string]any{"operation": "rm -rf"},
	})
	notExecutedError(t, err)
}

func TestNotExecutedWrapsCause(t *testing.T) {
	err := notExecuted(errors.New("provider down"))
	notExecutedError(t, err)
	require.Contains(t, err.Error(), "provider down")
}

func TestApprovalActionArgHelpers(t *testing.T) {
	args := map[string]any{
		"name":     "suite-a",
		"maxCases": float64(42),
		"tags":     []any{"a", "b"},
		"enabled":  true,
	}
	require.Equal(t, "suite-a", asString(args, "name"))
	require.Empty(t, asString(args, "missing"))
	require.Equal(t, 42, asInt(args, "maxCases"))
	require.Equal(t, 0, asInt(args, "missing"))
	require.Equal(t, []string{"a", "b"}, asStringSlice(args, "tags"))
	require.True(t, asBool(args, "enabled"))
	require.False(t, asBool(args, "missing"))
	// 缺失 key 返回 nil（而非空 slice）：OptimizationFingerprint 序列化 nil 为
	// null、[] 为空数组，跨路径必须一致，否则幂等键分叉。
	require.Nil(t, asStringSlice(args, "missing"))
}

func TestApprovalActionResourceRefHelper(t *testing.T) {
	args := map[string]any{"resource": map[string]any{
		"kind": "agent", "id": "agent-1", "revision_id": "rev-1",
	}}
	ref := asResourceRef(args, "resource")
	require.Equal(t, evaldomain.ResourceKind("agent"), ref.Kind)
	require.Equal(t, "agent-1", ref.ResourceID)
	require.Equal(t, "rev-1", ref.RevisionID)
	// 缺失字段返回零值，不 panic。
	require.Equal(t, evaldomain.ResourceRef{}, asResourceRef(map[string]any{}, "missing"))
}

func TestApprovalActionEvalCasesHelper(t *testing.T) {
	args := map[string]any{"cases": []any{
		map[string]any{"name": "c1", "input": "x", "expected_output": "y", "assertion_mode": "exact", "enabled": true},
		map[string]any{"name": "c2"}, // 仅必填字段
		nil,                          // 异常项跳过
	}}
	cases := asEvalCases(args, "cases")
	require.Len(t, cases, 2)
	require.Equal(t, "c1", cases[0].Name)
	require.Equal(t, evaldomain.AssertionMode("exact"), cases[0].AssertionMode)
	require.True(t, cases[0].Enabled)
	require.Equal(t, "c2", cases[1].Name)
}

func TestApprovalActionSearchSpaceHelper(t *testing.T) {
	// JSONB 反序列化形态：map[string]any + []any。
	jsonb := map[string]any{"searchSpace": map[string]any{
		"temperature": []any{float64(0.1), float64(0.7)},
	}}
	space := asSearchSpace(jsonb, "searchSpace")
	require.Len(t, space["temperature"], 2)

	// 内存直传形态：map[string][]any。
	direct := map[string]any{"searchSpace": map[string][]any{"topK": {float64(3)}}}
	require.Len(t, asSearchSpace(direct, "searchSpace")["topK"], 1)

	// 缺失返回空 map，不 panic。
	require.Empty(t, asSearchSpace(map[string]any{}, "missing"))
}

func TestApprovalActionExperimentCommandHelper(t *testing.T) {
	req := port.ApprovalActionRequest{
		DecidedBy: "user-admin",
		Arguments: map[string]any{
			"reason": "reviewed", "idempotencyKey": "idem-1",
			"expectedStateVersion": float64(3),
		},
	}
	cmd := asExperimentCommand(req, "reason")
	require.Equal(t, "user-admin", cmd.ActorID)
	require.Equal(t, "reviewed", cmd.Reason)
	require.Equal(t, "idem-1", cmd.IdempotencyKey)
	require.Equal(t, int64(3), cmd.ExpectedStateVersion)
}
