package graph_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestBuildReActGraph_CompleteTaskRoutesToPlanPath 验证 stratum_complete_task 走
// plan-tool 路径而非 MCP 路径。刻意不设置 ToolExecutionFn：若误走 MCP 路径，
// execMCPTool 会因 findTool 查不到（不在 AvailableTools）或 executor 未配置而
// 返回非 success 观察。走 plan 路径则 observation 为 success 且终态置位
// TaskCompleteRequested，并透出 completed task 观察（internal provider 佐证）。
func TestBuildReActGraph_CompleteTaskRoutesToPlanPath(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{
				ID: "c1", Name: "stratum_complete_task",
				Arguments: map[string]any{"expected_revision": int64(1)},
			}}},
			{Content: "任务已完成"},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "mark complete"}},
		ActivePlan: &domain.Plan{
			ID: "plan-1", Status: domain.PlanStatusActive,
			Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusSucceeded}},
		},
		// ToolExecutionFn 留空：证明未走 MCP 路径。
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig[graph.ReActState]{MaxSteps: 10})
	require.NoError(t, err)

	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, "stratum_complete_task", out.ToolObservations[0].ToolName)
	require.Equal(t, domain.ToolTraceStatusSuccess, out.ToolObservations[0].Status)
	// plan 工具 provider 归因为 internal，非 mcp —— 佐证走 plan-tool 路径。
	require.Equal(t, domain.ProviderTypeInternal, out.ToolObservations[0].ProviderType)
	require.True(t, out.TaskCompleteRequested)
	require.Equal(t, "任务已完成", out.Output)
}
