package factcheck

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

// obs 构造一条最小 ToolObservation 记录（by ToolCallID 对账所需字段）。
func obs(id, status, outcome string) domain.ToolObservation {
	return domain.ToolObservation{ToolCallID: id, ToolName: "mcp:orders:delete", Status: status, Outcome: outcome}
}

// TestReconcileCitations_ReferenceMatrix 覆盖判定矩阵全分支：成功 → verified；
// definite_failure / not_sent → verification_failed；outcome_unknown / legacy 无
// outcome → outcome_unknown；无对应记录 → invalid_reference。
func TestReconcileCitations_ReferenceMatrix(t *testing.T) {
	observations := []domain.ToolObservation{
		obs("call-ok", domain.ToolTraceStatusSuccess, ""),
		obs("call-fail", domain.ToolTraceStatusError, string(port.ToolExecutionOutcomeDefiniteFailure)),
		obs("call-notsent", domain.ToolTraceStatusError, string(port.ToolExecutionOutcomeNotSent)),
		obs("call-unknown", domain.ToolTraceStatusError, string(port.ToolExecutionOutcomeUnknown)),
		obs("call-legacy", domain.ToolTraceStatusError, ""),
	}
	output := "已删除订单 <tool_ref:call-ok>。已创建订单 <tool_ref:call-fail>。" +
		"已更新订单 <tool_ref:call-notsent>。已修改订单 <tool_ref:call-unknown>。" +
		"已禁用订单 <tool_ref:call-legacy>。声称不存在的执行 <tool_ref:call-ghost>。"
	refs, unverified := reconcileCitations(output, observations)
	require.Empty(t, unverified)

	byID := make(map[string]domain.ToolReferenceVerdict, len(refs))
	for _, r := range refs {
		byID[r.ToolCallID] = r
	}
	require.Equal(t, ClassVerified, byID["call-ok"].Classification)
	require.Equal(t, 0, byID["call-ok"].Risk)
	require.Equal(t, ClassVerificationFailed, byID["call-fail"].Classification)
	require.Equal(t, 5, byID["call-fail"].Risk)
	require.Equal(t, ClassVerificationFailed, byID["call-notsent"].Classification)
	require.Equal(t, 5, byID["call-notsent"].Risk)
	require.Equal(t, ClassOutcomeUnknown, byID["call-unknown"].Classification)
	require.Equal(t, 2, byID["call-unknown"].Risk)
	require.Equal(t, ClassOutcomeUnknown, byID["call-legacy"].Classification)
	require.Equal(t, 2, byID["call-legacy"].Risk)
	require.Equal(t, ClassInvalidReference, byID["call-ghost"].Classification)
	require.Equal(t, 4, byID["call-ghost"].Risk)
}

// TestReconcileCitations_DedupesSameReference 验证同一 tool_call_id 被多次引用时
// 只对账一次（观察记录唯一，重复引用不重复入报告）。
func TestReconcileCitations_DedupesSameReference(t *testing.T) {
	observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
	output := "已删除订单 <tool_ref:call-1>。并已发送通知 <tool_ref:call-1>。"
	refs, _ := reconcileCitations(output, observations)
	require.Len(t, refs, 1, "同一 tool_call_id 只对账一次")
	require.Equal(t, "call-1", refs[0].ToolCallID)
	require.Equal(t, ClassVerified, refs[0].Classification)
}

// TestReconcileCitations_SupportsBracketForm 验证兼容标记 [tool:ID]。
func TestReconcileCitations_SupportsBracketForm(t *testing.T) {
	observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
	refs, _ := reconcileCitations("已删除订单 [tool:call-1]。", observations)
	require.Len(t, refs, 1)
	require.Equal(t, "call-1", refs[0].ToolCallID)
	require.Equal(t, ClassVerified, refs[0].Classification)
}

// TestReconcileCitations_UnverifiedHeuristic 覆盖无引用副作用声称的软标记：
// 命中 accomplishment 白名单 → unverified；中性/进行态陈述不误判；带已验证
// 引用的句子不再标记；上限 AgentFactCheckMaxUnverified 截断。
func TestReconcileCitations_UnverifiedHeuristic(t *testing.T) {
	t.Run("hit marks unverified", func(t *testing.T) {
		refs, unverified := reconcileCitations("订单已删除。", nil)
		require.Empty(t, refs)
		require.Equal(t, []string{"订单已删除。"}, unverified)
	})

	t.Run("neutral statement not flagged", func(t *testing.T) {
		_, unverified := reconcileCitations("订单正在处理中。", nil)
		require.Empty(t, unverified)
	})

	t.Run("english marker", func(t *testing.T) {
		_, unverified := reconcileCitations("The order was deleted.", nil)
		require.Equal(t, []string{"The order was deleted."}, unverified)
	})

	t.Run("verified reference suppresses unverified", func(t *testing.T) {
		observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
		_, unverified := reconcileCitations("订单已删除 <tool_ref:call-1>。", observations)
		require.Empty(t, unverified)
	})

	t.Run("capped at max", func(t *testing.T) {
		_, unverified := reconcileCitations("已创建。已删除。已更新。已发送。已启用。已禁用。", nil)
		require.Len(t, unverified, 5)
	})
}

// TestReconcileCitations_NoObservationsMeansNoVerification 验证无观察记录时，
// 任何引用都无法对账 → invalid_reference（声称无对应真实执行）。
func TestReconcileCitations_NoObservationsMeansNoVerification(t *testing.T) {
	refs, _ := reconcileCitations("已删除订单 <tool_ref:call-1>。", nil)
	require.Len(t, refs, 1)
	require.Equal(t, ClassInvalidReference, refs[0].Classification)
	require.Equal(t, 4, refs[0].Risk)
}
