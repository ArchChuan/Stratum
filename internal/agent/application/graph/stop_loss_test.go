package graph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// P2 C 层 Agent ReAct 治理：correction 计数（nil 安全）、同错指纹、止损门、
// 降级输出指令、DegradeReason 固定枚举、子节点共享 map 传播。

// 裸 ReActState 构造（map 未初始化）触发 correction 不 panic —— 计数 helper
// 必须 nil 安全，plan_tools_test 裸构造会走 correction 分支。
func TestStopLoss_NilSafeBareStateCorrection(t *testing.T) {
	state := &graph.ReActState{}
	content := graph.RecordCorrectionForTest(state, "stratum_create_plan", errors.New("boom"), &domain.Plan{ID: "p1", Revision: 1})
	require.Contains(t, content, `"correction"`)
	require.Equal(t, 1, state.CorrectionStreaks["stratum_create_plan"])
	require.False(t, state.Degraded) // 单次未达阈值
}

// 同一工具连续（同错指纹）失败达阈值触发止损 + 整体降级 + 固定枚举 reason。
func TestStopLoss_ThreeRepeatedErrorsTriggerStopLoss(t *testing.T) {
	state := &graph.ReActState{}
	for i := 0; i < constants.AgentToolStopLossThreshold; i++ {
		graph.RecordToolFailureForTest(state, "tool_x", "same error")
	}
	require.True(t, state.StopLossTools["tool_x"])
	require.True(t, state.Degraded)
	require.Equal(t, constants.AgentDegradeReasonStopLossPrefix+"tool_x", state.DegradeReason)
}

// 同错指纹：规范化后相同才累计（空白/大小写差异不打断），不同错误重置为 1。
// 交错错误不达阈值 → 不停损、不降级。
func TestStopLoss_FingerprintResetOnDifferentError(t *testing.T) {
	state := &graph.ReActState{}
	graph.RecordToolFailureForTest(state, "tool_x", "  Error   One ")
	require.Equal(t, 1, state.CorrectionStreaks["tool_x"])
	graph.RecordToolFailureForTest(state, "tool_x", "error one") // 规范化后同指纹
	require.Equal(t, 2, state.CorrectionStreaks["tool_x"])
	graph.RecordToolFailureForTest(state, "tool_x", "a different failure")
	require.Equal(t, 1, state.CorrectionStreaks["tool_x"]) // 不同错重置
	require.False(t, state.StopLossTools["tool_x"])
	require.False(t, state.Degraded)
}

// 止损后的工具不再执行：makeToolNode 命中 StopLossTools 直接返回观察让模型换
// 路，不调用真实 tool fn（capGW 传 nil 也不触发）；消息配对完整、审计保留。
func TestStopLoss_ToolSkippedAfterStopLoss(t *testing.T) {
	state := graph.ReActState{
		StopLossTools:  map[string]bool{"tool_x": true},
		AvailableTools: []port.ToolDefinition{{Name: "tool_x"}},
		Messages: []port.LLMMessage{{
			Role:      "assistant",
			ToolCalls: []port.ToolCall{{ID: "c1", Name: "tool_x", Arguments: map[string]any{}}},
		}},
	}
	node := graph.MakeToolNodeForTest(nil, zap.NewNop())
	next, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, next.Messages, 2) // assistant tool_calls + tool 观察
	last := next.Messages[1]
	require.Equal(t, "tool", last.Role)
	require.Equal(t, "c1", last.ToolCallID)
	require.Contains(t, last.Content, "stopped")
	require.Len(t, next.AllToolCalls, 1) // 审计保留（尽管未执行）
}

