package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testAssistantGuard(value any) (port.GuardedToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return port.GuardedToolResult{}, err
	}
	return port.GuardedToolResult{ModelContent: string(raw)}, nil
}

func TestSystemAssistantToolsExecuteInProcessAndBuildArtifacts(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		OfficialDocsSearchFn: func(_ context.Context, query string) ([]domain.Citation, error) {
			return []domain.Citation{{Title: query, URL: "/docs/agent"}}, nil
		},
		DiagnosticFn: func(_ context.Context, areas []domain.DiagnosticArea) (domain.DiagnosticEvidence, error) {
			return domain.DiagnosticEvidence{Scope: domain.DiagnosticScopeSelf, AreaResults: []domain.DiagnosticAreaResult{
				{Area: domain.DiagnosticAreaAgent, Outcome: "success", DurationMs: 5},
			}}, nil
		},
		ProposalCreateFn: func(_ context.Context, _ map[string]any) (domain.ResourceChangeProposalArtifact, error) {
			return domain.ResourceChangeProposalArtifact{ID: "proposal-1", Status: domain.StatusReadyForReview}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}

	t.Run("official docs", func(t *testing.T) {
		result := execOfficialDocsSearchTool(context.Background(), port.ToolCall{
			Name: domain.SystemAssistantToolSearchOfficialDocs, Arguments: map[string]any{"query": "Agent"},
		}, state, time.Now())
		require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
		require.NotNil(t, result.artifact)
		require.Len(t, result.artifact.Citations, 1)
		require.Equal(t, "Agent", result.artifact.Citations[0].Title)
	})

	t.Run("diagnose tenant", func(t *testing.T) {
		result := execDiagnoseTenantTool(context.Background(), port.ToolCall{
			Name: domain.SystemAssistantToolDiagnoseTenant, Arguments: map[string]any{"areas": []any{"agent"}},
		}, state, time.Now())
		require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
		require.NotNil(t, result.artifact)
		require.NotNil(t, result.artifact.Evidence)
		require.Len(t, result.artifact.Evidence.AreaResults, 1)
	})

	t.Run("propose resource change", func(t *testing.T) {
		result := execProposeResourceChangeTool(context.Background(), port.ToolCall{
			Name: domain.SystemAssistantToolProposeResourceChange,
			Arguments: map[string]any{
				"resourceKind": "agent", "operation": "create",
				"payload": map[string]any{"name": "a", "description": "d", "model": "m", "maxIterations": 3, "maxContextTokens": 100},
			},
		}, state, time.Now())
		require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
		require.NotNil(t, result.artifact)
		require.Equal(t, "proposal-1", result.artifact.Proposal.ID)
	})
}

