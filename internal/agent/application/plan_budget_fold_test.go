package application_test

import (
	"context"
	"testing"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

// TestPlanSubLoopTokensFoldIntoParentBudget 验证 buildPlanNodeExecutor 的子循环
// token delta（final.TotalTokens − child.TotalTokens，child 是父状态的结构体
// 拷贝、继承父图基线）折回父图预算账本（Finding 1 修复）：
// 全链路（create_plan → continue_plan → 子循环 → 波次汇合 → 父图收尾）结束后
// result.TokensUsed = 父图自身用量 + 子循环用量，基线只计一次（delta 形式）。
// 预算 = 父图自身用量 + 子循环用量 − 1 时，父图最后一次 LLM 检查点必须业务终止；
// 修复前子循环用量丢失，该预算下不会终止（每槽位放宽 ≈ 一个子循环 cap）。
func TestPlanSubLoopTokensFoldIntoParentBudget(t *testing.T) {
	gw := &mockCapGW{responses: []port.CapabilityResponse{
		// 父图：创建 plan（基线 400）
		{ToolCalls: []port.ToolCall{{ID: "plan-1", Name: "stratum_create_plan", Arguments: map[string]any{
			"expected_revision": float64(0),
			"nodes":             []any{map[string]any{"key": "one", "goal": "Do one thing"}},
		}}}, Usage: port.TokenUsage{Total: 400}},
		// 父图：调度波次（基线 800）
		{ToolCalls: []port.ToolCall{{ID: "cont-1", Name: "stratum_continue_plan", Arguments: map[string]any{"expected_revision": float64(1)}}}, Usage: port.TokenUsage{Total: 400}},
		// 子循环：节点目标回答（子循环从基线 800 起，自身消耗 600）
		{Content: "node done", Usage: port.TokenUsage{Total: 600}},
		// 父图：波次汇合后的最终回答（累计 1900）
		{Content: "plan done", Usage: port.TokenUsage{Total: 500}},
	}}
	a := newReActAgent()
	a.SetCapGateway(gw)
	// 断点续接默认全开：plan 路径（create_plan/continue_plan/波次汇合）无条件
	// 写 checkpoint，测试必须注入 writer，否则 ExecutePlanTool 报
	// ErrPlanCheckpointRequired 使 plan 断裂、预算终止不触发。
	a.SetCheckpointStore(&resumableCheckpointStore{})
	// 预算 = 父图自身用量(400+400+500) + 子循环用量(600) − 1：只有折回后累计
	// (1900) 才超限；折回前父图只看得到 1300，不会终止。
	const budget = 1800

	result, err := a.Execute(context.Background(), "plan this task",
		agent.WithTenantID("t1"),
		agent.WithMaxTokensPerExecution(budget),
	)
	require.NoError(t, err) // 业务终止非错误路径
	require.Equal(t, graph.CostBudgetTerminated, result.TerminatedBy)
	// 基线 800 只计一次：400(create) + 400(continue) + 600(子循环) + 500(最终)。
	require.Equal(t, 1900, result.TokensUsed)
	require.Equal(t, "plan done", result.Output)
}
