package application

import (
	"context"
	"testing"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// P2 降级透出：collectGraphResult 从 finalState.StopLossTools 非空推导整体
// 降级（bool Degraded 值拷贝不传播，读共享 map 是唯一可靠信号），DegradeReason
// 用排序后第一个止损工具拼固定枚举，写入 AgentResult 供 handler SSE done 透出。
func TestCollectGraphResult_DerivesDegradedFromStopLoss(t *testing.T) {
	a := NewBaseAgent(&AgentConfig{}, zap.NewNop())
	result := &AgentResult{}
	finalState := agentgraph.ReActState{
		StopLossTools: map[string]bool{"tool_x": true, "tool_b": true},
	}
	a.collectGraphResult(context.Background(), result, finalState, agentExecContext{cfg: &ExecutionConfig{}})
	require.True(t, result.Degraded)
	require.Equal(t, "tool_stop_loss:tool_b", result.DegradeReason) // 排序后第一个
	require.NotContains(t, result.DegradeReason, "tool_x")
}

func TestCollectGraphResult_NoStopLossNotDegraded(t *testing.T) {
	a := NewBaseAgent(&AgentConfig{}, zap.NewNop())
	result := &AgentResult{}
	a.collectGraphResult(context.Background(), result, agentgraph.ReActState{}, agentExecContext{cfg: &ExecutionConfig{}})
	require.False(t, result.Degraded)
	require.Empty(t, result.DegradeReason)
}
