package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

type countingMCPExecutor struct {
	calls int
}

func (e *countingMCPExecutor) ExecuteMCPTool(
	context.Context, string, string, map[string]any,
) (port.MCPToolResult, error) {
	e.calls++
	return port.MCPToolResult{Content: []port.MCPContent{{Type: "text", Text: "executed"}}}, nil
}

func TestToolExecutionGuardRejectsForgedToolBeforeExecutor(t *testing.T) {
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: executor,
	})

	_, err := guard.Execute(context.Background(), ToolExecutionRequest{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolCallID: "call-1",
		Tool: port.ToolDefinition{
			Name: "mcp:orders:delete", ProviderType: domain.ProviderTypeMCP,
			ServerID: "orders", CapabilityID: "delete", Metadata: map[string]any{"risk_level": "read", "policy_resolved": true},
		},
		Arguments:     map[string]any{"id": "order-1"},
		AgentToolIDs:  []string{"mcp:orders:get"},
		PolicyVersion: "policy-v1",
	})

	require.ErrorIs(t, err, ErrToolAuthorizationDenied)
	require.Zero(t, executor.calls)
}

func TestToolExecutionGuardExecutesAuthorizedReadTool(t *testing.T) {
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: executor,
	})

	output, err := guard.Execute(context.Background(), authorizedToolExecutionRequest())

	require.NoError(t, err)
	guarded, ok := output.(port.GuardedToolResult)
	require.True(t, ok)
	require.Contains(t, guarded.ModelContent, "executed")
	require.Equal(t, 1, executor.calls)
}

func TestToolExecutionGuardSharedRiskModelForAllAgents(t *testing.T) {
	// 系统助手等同化后：所有 agent（含平台助手 seed 行）共享同一授权模型，
	// 决策只由 risk_level + policy_resolved 决定，不再按 AgentID 区分。
	// 管理员显式配置（policy_resolved=true）后 read/write_reversible/destructive
	// 直接放行（配置即授权）；未配置（policy_resolved=false）一律 require_approval
	// （含 read）；unclassified 一律 require_approval。
	tests := []struct {
		name     string
		risk     string
		resolved bool
		executes bool
	}{
		{name: "configured read executes", risk: "read", resolved: true, executes: true},
		{name: "configured write reversible executes", risk: "write_reversible", resolved: true, executes: true},
		{name: "configured destructive executes", risk: "destructive", resolved: true, executes: true},
		{name: "unclassified requires approval", risk: "unclassified", resolved: true, executes: false},
		{name: "unconfigured read requires approval", risk: "read", resolved: false, executes: false},
		{name: "unconfigured destructive requires approval", risk: "destructive", resolved: false, executes: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &countingMCPExecutor{}
			requests := 0
			guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
				Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
					scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
				}),
				Executor: executor,
				RequestApproval: func(_ context.Context, request port.ToolApprovalRequest) (string, error) {
					requests++
					require.Equal(t, "call-1", request.ToolCallID)
					return "approval-1", nil
				},
			})
			req := authorizedToolExecutionRequest()
			req.Tool.Metadata["risk_level"] = tt.risk
			req.Tool.Metadata["policy_resolved"] = tt.resolved

			_, err := guard.Execute(context.Background(), req)

			if tt.executes {
				require.NoError(t, err)
				require.Equal(t, 1, executor.calls)
				require.Zero(t, requests)
				return
			}
			var approvalErr *port.ToolApprovalRequiredError
			require.ErrorAs(t, err, &approvalErr)
			require.Equal(t, "approval-1", approvalErr.ApprovalID)
			require.Equal(t, 1, requests)
			require.Zero(t, executor.calls)
		})
	}
}

func TestToolExecutionGuardReauthorizesApprovedCall(t *testing.T) {
	executor := &countingMCPExecutor{}
	approvedCalls := 0
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			err: errors.New("membership lookup failed"),
		}),
		Executor: executor,
		ExecuteApproved: func(context.Context, ToolExecutionRequest) (port.MCPToolResult, error) {
			approvedCalls++
			return port.MCPToolResult{}, nil
		},
	})
	req := authorizedToolExecutionRequest()
	req.ApprovalID = "approval-1"
	req.Tool.Metadata["risk_level"] = "destructive"

	_, err := guard.Execute(context.Background(), req)

	require.ErrorIs(t, err, ErrToolAuthorizationDenied)
	require.Zero(t, approvedCalls)
	require.Zero(t, executor.calls)
}

func TestToolExecutionGuardValidatesArgumentsBeforeExecution(t *testing.T) {
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: executor,
	})
	req := authorizedToolExecutionRequest()
	req.Tool.InputSchema = map[string]any{
		"type": "object", "required": []any{"id"},
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
	}
	req.Arguments = map[string]any{"id": 42}

	_, err := guard.Execute(context.Background(), req)

	require.ErrorIs(t, err, ErrToolArgumentsInvalid)
	require.Zero(t, executor.calls)
}

func delegateGuardRequest(delegate port.DelegateToolRunFunc) ToolExecutionRequest {
	req := authorizedToolExecutionRequest()
	req.Tool = port.ToolDefinition{
		Name: graph.StratumDelegateToolName, ProviderType: domain.ProviderTypeBuiltin,
		InputSchema: map[string]any{
			"type": "object", "required": []any{"goal"},
			"properties": map[string]any{
				"goal":      map[string]any{"type": "string", "minLength": 1},
				"max_steps": map[string]any{"type": "integer"},
			},
		},
		Metadata: map[string]any{"risk_level": "read", "policy_resolved": true},
	}
	req.Arguments = map[string]any{"goal": "summarize file"}
	req.AgentToolIDs = []string{graph.StratumDelegateToolName}
	req.DelegateExecutor = delegate
	return req
}