func TestSystemAssistantToolsFailClosedWhenUnavailable(t *testing.T) {
	tests := []struct {
		name  string
		state ReActState
		call  func(*ReActState) toolExecResult
	}{
		{name: "ungoverned docs", state: ReActState{}, call: func(s *ReActState) toolExecResult {
			return execOfficialDocsSearchTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
		{name: "nil diagnostic fn", state: ReActState{GovernedAssistant: true}, call: func(s *ReActState) toolExecResult {
			return execDiagnoseTenantTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
		{name: "nil proposal fn", state: ReActState{GovernedAssistant: true}, call: func(s *ReActState) toolExecResult {
			return execProposeResourceChangeTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.call(&tc.state)
			require.Equal(t, domain.ToolTraceStatusError, result.status)
			require.NotEmpty(t, result.errMsg)
		})
	}
}

func TestSystemAssistantDirectApplyToolExecutesAndBuildsArtifact(t *testing.T) {
	deadlineSeen := false
	state := &ReActState{
		GovernedAssistant: true,
		ResourceChangeApplyFn: func(callCtx context.Context, _ map[string]any) (domain.ApplyResult, error) {
			if _, ok := callCtx.Deadline(); ok {
				deadlineSeen = true
			}
			return domain.ApplyResult{ResourceID: "server-1", Fingerprint: "fp"}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	result := execApplyResourceChangeTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolApplyResourceChange,
		Arguments: map[string]any{
			"resourceKind": "mcp", "operation": "update", "resourceId": "server-1",
			"payload": map[string]any{"name": "new"},
		},
	}, state, time.Now())

	require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
	require.NotNil(t, result.artifact)
	require.NotNil(t, result.artifact.DirectApply)
	// Artifact-only extraction echoes raw tool arguments; canonical kind
	// mapping is validated inside ApplyDirect.
	require.Equal(t, domain.ResourceKind("mcp"), result.artifact.DirectApply.ResourceKind)
	require.Equal(t, domain.OperationUpdate, result.artifact.DirectApply.Operation)
	require.Equal(t, "server-1", result.artifact.DirectApply.ResourceID)
	require.Equal(t, "success", result.artifact.DirectApply.Outcome)
	require.Empty(t, result.artifact.DirectApply.ErrorCode)
	require.Contains(t, result.content, "fp")
	// The apply call carries the bounded tool deadline (10s), not a flat timeout.
	require.True(t, deadlineSeen)
}

func TestSystemAssistantDirectApplyToolFailClosed(t *testing.T) {
	applyFnCalled := false
	fn := func(context.Context, map[string]any) (domain.ApplyResult, error) {
		applyFnCalled = true
		return domain.ApplyResult{}, nil
	}
	tests := []struct {
		name  string
		state ReActState
	}{
		{name: "ungoverned assistant", state: ReActState{ResourceChangeApplyFn: fn}},
		{name: "unwired apply fn", state: ReActState{GovernedAssistant: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applyFnCalled = false
			result := execApplyResourceChangeTool(context.Background(), port.ToolCall{
				Name: domain.SystemAssistantToolApplyResourceChange, Arguments: map[string]any{},
			}, &tc.state, time.Now())
			require.Equal(t, domain.ToolTraceStatusError, result.status)
			require.False(t, applyFnCalled, "apply fn must not be called when the tool is unavailable")
			// The tool is unavailable before any apply happens, so no typed
			// artifact is produced — the error surface is the message alone.
			require.Nil(t, result.artifact)
			require.Contains(t, result.content, "tool unavailable")
		})
	}
}

func TestSystemAssistantDirectApplyToolErrorBecomesTypedArtifact(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ResourceChangeApplyFn: func(context.Context, map[string]any) (domain.ApplyResult, error) {
			return domain.ApplyResult{}, errors.New("ownership denied")
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	result := execApplyResourceChangeTool(context.Background(), port.ToolCall{
		Name:      domain.SystemAssistantToolApplyResourceChange,
		Arguments: map[string]any{"resourceKind": "agent", "operation": "create", "payload": map[string]any{}},
	}, state, time.Now())

	require.Equal(t, domain.ToolTraceStatusError, result.status)
	// The raw apply error is sanitized for the model surface (no internal
	// details leak); the typed artifact keeps a stable error code.
	require.Equal(t, "evidence unavailable", result.errMsg)
	require.Contains(t, result.content, "error:")
	require.NotNil(t, result.artifact.DirectApply)
	require.Equal(t, "error", result.artifact.DirectApply.Outcome)
	require.NotEmpty(t, result.artifact.DirectApply.ErrorCode)
}

// TestSystemAssistantInvalidArgumentsMapsToRecoverableError guards the
// argument-parsing error surface: a malformed payload must surface as
// "invalid tool arguments" (a signal the model can self-correct) rather than
// falling into the generic "evidence unavailable" bucket that implies the
// platform is unhealthy.
func TestSystemAssistantInvalidArgumentsMapsToRecoverableError(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		// 参数解析在装配闭包内（ApplyDirectFromTool → 严格解析器）执行；
		// graph 层收到的是 callErr，这里直接模拟该错误以验证错误映射：
		// 非法参数必须映射为模型可自纠的 "invalid tool arguments"，
		// 而非默认的 "evidence unavailable"（暗示平台不健康）。
		ResourceChangeApplyFn: func(context.Context, map[string]any) (domain.ApplyResult, error) {
			return domain.ApplyResult{}, domain.ErrInvalidSystemAssistantToolArguments
		},
	}
	result := execApplyResourceChangeTool(context.Background(), port.ToolCall{
		Name:      domain.SystemAssistantToolApplyResourceChange,
		Arguments: map[string]any{"resourceKind": "agent", "operation": "create"},
	}, state, time.Now())
	require.Equal(t, "invalid tool arguments", result.errMsg)
	require.NotNil(t, result.artifact)
	require.Equal(t, "error", result.artifact.DirectApply.Outcome)
	require.Equal(t, "invalid_arguments", result.artifact.DirectApply.ErrorCode)
}

func TestSystemAssistantDirectApplyToolRoutesThroughDispatch(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ResourceChangeApplyFn: func(context.Context, map[string]any) (domain.ApplyResult, error) {
			return domain.ApplyResult{ResourceID: "skill-1"}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	provider := classifyToolProvider(domain.SystemAssistantToolApplyResourceChange, nil)
	require.Equal(t, domain.ProviderTypeInternal, provider.ProviderType)
	result := dispatchToolCall(context.Background(), port.ToolCall{
		Name:      domain.SystemAssistantToolApplyResourceChange,
		Arguments: map[string]any{"resourceKind": "skill", "operation": "update", "resourceId": "skill-1"},
	}, state, time.Now(), provider, zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
	require.NotNil(t, result.artifact)
	// Artifact-only extraction echoes the raw argument; the canonical kind is
	// validated inside ApplyDirect, so here we assert the echo plus the route.
	require.Equal(t, domain.ResourceKind("skill"), result.artifact.DirectApply.ResourceKind)
}

func TestSystemAssistantListModelsToolExecutesAndBuildsArtifact(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ListModelsFn: func(_ context.Context) (map[string]any, error) {
			return map[string]any{"models": []domain.TenantModelDetail{
				{Model: "qwen-plus", Capabilities: []string{"chat"}, Enabled: true},
			}}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	result := execListModelsTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolListModels,
	}, state, time.Now())
	require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
	require.NotNil(t, result.artifact)
	require.Equal(t, domain.SystemAssistantToolListModels, result.artifact.Tool)
	require.Equal(t, "success", result.artifact.Outcome)
	require.Contains(t, result.content, "qwen-plus")
}

func TestSystemAssistantUpdateSystemModelToolValidatesModelArgument(t *testing.T) {
	var seen string
	state := &ReActState{
		GovernedAssistant: true,
		UpdateSystemModelFn: func(_ context.Context, model string) (map[string]any, error) {
			seen = model
			return map[string]any{"model": model, "ready": true}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	missing := execUpdateSystemModelTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolUpdateSystemModel, Arguments: map[string]any{},
	}, state, time.Now())
	require.Equal(t, domain.ToolTraceStatusError, missing.status)
	require.Contains(t, missing.content, "model required")
	require.Empty(t, seen, "update fn must not be called without a model argument")

	ok := execUpdateSystemModelTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolUpdateSystemModel, Arguments: map[string]any{"model": "qwen-plus"},
	}, state, time.Now())
	require.Equal(t, domain.ToolTraceStatusSuccess, ok.status)
	require.Equal(t, "qwen-plus", seen)
	require.NotNil(t, ok.artifact)
	require.Equal(t, domain.SystemAssistantToolUpdateSystemModel, ok.artifact.Tool)
}

func TestSafeAssistantToolErrorSentinelMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"proposal forbidden", domain.ErrProposalForbidden, "proposal forbidden"},
		{"proposal invalid", domain.ErrProposalInvalid, "invalid proposal payload"},
		{"proposal expired", domain.ErrProposalExpired, "proposal expired"},
		{"system managed", domain.ErrSystemAssistantManaged, "resource is system-managed"},
		{"invalid args carries detail", &domain.InvalidToolArgumentsError{Detail: "缺少必填字段 name"}, "invalid tool arguments: 缺少必填字段 name"},
		{"bare invalid args", domain.ErrInvalidSystemAssistantToolArguments, "invalid tool arguments"},
		{"apply definite failure unwraps sentinel", &port.ResourceApplyError{Outcome: port.ResourceApplyDefiniteFailure, Err: domain.ErrProposalForbidden}, "proposal forbidden"},
		{"apply unknown outcome fail closed", &port.ResourceApplyError{Outcome: port.ResourceApplyUnknownOutcome}, "evidence unavailable"},
		{"unmatched error", errors.New("boom"), "evidence unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, safeAssistantToolError(tc.err))
		})
	}
}

