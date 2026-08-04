package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
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