// TestToolExecutionGuardDispatchesDelegateBeforeExecutor 验证 stratum_delegate 走
// guard 全链路：授权 + jsonschema 通过后直接调用 delegate 闭包（不经 MCP executor），
// 结果仍经 ResultGuard 打 untrusted 标记。
func TestToolExecutionGuardDispatchesDelegateBeforeExecutor(t *testing.T) {
	executor := &countingMCPExecutor{}
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: executor,
	})

	called := false
	req := delegateGuardRequest(func(_ context.Context, args map[string]any) (port.MCPToolResult, error) {
		called = true
		require.Equal(t, "summarize file", args["goal"])
		return port.MCPToolResult{StructuredContent: map[string]any{
			"summary": "done", "status": "success", "tokens_used": 42,
		}}, nil
	})

	output, err := guard.Execute(context.Background(), req)
	require.NoError(t, err)
	require.True(t, called, "delegate 闭包必须被调用")
	// 结果经 ResultGuard 包裹为 <untrusted_tool_result>，且不触碰 MCP executor。
	guarded, ok := output.(port.GuardedToolResult)
	require.True(t, ok)
	require.True(t, guarded.Untrusted)
	require.Contains(t, guarded.ModelContent, "<untrusted_tool_result>")
	require.Contains(t, guarded.ModelContent, `"tokens_used":42`)
	require.Zero(t, executor.calls)
}

func TestToolExecutionGuardDelegateInvalidArgumentsRejected(t *testing.T) {
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
	})
	executorCalled := false
	req := delegateGuardRequest(func(_ context.Context, _ map[string]any) (port.MCPToolResult, error) {
		executorCalled = true
		return port.MCPToolResult{}, nil
	})
	req.Arguments = map[string]any{} // 缺必填 goal

	_, err := guard.Execute(context.Background(), req)
	require.ErrorIs(t, err, ErrToolArgumentsInvalid)
	require.False(t, executorCalled, "非法参数不得调用 delegate 闭包")
}

func TestToolExecutionGuardDelegateNotAllowlistedDenied(t *testing.T) {
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
	})
	executorCalled := false
	req := delegateGuardRequest(func(_ context.Context, _ map[string]any) (port.MCPToolResult, error) {
		executorCalled = true
		return port.MCPToolResult{}, nil
	})
	req.AgentToolIDs = []string{"mcp:orders:get"} // 未把 delegate 列入 AgentToolIDs

	_, err := guard.Execute(context.Background(), req)
	require.ErrorIs(t, err, ErrToolAuthorizationDenied)
	require.False(t, executorCalled, "未授权工具不得调用 delegate 闭包")
}

func TestSystemAssistantToolInputSchemasCompileForSharedExecutionGuard(t *testing.T) {
	tests := []struct {
		name      string
		arguments map[string]any
	}{
		{name: ToolSearchOfficialDocs, arguments: map[string]any{"query": "Agent 使用"}},
		{name: ToolDiagnoseTenant, arguments: map[string]any{"areas": []any{"agent", "mcp"}}},
		{name: ToolProposeResourceChange, arguments: map[string]any{
			"resourceKind": "skill_draft", "operation": "create",
			"payload": map[string]any{"name": "草稿", "description": "", "instructions": "执行步骤"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var schema map[string]any
			for _, tool := range SystemAssistantToolDefinitions() {
				if tool.Name == tc.name {
					schema = tool.InputSchema
				}
			}
			require.NotNil(t, schema)
			require.NoError(t, validateToolArguments(schema, tc.arguments))
		})
	}
}

// errorResultMCPExecutor 返回 IsError=true 的结果，模拟远端工具真实执行但明确报错。
type errorResultMCPExecutor struct{}

func (errorResultMCPExecutor) ExecuteMCPTool(
	context.Context, string, string, map[string]any,
) (port.MCPToolResult, error) {
	return port.MCPToolResult{IsError: true}, nil
}

func TestToolExecutionGuardWrapsDefiniteFailureOutcome(t *testing.T) {
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: errorResultMCPExecutor{},
	})

	_, err := guard.Execute(context.Background(), authorizedToolExecutionRequest())

	var execErr *port.MCPToolExecutionError
	require.ErrorAs(t, err, &execErr)
	require.Equal(t, port.ToolExecutionOutcomeDefiniteFailure, execErr.Outcome)
	require.ErrorIs(t, err, ErrMCPToolResult)
}

func TestToolExecutionGuardWrapsSchemaMismatchAsUnknownOutcome(t *testing.T) {
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
			scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
		}),
		Executor: &countingMCPExecutor{},
	})
	req := authorizedToolExecutionRequest()
	// 输出 schema 要求 number，executor 返回 text → schema 校验失败，归类 outcome_unknown。
	req.Tool.OutputSchema = map[string]any{
		"type": "number",
	}

	_, err := guard.Execute(context.Background(), req)

	var execErr *port.MCPToolExecutionError
	require.ErrorAs(t, err, &execErr)
	require.Equal(t, port.ToolExecutionOutcomeUnknown, execErr.Outcome)
	require.ErrorIs(t, err, ErrMCPToolResultSchema)
}

func authorizedToolExecutionRequest() ToolExecutionRequest {
	return ToolExecutionRequest{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolCallID: "call-1",
		Tool: port.ToolDefinition{
			Name: "mcp:orders:get", ProviderType: domain.ProviderTypeMCP,
			ServerID: "orders", CapabilityID: "get", InputSchema: map[string]any{"type": "object"},
			Metadata: map[string]any{"risk_level": "read", "policy_resolved": true},
		},
		Arguments:     map[string]any{"id": "order-1"},
		AgentToolIDs:  []string{"mcp:orders:get"},
		PolicyVersion: "policy-v1",
	}
}
