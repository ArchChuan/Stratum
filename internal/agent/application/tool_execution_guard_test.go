package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
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

func TestToolExecutionGuardRequestsApprovalBeforeDestructiveExecution(t *testing.T) {
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
	req.Tool.Metadata["risk_level"] = "destructive"

	_, err := guard.Execute(context.Background(), req)

	var approvalErr *port.ToolApprovalRequiredError
	require.ErrorAs(t, err, &approvalErr)
	require.Equal(t, "approval-1", approvalErr.ApprovalID)
	require.Equal(t, 1, requests)
	require.Zero(t, executor.calls)
}

func TestToolExecutionGuardSystemAssistantApprovalBoundaries(t *testing.T) {
	// Platform assistant applies the strict L3b risk model (spec 2026-08-04
	// §4.4): write_reversible needs approval; destructive and unclassified
	// tools are refused. Ordinary agents keep the shared model.
	t.Run("write reversible requires approval", func(t *testing.T) {
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
				return "approval-sa-1", nil
			},
		})
		req := authorizedToolExecutionRequest()
		req.AgentID = domain.SystemAssistantID
		req.Tool.Metadata["risk_level"] = "write_reversible"

		_, err := guard.Execute(context.Background(), req)

		var approvalErr *port.ToolApprovalRequiredError
		require.ErrorAs(t, err, &approvalErr)
		require.Equal(t, "approval-sa-1", approvalErr.ApprovalID)
		require.Equal(t, 1, requests)
		require.Zero(t, executor.calls)
	})

	t.Run("destructive refused before executor", func(t *testing.T) {
		executor := &countingMCPExecutor{}
		guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
			Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
				scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
			}),
			Executor: executor,
		})
		req := authorizedToolExecutionRequest()
		req.AgentID = domain.SystemAssistantID
		req.Tool.Metadata["risk_level"] = "destructive"

		_, err := guard.Execute(context.Background(), req)

		require.ErrorIs(t, err, ErrToolAuthorizationDenied)
		require.Contains(t, err.Error(), string(domain.ToolReasonDestructiveForbidden))
		require.Zero(t, executor.calls)
	})

	t.Run("unclassified refused before executor", func(t *testing.T) {
		executor := &countingMCPExecutor{}
		guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
			Authorizer: NewToolAuthorizer(stubToolUserScopeResolver{
				scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
			}),
			Executor: executor,
		})
		req := authorizedToolExecutionRequest()
		req.AgentID = domain.SystemAssistantID
		req.Tool.Metadata["risk_level"] = "unclassified"

		_, err := guard.Execute(context.Background(), req)

		require.ErrorIs(t, err, ErrToolAuthorizationDenied)
		require.Contains(t, err.Error(), string(domain.ToolReasonToolUnclassified))
		require.Zero(t, executor.calls)
	})
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