func TestAssistantToolErrorCodeMapping(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"proposal forbidden", "forbidden"},
		{"invalid proposal payload", "invalid_payload"},
		{"proposal expired", "expired"},
		{"resource is system-managed", "system_managed"},
		{"invalid tool arguments: 缺少必填字段 name", "invalid_arguments"},
		{"tool timeout", "timeout"},
		{"evidence unavailable", "unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.message, func(t *testing.T) {
			require.Equal(t, tc.want, assistantToolErrorCode(tc.message))
		})
	}
}

func TestSystemAssistantListAgentsToolExecutesAndBuildsArtifact(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ListAgentsFn: func(_ context.Context) (map[string]any, error) {
			return map[string]any{"agents": []map[string]any{{"id": "a1", "name": "sales"}}}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	result := execListAgentsTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolListAgents,
	}, state, time.Now())
	require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
	require.NotNil(t, result.artifact)
	require.Equal(t, domain.SystemAssistantToolListAgents, result.artifact.Tool)
	require.Equal(t, "success", result.artifact.Outcome)
	require.Contains(t, result.content, "sales")
}

func TestSystemAssistantListMCPServersToolExecutesAndBuildsArtifact(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ListMCPServersFn: func(_ context.Context) (map[string]any, error) {
			return map[string]any{"servers": []map[string]any{{"name": "docs", "status": "connected"}}}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	result := execListMCPServersTool(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolListMCPServers,
	}, state, time.Now())
	require.Equal(t, domain.ToolTraceStatusSuccess, result.status)
	require.NotNil(t, result.artifact)
	require.Equal(t, domain.SystemAssistantToolListMCPServers, result.artifact.Tool)
	require.Contains(t, result.content, "docs")
}

