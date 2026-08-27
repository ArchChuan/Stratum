package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
)

// ── 场景 3 补强：C2d digest 语义 ────────────────────────────────────────────

// C2d：guard 比较用 canonical digest，容忍 int/float 表示差异。payload 存
// int(2)，运行时重新生成的参数经 JSON 解析为 float64(2)——json.Marshal 两者
// 都编码为 "2"，digest 相同 → 仍注入 approvalID 走 ExecuteApproved。
func TestBuildApprovalResumeOptions_GuardDigestToleratesIntFloat(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	payload.Arguments = map[string]any{"quantity": int(2)}
	d, err := CanonicalToolArgumentsDigest(payload.Arguments)
	require.NoError(t, err)
	payload.ArgumentsDigest = d
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, singleApprovalEntry(payload, false))
	require.NoError(t, err)
	_, fn := applyToolOptions(t, options)

	req := matchingToolRequest(payload)
	req.Arguments = map[string]any{"quantity": float64(2)} // 运行时表示差异

	out, err := fn(context.Background(), req)

	require.NoError(t, err)
	require.True(t, consumed(), "int/float 差异不应阻止 digest 命中")
	require.Equal(t, 1, repo.claimed, "必须经 ExecuteApproved CAS 消费")
	require.NotNil(t, out)
}

// C2d 精确化：consumed 置位后（决定已被 ExecuteApproved 内部 ClaimExecution CAS
// 原子消费），同参再次调用不得再次注入 ApprovalID——否则同轮内同一工具第二次
// 调用会重复消费/重复执行。第二次走正常授权路径：该工具未配置 policy
// （policy_resolved=false）→ destructive 仍 require_approval →
// ToolApprovalRequiredError，repo.claimed 保持 1（决定只消费一次）。
func TestBuildApprovalResumeOptions_GuardNoReinjectAfterConsumed(t *testing.T) {
	payload := resumePayload("e1", "a1", "user-1")
	svc, repo := resumeOptionService(t, payload)
	agent := resumeOptionAgent{config: &domain.AgentConfig{MCPToolIDs: []string{"mcp:srv:delete"}}}

	options, consumed, err := svc.buildApprovalResumeOptions(context.Background(), "t1", agent, singleApprovalEntry(payload, false))
	require.NoError(t, err)
	_, fn := applyToolOptions(t, options)

	req := matchingToolRequest(payload)

	out, err := fn(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, consumed())
	require.Equal(t, 1, repo.claimed)

	// 第二次同参：consumed 后不注入 ApprovalID → 走正常授权路径。工具未配置
	// policy（未放行）→ 仍需审批。
	req.Tool.Metadata["policy_resolved"] = false
	_, err = fn(context.Background(), req)

	var approvalErr *port.ToolApprovalRequiredError
	require.ErrorAs(t, err, &approvalErr, "consumed 后同参调用不得复用已消费批准")
	require.Equal(t, 1, repo.claimed, "第二次调用不得再次 ClaimExecution")
}

// ── 场景 5：方案 C 作废旧 pending（service 层调用逻辑） ───────────────────

// 方案 C：mcp_tool + 非空 executionID 的 Request 必须先作废旧 pending 再创建。
func TestToolApprovalRequestInvalidatesStaleBeforeCreate(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	id, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "1"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, repo.invalidateStaleCalls, "mcp_tool + 非空 executionID 必须先作废旧 pending")
}

// 非 mcp_tool subject（评测动作等）：无 agent 执行恢复语义，作废跳过。
func TestToolApprovalRequestSkipsStaleInvalidateNonMCP(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "1"}, SubjectKind: domain.SubjectKindEvaluationAction,
	})
	require.NoError(t, err)
	require.Zero(t, repo.invalidateStaleCalls, "非 mcp_tool 不作废旧 pending")
}

// executionID 空（非执行内审批）：作废无定位目标，跳过。
func TestToolApprovalRequestSkipsStaleInvalidateEmptyExecID(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "t1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "1"},
	})
	require.NoError(t, err)
	require.Zero(t, repo.invalidateStaleCalls, "无 executionID 不作废旧 pending")
}

// 作废失败 best-effort：不阻断新审批创建（旧 pending 由过期清理兜底）。
func TestToolApprovalRequestContinuesOnStaleInvalidateError(t *testing.T) {
	repo := &approvalRepoFake{invalidateStaleErr: errors.New("db down")}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	id, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "1"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id, "作废失败不得阻断新审批")
	require.Equal(t, 1, repo.invalidateStaleCalls)
}

// ── 场景 6：F2 配额隔离 ───────────────────────────────────────────────────

// F2 配额隔离：enforcePendingQuota 只计 pending（ListPending），approved 待执行
// 行（ListActionable 的查询范围）不得占用 MaxPendingApprovalsPerActor 配额。
// 断言 Request 的配额判定只调 ListPending，从不调 ListActionable——防止将来误
// 把含 approved 的列表接入配额导致生产级审批创建被拒。
func TestToolApprovalRequestQuotaIgnoresActionable(t *testing.T) {
	repo := &approvalRepoFake{pendingN: constants.MaxPendingApprovalsPerActor - 1}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	id, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "t1", ExecutionID: "e1", AgentID: "a1", UserID: "u1",
		ToolCallID: "tc1", ServerID: "srv", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "1"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, repo.listPendingCalls, "配额计数必须用 ListPending")
	require.Zero(t, repo.listActionableCalls, "approved 待执行行不得计入 pending 配额")
}
