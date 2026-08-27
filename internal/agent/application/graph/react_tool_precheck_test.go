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

func mcpToolDefinition(name, capabilityID, serverID string) port.ToolDefinition {
	return port.ToolDefinition{
		Name: name, CapabilityID: capabilityID, ServerID: serverID,
		ProviderType: domain.ProviderTypeMCP,
		Metadata:     map[string]any{"risk_level": "destructive", "policy_resolved": true},
	}
}

// 整轮暂停：同一轮 LLM 消息含多个需审批 MCP 工具调用时，PrecheckApprovals 返回
// 全部错误，makeToolNode 封装 BatchToolApprovalRequiredError 终止本轮，工具一个
// 都不执行（ToolExecutionFn 不被调用）。
func TestToolNode_PrecheckBatchApprovalPausesWholeRound(t *testing.T) {
	calls := []port.ToolCall{
		{ID: "call-1", Name: "delete_order", Arguments: map[string]any{"order_id": "o1"}},
		{ID: "call-2", Name: "purge_customer", Arguments: map[string]any{"customer_id": "c1"}},
	}
	state := graph.ReActState{
		TenantID: "t1", TraceID: "tr1", Model: "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: calls}},
		AvailableTools: []port.ToolDefinition{
			mcpToolDefinition("delete_order", "delete", "orders"),
			mcpToolDefinition("purge_customer", "purge", "customers"),
		},
		PrecheckApprovals: func(_ context.Context, _ []port.ToolDefinition, got []port.ToolCall) ([]port.ToolApprovalRequiredError, error) {
			require.Len(t, got, 2, "预检必须收到本轮全部工具调用")
			return []port.ToolApprovalRequiredError{
				{ApprovalID: "approval-1", ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive},
				{ApprovalID: "approval-2", ToolCallID: "call-2", ServerID: "customers", ToolName: "purge", RiskLevel: port.ToolRiskDestructive},
			}, nil
		},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			t.Fatal("整轮暂停：需审批的工具一个都不允许执行")
			return nil, nil
		},
	}
	node := graph.MakeToolNodeForTest(&capGWSequence{}, zap.NewNop())
	out, err := node(context.Background(), state)

	var batchErr *port.BatchToolApprovalRequiredError
	require.ErrorAs(t, err, &batchErr, "整轮暂停必须返回 BatchToolApprovalRequiredError")
	require.Len(t, batchErr.Errors, 2)
	require.Equal(t, "call-1", batchErr.Errors[0].ToolCallID)
	require.Equal(t, "approval-1", batchErr.Errors[0].ApprovalID)
	require.Equal(t, "approval-2", batchErr.Errors[1].ApprovalID)
	require.Len(t, out.Messages, 1, "整轮暂停：state 原样返回，不得追加任何 tool 观察/消息")
	require.Empty(t, out.AllToolCalls, "整轮暂停：不得记录任何工具调用")
}

// 对照：预检返回空（本轮无工具需审批）时，工具正常执行，不进批量暂停路径。
func TestToolNode_PrecheckNoneRunsTools(t *testing.T) {
	calls := []port.ToolCall{
		{ID: "call-1", Name: "list_orders", Arguments: map[string]any{}},
	}
	state := graph.ReActState{
		TenantID: "t1", TraceID: "tr1", Model: "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: calls}},
		AvailableTools: []port.ToolDefinition{
			mcpToolDefinition("list_orders", "list", "orders"),
		},
		PrecheckApprovals: func(context.Context, []port.ToolDefinition, []port.ToolCall) ([]port.ToolApprovalRequiredError, error) {
			return nil, nil
		},
		ToolExecutionFn: func(_ context.Context, req port.ToolExecutionRequest) (any, error) {
			require.Equal(t, "call-1", req.ToolCallID)
			return port.GuardedToolResult{ModelContent: "orders ok", Untrusted: true}, nil
		},
	}
	node := graph.MakeToolNodeForTest(&capGWSequence{}, zap.NewNop())
	out, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Messages, 2, "assistant 消息 + 1 条 tool 结果")
	require.Equal(t, "tool", out.Messages[1].Role)
	require.Equal(t, "orders ok", out.Messages[1].Content)
	require.Len(t, out.AllToolCalls, 1)
}

// 预检钩子为 nil 时退回逐条 guard 路径：工具直接执行，与旧行为一致。
func TestToolNode_PrecheckNilRunsTools(t *testing.T) {
	calls := []port.ToolCall{
		{ID: "call-1", Name: "list_orders", Arguments: map[string]any{}},
	}
	state := graph.ReActState{
		TenantID: "t1", TraceID: "tr1", Model: "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: calls}},
		AvailableTools: []port.ToolDefinition{
			mcpToolDefinition("list_orders", "list", "orders"),
		},
		ToolExecutionFn: func(_ context.Context, req port.ToolExecutionRequest) (any, error) {
			require.Equal(t, "call-1", req.ToolCallID)
			return port.GuardedToolResult{ModelContent: "orders ok", Untrusted: true}, nil
		},
	}
	node := graph.MakeToolNodeForTest(&capGWSequence{}, zap.NewNop())
	out, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, out.Messages, 2)
	require.Equal(t, "tool", out.Messages[1].Role)
}