// 降级时最后一步 final-answer 指令为降级专用：不得声称完成了未验证的操作。
func TestStopLoss_DegradedFinalAnswerInstruction(t *testing.T) {
	state := &graph.ReActState{
		MaxLLMSteps: 3,
		Steps:       2,
		Degraded:    true,
		Messages:    []port.LLMMessage{{Role: "user", Content: "task"}},
	}
	tools, msgs, _ := graph.PrepareLLMRequestForTest(context.Background(), state)
	require.Empty(t, tools) // 最后一步禁工具
	// 降级指令以 system role 注入头部（anchor 区），不追加在末尾与任务混淆。
	instruction := lastSystemContent(msgs)
	require.Contains(t, instruction, "Do not claim operations that were not verified")
	require.NotContains(t, instruction, "provide your final answer now. Do not call any tools")
	require.Contains(t, instruction, "inform the user that the maximum number of steps has been reached")
}

// 未降级（或仅单点止损未达整体）时 final-answer 用普通指令。
func TestStopLoss_NormalFinalAnswerWhenNotDegraded(t *testing.T) {
	state := &graph.ReActState{
		MaxLLMSteps: 3,
		Steps:       2,
		Messages:    []port.LLMMessage{{Role: "user", Content: "task"}},
	}
	_, msgs, _ := graph.PrepareLLMRequestForTest(context.Background(), state)
	instruction := lastSystemContent(msgs)
	require.NotContains(t, instruction, "Do not claim operations that were not verified")
	require.Contains(t, instruction, "inform the user that the maximum number of steps has been reached")
}

// lastSystemContent 返回消息序列中最后一条 system 消息正文（测试侧收尾指令
// 定位：指令以 system role 注入头部 anchor 区）。
func lastSystemContent(msgs []port.LLMMessage) string {
	var content string
	for _, m := range msgs {
		if m.Role == "system" {
			content = m.Content
		}
	}
	return content
}

// DegradeReason 是固定枚举，绝不含内部标识或原始错误正文：err 与 plan 里都带
// plan_id/revision 细节，reason 只允许 "tool_stop_loss:<tool>"。
func TestStopLoss_DegradeReasonNeverLeaksInternalDetail(t *testing.T) {
	state := &graph.ReActState{}
	secretErr := errors.New("secret detail plan_id=plan-secret-abc revision=7")
	plan := &domain.Plan{ID: "plan-secret-abc", Revision: 7}
	for i := 0; i < constants.AgentToolStopLossThreshold; i++ {
		graph.RecordCorrectionForTest(state, "stratum_create_plan", secretErr, plan)
	}
	require.True(t, state.Degraded)
	require.Equal(t, "tool_stop_loss:stratum_create_plan", state.DegradeReason)
	require.NotContains(t, state.DegradeReason, "plan-secret-abc")
	require.NotContains(t, state.DegradeReason, "revision")
	require.NotContains(t, state.DegradeReason, "secret detail")
}

// plan slot 子状态是结构体拷贝但 map 共享：子节点止损全局累计到父图（3 节点
// 各失败 1 次 = 3 全局止损），而 bool Degraded 值拷贝不传播 —— 父图降级由
// collectGraphResult 从共享 StopLossTools map 推导。
func TestStopLoss_ChildStatePropagatesSharedMapToParent(t *testing.T) {
	parent := graph.ReActState{
		CorrectionStreaks:         map[string]int{},
		LastCorrectionFingerprint: map[string]string{},
		StopLossTools:             map[string]bool{},
	}
	child := graph.PlanSlotChildStateForTest(parent)
	// 子节点 3 次同错触发止损，写进共享 map → 父图可见；bool Degraded 值拷贝
	// 不传播，父图降级由 collectGraphResult 从共享 StopLossTools 推导。
	for i := 0; i < constants.AgentToolStopLossThreshold; i++ {
		graph.RecordToolFailureForTest(&child, "tool_y", "same err")
	}
	require.True(t, parent.StopLossTools["tool_y"]) // 共享 map → 父图可见
	require.True(t, child.Degraded)
	require.False(t, parent.Degraded) // bool 值拷贝不传播，由 collectGraphResult 推导
}
