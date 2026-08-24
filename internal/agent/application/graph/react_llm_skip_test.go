package graph_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ledgerSpy 统计 Record 调用次数，用于断言跳过轮不记账。
type ledgerSpy struct{ calls int }

func (l *ledgerSpy) Record(_ context.Context, _ string, usage port.TokenUsage) (int, float64) {
	l.calls++
	return usage.Total, 0
}

func skipState() graph.ReActState {
	return graph.ReActState{
		TenantID: "t1", TraceID: "tr1", Model: "qwen-turbo",
		MaxLLMSteps: 5,
		Messages:    []port.LLMMessage{{Role: "user", Content: "continue"}},
	}
}

// C2b:SkipNextLLM 跳过轮不调 routeLLM、不 Steps++、不记账，消费后清零。
func TestLLMNode_SkipNextLLM_DoesNotRouteAndDoesNotStep(t *testing.T) {
	stub := &capGWSequence{}
	ledger := &ledgerSpy{}
	node := graph.MakeLLMNodeForTest(stub, ledger, zap.NewNop())

	state := skipState()
	state.SkipNextLLM = true
	state.Steps = 2

	out, err := node(context.Background(), state)
	require.NoError(t, err)
	require.False(t, out.SkipNextLLM, "SkipNextLLM 消费后应清零")
	require.Equal(t, 2, out.Steps, "跳过轮不得 Steps++")
	require.Empty(t, stub.llmReqs, "跳过轮不得调用 routeLLM")
	require.Zero(t, ledger.calls, "跳过轮不得记账")
	require.Empty(t, out.Messages[len(out.Messages)-1].ToolCalls, "跳过轮不得 append LLM 响应")
}

// 对照：SkipNextLLM=false 时正常走 routeLLM、Steps++、记账。
func TestLLMNode_WithoutSkip_RoutesAndSteps(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	ledger := &ledgerSpy{}
	node := graph.MakeLLMNodeForTest(stub, ledger, zap.NewNop())

	state := skipState()
	state.Steps = 2

	out, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, 3, out.Steps, "正常轮应 Steps++")
	require.Len(t, stub.llmReqs, 1, "正常轮应调用 routeLLM")
	require.Equal(t, 1, ledger.calls, "正常轮应记账")
	require.Equal(t, "done", out.Output)
}
