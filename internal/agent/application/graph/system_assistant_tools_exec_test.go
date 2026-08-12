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
