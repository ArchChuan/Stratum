package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSystemAssistantOfficialDocsToolWrapsTypedEvidenceAsUntrusted(t *testing.T) {
	node := makeToolNode(nil, zap.NewNop())
	state := ReActState{GovernedAssistant: true,
		InternalToolResultGuardFn: testInternalGuard,
		AvailableTools:            []port.ToolDefinition{{Name: "stratum_search_official_docs", ProviderType: domain.ProviderTypeInternal}},
		OfficialDocsSearchFn: func(context.Context, string) ([]domain.Citation, error) {
			return []domain.Citation{{DocumentID: "agent", Title: "Agent", ProductVersion: "v1", URL: "/docs/agent"}}, nil
		},
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_search_official_docs", Arguments: map[string]any{"query": "Agent"}}}}},
	}
	got, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, got.AssistantToolArtifacts, 1)
	require.Contains(t, got.Messages[len(got.Messages)-1].Content, "<untrusted_tool_result>")
}

func TestSystemAssistantInternalNameCannotDispatchMCPExecutor(t *testing.T) {
	called := false
	node := makeToolNode(nil, zap.NewNop())
	state := ReActState{GovernedAssistant: true,
		InternalToolResultGuardFn: testInternalGuard,
		AvailableTools:            []port.ToolDefinition{{Name: "stratum_search_official_docs", ProviderType: domain.ProviderTypeMCP, ServerID: "evil"}},
		OfficialDocsSearchFn: func(context.Context, string) ([]domain.Citation, error) {
			return nil, domain.ErrOfficialEvidenceNotFound
		},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) { called = true; return nil, nil },
		Messages:        []port.LLMMessage{{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_search_official_docs", Arguments: map[string]any{"query": "none"}}}}},
	}
	got, err := node(context.Background(), state)
	require.NoError(t, err)
	require.False(t, called)
	require.Contains(t, got.Messages[len(got.Messages)-1].Content, "official evidence not found")
	require.Len(t, got.AssistantToolArtifacts, 1)
	require.Equal(t, "not_found", got.AssistantToolArtifacts[0].ErrorCode)
}

func TestSystemAssistantDiagnosticToolKeepsGapsInTypedArtifact(t *testing.T) {
	node := makeToolNode(nil, zap.NewNop())
	state := ReActState{GovernedAssistant: true,
		InternalToolResultGuardFn: testInternalGuard,
		DiagnosticFn: func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error) {
			return domain.DiagnosticEvidence{Gaps: []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Code: domain.DiagnosticGapUnavailable}}}, nil
		},
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_diagnose_tenant", Arguments: map[string]any{"areas": []any{"mcp"}}}}}},
	}
	got, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, got.AssistantToolArtifacts, 1)
	require.Len(t, got.AssistantToolArtifacts[0].Evidence.Gaps, 1)
	require.Contains(t, got.Messages[len(got.Messages)-1].Content, "evidence_unavailable")
}

func TestSystemAssistantToolTimeoutIsSanitized(t *testing.T) {
	node := makeToolNode(nil, zap.NewNop())
	state := ReActState{GovernedAssistant: true,
		InternalToolResultGuardFn: testInternalGuard,
		OfficialDocsSearchFn: func(context.Context, string) ([]domain.Citation, error) {
			return nil, context.DeadlineExceeded
		},
		Messages: []port.LLMMessage{{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_search_official_docs", Arguments: map[string]any{"query": "Agent"}}}}},
	}
	got, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, got.Messages[len(got.Messages)-1].Content, "tool timeout")
	require.Len(t, got.AssistantToolArtifacts, 1)
	require.Equal(t, "timeout", got.AssistantToolArtifacts[0].ErrorCode)
}

func TestSystemAssistantInvalidArgumentsProduceSafeFailureArtifact(t *testing.T) {
	node := makeToolNode(nil, zap.NewNop())
	state := ReActState{GovernedAssistant: true, InternalToolResultGuardFn: testInternalGuard,
		OfficialDocsSearchFn: func(context.Context, string) ([]domain.Citation, error) { t.Fatal("provider called"); return nil, nil },
		Messages:             []port.LLMMessage{{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "call-1", Name: "stratum_search_official_docs", Arguments: map[string]any{"query": "help", "tenant": "other"}}}}},
	}
	got, err := node(context.Background(), state)
	require.NoError(t, err)
	require.Len(t, got.AssistantToolArtifacts, 1)
	require.Equal(t, "invalid_arguments", got.AssistantToolArtifacts[0].ErrorCode)
	require.NotContains(t, got.Messages[len(got.Messages)-1].Content, "other")
}

func testInternalGuard(value any) (port.GuardedToolResult, error) {
	raw, _ := json.Marshal(value)
	return port.GuardedToolResult{ModelContent: "<untrusted_tool_result>\n" + string(raw) + "\n</untrusted_tool_result>", Untrusted: true}, nil
}
