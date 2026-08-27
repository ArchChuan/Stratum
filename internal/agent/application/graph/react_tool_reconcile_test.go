package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// mcpToolState 构造带 MCP 工具定义与自定义执行错误的 ReActState fixture，
// 复用既有 graph 测试的 ToolExecutionFn 注入模式。
func mcpToolState(execErr error) *ReActState {
	return &ReActState{
		AvailableTools: []port.ToolDefinition{{
			Name: "mcp:orders:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "orders", CapabilityID: "delete",
		}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			return nil, execErr
		},
	}
}

// mcpToolCall 是测试用的一次 MCP 工具调用。
func mcpToolCall() port.ToolCall {
	return port.ToolCall{ID: "call-1", Name: "mcp:orders:delete", Arguments: map[string]any{"id": "o1"}}
}

// TestExecMCPToolUnwrapsOutcome 验证 execMCPTool 对带 Outcome 的
// MCPToolExecutionError 做 errors.As 解包并回填 toolExecResult.outcome，
// 供 ToolObservation 落盘与幻觉防护对账使用。
func TestExecMCPToolUnwrapsOutcome(t *testing.T) {
	withOutcome := func(o port.ToolExecutionOutcome) error {
		return &port.MCPToolExecutionError{Outcome: o, Err: errors.New("boom")}
	}
	tests := []struct {
		name    string
		execErr error
		want    string
	}{
		{name: "definite failure", execErr: withOutcome(port.ToolExecutionOutcomeDefiniteFailure), want: string(port.ToolExecutionOutcomeDefiniteFailure)},
		{name: "outcome unknown", execErr: withOutcome(port.ToolExecutionOutcomeUnknown), want: string(port.ToolExecutionOutcomeUnknown)},
		{name: "not sent", execErr: withOutcome(port.ToolExecutionOutcomeNotSent), want: string(port.ToolExecutionOutcomeNotSent)},
		{name: "plain error keeps empty", execErr: errors.New("boom"), want: ""},
		{name: "wrapped error still unwraps", execErr: errors.Join(errors.New("wrap"), withOutcome(port.ToolExecutionOutcomeDefiniteFailure)), want: string(port.ToolExecutionOutcomeDefiniteFailure)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mcpToolState(tt.execErr)
			provider := classifyToolProvider(mcpToolCall().Name, s.AvailableTools)
			require.Equal(t, domain.ProviderTypeMCP, provider.ProviderType)

			res := execMCPTool(context.Background(), mcpToolCall(), s, time.Now(), provider, zap.NewNop())

			require.Equal(t, domain.ToolTraceStatusError, res.status)
			require.Equal(t, tt.want, res.outcome)
		})
	}
}

// TestExecMCPToolOutcomePersistedToObservation 验证 outcome 从 execMCPTool
// 经 appendToolObservation 落到 ToolObservation.Outcome（对账数据源）。
func TestExecMCPToolOutcomePersistedToObservation(t *testing.T) {
	s := mcpToolState(&port.MCPToolExecutionError{Outcome: port.ToolExecutionOutcomeDefiniteFailure, Err: errors.New("boom")})
	provider := classifyToolProvider(mcpToolCall().Name, s.AvailableTools)
	start := time.Now()

	res := execMCPTool(context.Background(), mcpToolCall(), s, start, provider, zap.NewNop())
	appendToolObservation(s, mcpToolCall(), provider, res, start, 42)

	require.Len(t, s.ToolObservations, 1)
	obs := s.ToolObservations[0]
	require.Equal(t, "call-1", obs.ToolCallID)
	require.Equal(t, domain.ToolTraceStatusError, obs.Status)
	require.Equal(t, string(port.ToolExecutionOutcomeDefiniteFailure), obs.Outcome)
}

// TestExecMCPToolSuccessKeepsOutcomeEmpty 验证成功执行不污染 outcome
// （保持空串，向后兼容既有序列化）。
func TestExecMCPToolSuccessKeepsOutcomeEmpty(t *testing.T) {
	s := mcpToolState(nil)
	s.ToolExecutionFn = func(context.Context, port.ToolExecutionRequest) (any, error) {
		return port.GuardedToolResult{ModelContent: "<untrusted_tool_result>ok</untrusted_tool_result>", Untrusted: true}, nil
	}
	provider := classifyToolProvider(mcpToolCall().Name, s.AvailableTools)
	start := time.Now()

	res := execMCPTool(context.Background(), mcpToolCall(), s, start, provider, zap.NewNop())

	require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
	require.Empty(t, res.outcome)
}
