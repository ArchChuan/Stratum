package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type toolPermissionLLM struct {
	responses []port.CapabilityResponse
	requests  []port.LLMCapRequest
}

func (s *toolPermissionLLM) Route(_ context.Context, request port.CapabilityRequest) (port.CapabilityResponse, error) {
	if request.LLM != nil {
		s.requests = append(s.requests, *request.LLM)
	}
	if len(s.responses) == 0 {
		return port.CapabilityResponse{}, errors.New("unexpected LLM call")
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func TestToolPermissionE2EForgedCallNeverReachesMCP(t *testing.T) {
	llm := &toolPermissionLLM{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "forged-1", Name: "mcp:orders:delete", Arguments: map[string]any{"id": "order-1"}}}},
		{Content: "denied safely"},
	}}
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{
			UserActive: true, AllowsTool: true,
		}}),
		Executor: executor,
	})
	out, err := runToolPermissionLoop(t, llm, func(ctx context.Context, request port.ToolExecutionRequest) (any, error) {
		bindToolPermissionSubject(&request, []string{"mcp:orders:get"})
		return guard.Execute(ctx, request)
	})

	require.NoError(t, err)
	require.Equal(t, "denied safely", out.Output)
	require.Zero(t, executor.calls)
	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, domain.ToolTraceStatusError, out.ToolObservations[0].Status)
}

func TestToolPermissionE2EDestructiveCallPausesBeforeMCP(t *testing.T) {
	llm := &toolPermissionLLM{responses: []port.CapabilityResponse{{ToolCalls: []port.ToolCall{{
		ID: "delete-1", Name: "mcp:orders:delete", Arguments: map[string]any{"id": "order-1"},
	}}}}}
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{
			UserActive: true, AllowsTool: true,
		}}),
		Executor: executor,
		RequestApproval: func(context.Context, port.ToolApprovalRequest) (string, error) {
			return "approval-1", nil
		},
	})
	_, err := runToolPermissionLoop(t, llm, func(ctx context.Context, request port.ToolExecutionRequest) (any, error) {
		bindToolPermissionSubject(&request, []string{"mcp:orders:delete"})
		return guard.Execute(ctx, request)
	})

	var approvalErr *port.ToolApprovalRequiredError
	require.ErrorAs(t, err, &approvalErr)
	require.Equal(t, "approval-1", approvalErr.ApprovalID)
	require.Zero(t, executor.calls)
}

func TestToolPermissionE2EApprovedCallReauthorizesAndReturnsGuardedResult(t *testing.T) {
	llm := &toolPermissionLLM{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "delete-1", Name: "mcp:orders:delete", Arguments: map[string]any{"id": "order-1"}}}},
		{Content: "completed"},
	}}
	approvedCalls := 0
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{
			UserActive: true, AllowsTool: true,
		}}),
		ExecuteApproved: func(_ context.Context, request ToolExecutionRequest) (port.MCPToolResult, error) {
			approvedCalls++
			require.Equal(t, "order-1", request.Arguments["id"])
			return port.MCPToolResult{Content: []port.MCPContent{{Type: "text", Text: "deleted"}}}, nil
		},
	})
	out, err := runToolPermissionLoop(t, llm, func(ctx context.Context, request port.ToolExecutionRequest) (any, error) {
		bindToolPermissionSubject(&request, []string{"mcp:orders:delete"})
		request.ApprovalID = "approval-1"
		return guard.Execute(ctx, request)
	})

	require.NoError(t, err)
	require.Equal(t, 1, approvedCalls)
	require.Equal(t, "completed", out.Output)
	require.Contains(t, llm.requests[1].Messages[len(llm.requests[1].Messages)-1].Content, "untrusted_tool_result")
}

func runToolPermissionLoop(
	t *testing.T,
	llm port.CapabilityGateway,
	execute port.ToolExecutionFn,
) (graph.ReActState, error) {
	t.Helper()
	compiled, err := graph.BuildReActGraph(llm, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	return compiled.Invoke(context.Background(), graph.ReActState{
		TenantID: "tenant-1", TraceID: "trace-1", ExecutionID: "exec-1", Model: "test-model",
		Messages: []port.LLMMessage{{Role: "user", Content: "operate on order"}},
		AvailableTools: []port.ToolDefinition{{
			Name: "mcp:orders:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "orders", CapabilityID: "delete", InputSchema: map[string]any{"type": "object"},
			Metadata: map[string]any{"risk_level": "destructive", "policy_resolved": true},
		}},
		ToolExecutionFn: execute,
	}, graph.RunConfig{MaxSteps: 5})
}

func bindToolPermissionSubject(request *port.ToolExecutionRequest, agentToolIDs []string) {
	request.TenantID = "tenant-1"
	request.UserID = "user-1"
	request.AgentID = "agent-1"
	request.TraceID = "trace-1"
	request.ExecutionID = "exec-1"
	request.AgentToolIDs = agentToolIDs
}