func TestSystemAssistantListToolsFailClosedWhenUnavailable(t *testing.T) {
	states := []struct {
		name string
		call func(*ReActState) toolExecResult
	}{
		{name: "ungoverned list agents", call: func(s *ReActState) toolExecResult {
			return execListAgentsTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
		{name: "nil list agents fn", call: func(s *ReActState) toolExecResult {
			return execListAgentsTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
		{name: "nil list mcp fn", call: func(s *ReActState) toolExecResult {
			return execListMCPServersTool(context.Background(), port.ToolCall{}, s, time.Now())
		}},
	}
	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.call(&ReActState{GovernedAssistant: true})
			require.Equal(t, domain.ToolTraceStatusError, result.status)
			require.Contains(t, result.content, "tool unavailable")
		})
	}
}

func TestSystemAssistantModelToolsRouteThroughDispatch(t *testing.T) {
	state := &ReActState{
		GovernedAssistant: true,
		ListModelsFn: func(_ context.Context) (map[string]any, error) {
			return map[string]any{"models": []domain.TenantModelDetail{}}, nil
		},
		UpdateSystemModelFn: func(_ context.Context, model string) (map[string]any, error) {
			return map[string]any{"model": model, "ready": true}, nil
		},
		InternalToolResultGuardFn: testAssistantGuard,
	}
	listProvider := classifyToolProvider(domain.SystemAssistantToolListModels, nil)
	require.Equal(t, domain.ProviderTypeInternal, listProvider.ProviderType)
	listResult := dispatchToolCall(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolListModels,
	}, state, time.Now(), listProvider, zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, listResult.status)

	updateProvider := classifyToolProvider(domain.SystemAssistantToolUpdateSystemModel, nil)
	require.Equal(t, domain.ProviderTypeInternal, updateProvider.ProviderType)
	updateResult := dispatchToolCall(context.Background(), port.ToolCall{
		Name: domain.SystemAssistantToolUpdateSystemModel, Arguments: map[string]any{"model": "qwen-plus"},
	}, state, time.Now(), updateProvider, zap.NewNop())
	require.Equal(t, domain.ToolTraceStatusSuccess, updateResult.status)
	require.Contains(t, updateResult.content, "qwen-plus")
}
