package graph_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBuildReActGraph_DestructiveToolPausesBeforeExecution(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{ToolCalls: []port.ToolCall{{
		ID: "delete-1", Name: "delete_order", Arguments: map[string]any{"id": "o1"},
	}}}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	guardCalled := false
	state := graph.ReActState{
		TenantID: "tenant-1", TraceID: "trace-1", Model: "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "delete order"}},
		AvailableTools: []port.ToolDefinition{{
			Name: "delete_order", ProviderType: "mcp", ServerID: "orders", CapabilityID: "delete_order",
			Metadata: map[string]any{"risk_level": "destructive"},
		}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			guardCalled = true
			return nil, &port.ToolApprovalRequiredError{ToolName: "delete_order"}
		},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	var approvalErr *port.ToolApprovalRequiredError
	require.True(t, errors.As(err, &approvalErr))
	require.Equal(t, "delete_order", approvalErr.ToolName)
	require.True(t, guardCalled)
}

func TestBuildReActGraph_ForgedToolCallUsesExecutionGuard(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{ToolCalls: []port.ToolCall{{
		ID: "forged-1", Name: "mcp:orders:delete", Arguments: map[string]any{"id": "order-1"},
	}}}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	guardCalls := 0

	_, err = cg.Invoke(context.Background(), graph.ReActState{
		TenantID: "tenant-1", Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "run"}},
		AvailableTools: []port.ToolDefinition{{
			Name: "mcp:orders:delete", ProviderType: "mcp", ServerID: "orders", CapabilityID: "delete",
		}},
		ToolExecutionFn: func(_ context.Context, request port.ToolExecutionRequest) (any, error) {
			guardCalls++
			require.Equal(t, "forged-1", request.ToolCallID)
			return nil, &port.ToolApprovalRequiredError{ApprovalID: "approval-1"}
		},
	}, graph.RunConfig{MaxSteps: 5})

	var approvalErr *port.ToolApprovalRequiredError
	require.ErrorAs(t, err, &approvalErr)
	require.Equal(t, 1, guardCalls)
}

func TestBuildReActGraph_FinalInstructionFitsContextBudget(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	messages := []port.LLMMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "task"},
	}
	for i := 0; i < 8; i++ {
		messages = append(messages, port.LLMMessage{Role: "assistant", Content: strings.Repeat("x", 700)})
	}

	const budget = 800
	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model:            "qwen",
		Messages:         messages,
		MaxLLMSteps:      1,
		MaxContextTokens: budget,
	}, graph.RunConfig{MaxSteps: 2})
	require.NoError(t, err)
	require.Len(t, stub.llmReqs, 1)

	reqMessages := stub.llmReqs[0].Messages
	joined, err := json.Marshal(reqMessages)
	require.NoError(t, err)
	require.Contains(t, string(joined), "maximum reasoning steps")

	estimate := make([]tokenutil.Message, len(reqMessages))
	for i, message := range reqMessages {
		estimate[i] = tokenutil.Message{Role: message.Role, Content: message.Content}
	}
	wantMax := int(float64(budget) * constants.LoopCompactionSafetyRatio)
	require.LessOrEqual(t, tokenutil.EstimateMessages(estimate), wantMax)
}

func TestBuildReActGraph_FinalInstructionDoesNotReplaceCurrentTask(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	messages := []port.LLMMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "old request"}}
	for i := 0; i < 8; i++ {
		messages = append(messages, port.LLMMessage{Role: "assistant", Content: strings.Repeat("history", 120)})
	}
	messages = append(messages, port.LLMMessage{Role: "user", Content: "CURRENT TASK"})

	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: messages, MaxLLMSteps: 1, MaxContextTokens: 800,
	}, graph.RunConfig{MaxSteps: 2})
	require.NoError(t, err)
	reqMessages := stub.llmReqs[0].Messages
	encoded, err := json.Marshal(reqMessages)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "CURRENT TASK")
	require.Contains(t, string(encoded), "maximum reasoning steps")
	require.Contains(t, reqMessages[len(reqMessages)-1].Content, "maximum reasoning steps")
}

func TestBuildReActGraph_ReservesContextBudgetForToolSchemas(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	messages := []port.LLMMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "task"}}
	for i := 0; i < 8; i++ {
		messages = append(messages, port.LLMMessage{Role: "assistant", Content: strings.Repeat("x", 700)})
	}
	tools := []port.ToolDefinition{{
		Name: "search", Description: strings.Repeat("tool description ", 30),
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "description": strings.Repeat("query details ", 30)},
		}},
	}}

	const budget = 1000
	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: messages, AvailableTools: tools, MaxContextTokens: budget,
	}, graph.RunConfig{MaxSteps: 2})
	require.NoError(t, err)
	require.Len(t, stub.llmReqs, 1)

	req := stub.llmReqs[0]
	estimate := make([]tokenutil.Message, len(req.Messages))
	for i, message := range req.Messages {
		estimate[i] = tokenutil.Message{Role: message.Role, Content: message.Content}
	}
	toolJSON, err := json.Marshal(req.Tools)
	require.NoError(t, err)
	total := tokenutil.EstimateMessages(estimate) + tokenutil.EstimateText(string(toolJSON))
	require.LessOrEqual(t, total, int(float64(budget)*constants.LoopCompactionSafetyRatio))
}

func TestBuildReActGraph_DropsToolsThatConsumeMessageAllowance(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	const budget = 800
	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", MaxContextTokens: budget,
		Messages: []port.LLMMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "CURRENT TASK"}},
		AvailableTools: []port.ToolDefinition{{
			Name: "oversized", Description: strings.Repeat("schema", 1000),
			InputSchema: map[string]any{"type": "object"},
		}},
	}, graph.RunConfig{MaxSteps: 2})
	require.NoError(t, err)
	require.Empty(t, stub.llmReqs[0].Tools)
	require.Equal(t, "CURRENT TASK", stub.llmReqs[0].Messages[len(stub.llmReqs[0].Messages)-1].Content)
}

func TestBuildReActGraph_KeepsLargeToolWhenActualPromptStillFits(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{Content: "done"}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", MaxContextTokens: 1000,
		Messages: []port.LLMMessage{{Role: "user", Content: "short task"}},
		AvailableTools: []port.ToolDefinition{{
			Name: "large_but_usable", Description: strings.Repeat("schema", 310),
			InputSchema: map[string]any{"type": "object"},
		}},
	}, graph.RunConfig{MaxSteps: 2})
	require.NoError(t, err)
	require.Len(t, stub.llmReqs[0].Tools, 1)
}

func TestBuildReActGraph_UnclassifiedToolAlsoRequiresApproval(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{{ToolCalls: []port.ToolCall{{ID: "call-1", Name: "unknown_risk"}}}}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen-turbo", Messages: []port.LLMMessage{{Role: "user", Content: "run"}},
		AvailableTools: []port.ToolDefinition{{Name: "unknown_risk", ProviderType: "mcp", ServerID: "server"}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			return nil, &port.ToolApprovalRequiredError{}
		},
	}, graph.RunConfig{MaxSteps: 5})
	var approvalErr *port.ToolApprovalRequiredError
	require.True(t, errors.As(err, &approvalErr))
}

func TestBuildReActGraph_ApprovedDestructiveToolUsesExecutionGuardOnce(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "new-call", Name: "delete_order", Arguments: map[string]any{"id": "o1"}}}},
		{Content: "deleted"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	guardCalls := 0
	out, err := cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "delete"}},
		AvailableTools: []port.ToolDefinition{{Name: "delete_order", ProviderType: "mcp", ServerID: "orders", CapabilityID: "delete_order", Metadata: map[string]any{"risk_level": "destructive"}}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			guardCalls++
			return guardedToolOutput("ok"), nil
		},
	}, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, "deleted", out.Output)
	require.Equal(t, 1, guardCalls)
}

// capGWSequence drives LLM responses in sequence; tool always returns fixed resp.
type capGWSequence struct {
	responses []port.CapabilityResponse
	idx       int
	// non-zero infinite means return this after the sequence is exhausted
	infinite port.CapabilityResponse
	llmReqs  []port.LLMCapRequest
}

func (s *capGWSequence) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.LLM != nil {
		s.llmReqs = append(s.llmReqs, *req.LLM)
	}
	if s.idx < len(s.responses) {
		r := s.responses[s.idx]
		s.idx++
		return r, nil
	}
	return s.infinite, nil
}

func TestBuildReActGraph_ActivatesSingleInstructionSkillAndNarrowsMCPTools(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "activate-1", Name: "stratum_activate_skill", Arguments: map[string]any{"skill_id": "skill-a"}}}},
		{ToolCalls: []port.ToolCall{{ID: "activate-2", Name: "stratum_activate_skill", Arguments: map[string]any{"skill_id": "skill-b"}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "complete task"}},
		AvailableTools: []port.ToolDefinition{
			{Name: "mcp:orders:get", ProviderType: "mcp"},
			{Name: "mcp:orders:delete", ProviderType: "mcp"},
			{Name: "stratum_recall_memory", ProviderType: "builtin"},
		},
		AgentMemoryScope: "user",
		SkillCatalog: map[string]port.SkillActivation{
			"skill-a": {SkillID: "skill-a", RevisionID: "revision-a", Instructions: "USE INSTRUCTION A", MCPToolIDs: []string{"mcp:orders:get"}, MemoryScopes: []string{"user"}},
			"skill-b": {SkillID: "skill-b", RevisionID: "revision-b", Instructions: "USE INSTRUCTION B", MCPToolIDs: []string{"mcp:orders:delete"}, MemoryScopes: []string{"conversation"}},
		},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, "skill-b", out.ActiveSkill.SkillID)
	require.Len(t, stub.llmReqs, 3)

	secondMessages, _ := json.Marshal(stub.llmReqs[1].Messages)
	require.Contains(t, string(secondMessages), "USE INSTRUCTION A")
	require.NotContains(t, string(secondMessages), "USE INSTRUCTION B")
	require.Equal(t, []string{"stratum_activate_skill", "mcp:orders:get", "stratum_recall_memory"}, toolNames(stub.llmReqs[1].Tools))

	thirdMessages, _ := json.Marshal(stub.llmReqs[2].Messages)
	require.Contains(t, string(thirdMessages), "USE INSTRUCTION B")
	require.NotContains(t, string(thirdMessages), "USE INSTRUCTION A")
	require.Equal(t, []string{"stratum_activate_skill", "mcp:orders:delete"}, toolNames(stub.llmReqs[2].Tools))
}

func TestBuildReActGraph_ActiveSkillIntersectsKnowledgeWorkspaces(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "a1", Name: "stratum_activate_skill", Arguments: map[string]any{"skill_id": "skill-a"}}}},
		{ToolCalls: []port.ToolCall{{ID: "k1", Name: "stratum_search_knowledge", Arguments: map[string]any{"workspaces": []any{"kb-allowed", "kb-agent-only", "kb-skill-only"}, "query": "q"}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)
	var searched []string
	_, err = cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "search"}},
		AvailableTools:             []port.ToolDefinition{{Name: "stratum_search_knowledge", ProviderType: "builtin"}},
		AgentKnowledgeWorkspaceIDs: []string{"kb-allowed", "kb-agent-only"},
		SkillCatalog:               map[string]port.SkillActivation{"skill-a": {SkillID: "skill-a", KnowledgeWorkspaceIDs: []string{"kb-allowed", "kb-skill-only"}}},
		RAGSearchFn: func(_ context.Context, workspaces []string, _ string, _ int) (string, error) {
			searched = workspaces
			return "result", nil
		},
	}, graph.RunConfig{MaxSteps: 8})
	require.NoError(t, err)
	require.Equal(t, []string{"kb-allowed"}, searched)
}

func toolNames(tools []port.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i := range tools {
		names[i] = tools[i].Name
	}
	return names
}

type slowCapGW struct{ delay time.Duration }

func (s *slowCapGW) Route(ctx context.Context, _ port.CapabilityRequest) (port.CapabilityResponse, error) {
	select {
	case <-ctx.Done():
		return port.CapabilityResponse{}, ctx.Err()
	case <-time.After(s.delay):
		return port.CapabilityResponse{Content: "slow"}, nil
	}
}

type errCapGW struct{ err error }

func (e *errCapGW) Route(_ context.Context, _ port.CapabilityRequest) (port.CapabilityResponse, error) {
	return port.CapabilityResponse{}, e.err
}

func TestBuildReActGraph_DirectAnswer(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{{Content: "42"}},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		TenantID: "t1",
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "what is 6x7?"}},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, "42", out.Output)
	require.Equal(t, 1, out.Steps)
}

func TestBuildReActGraph_ToolCall(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "calc 6*7"}},
		AvailableTools: []port.ToolDefinition{{Name: "calc", ProviderType: "mcp", ProviderID: "math", ServerID: "math", Metadata: map[string]any{"risk_level": "read"}}},
		ToolExecutionFn: func(_ context.Context, request port.ToolExecutionRequest) (any, error) {
			require.Equal(t, "math", request.Tool.ServerID)
			require.Equal(t, "calc", request.Tool.CapabilityID)
			return guardedToolOutput("42"), nil
		},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, "The answer is 42", out.Output)
	require.Equal(t, 2, out.Steps)
	require.Len(t, out.AllToolCalls, 1)
	require.Equal(t, "calc", out.AllToolCalls[0].Name)
	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, "c1", out.ToolObservations[0].ToolCallID)
	require.Equal(t, "calc", out.ToolObservations[0].ToolName)
	require.Equal(t, "success", out.ToolObservations[0].Status)
	require.Equal(t, "42", out.ToolObservations[0].RawText)
	require.Equal(t, "mcp", out.ToolObservations[0].ProviderType)
	require.Equal(t, "math", out.ToolObservations[0].ProviderID)
	require.Equal(t, "agent", out.TraceEvents[0].RunType)
	require.NotEmpty(t, out.ToolObservations[0].Summary)
	require.NotEmpty(t, out.TraceEvents)
}

func TestBuildReActGraph_MCPToolCallRecordsProviderMetadata(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "mcp-call-1", Name: "mcp_search", Arguments: map[string]any{"query": "status"}}}},
			{Content: "Done"},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "use mcp"}},
		AvailableTools: []port.ToolDefinition{{
			Name:         "mcp_search",
			Description:  "search through mcp",
			InputSchema:  map[string]any{"type": "object"},
			ProviderType: "mcp",
			ProviderID:   "server-1",
			ServerID:     "server-1",
			Metadata:     map[string]any{"risk_level": "read"},
		}},
		ToolExecutionFn: func(_ context.Context, request port.ToolExecutionRequest) (any, error) {
			require.Equal(t, "server-1", request.Tool.ServerID)
			require.Equal(t, "mcp_search", request.Tool.CapabilityID)
			require.Equal(t, "status", request.Arguments["query"])
			return guardedToolOutput("mcp result"), nil
		},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, "mcp", out.ToolObservations[0].ProviderType)
	require.Equal(t, "server-1", out.ToolObservations[0].ProviderID)
	require.Equal(t, "server-1", out.ToolObservations[0].ServerID)
	require.Equal(t, "mcp", out.ToolObservations[0].ToolType)
	require.NotEmpty(t, out.TraceEvents)
	require.Equal(t, "mcp_search", out.TraceEvents[3].NodeID)
	require.Equal(t, "mcp", out.TraceEvents[3].NodeType)
}

func TestBuildReActGraph_MaxIterations(t *testing.T) {
	// LLM always returns a tool call → loop until max steps hit
	stub := &capGWSequence{
		infinite: port.CapabilityResponse{
			ToolCalls: []port.ToolCall{{ID: "c1", Name: "noop", Arguments: map[string]any{}}},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "loop"}},
		AvailableTools: []port.ToolDefinition{{Name: "noop", ProviderType: "mcp", ServerID: "test", Metadata: map[string]any{"risk_level": "read"}}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			return guardedToolOutput("ok"), nil
		},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 4})
	require.ErrorContains(t, err, "max steps")
}

func TestBuildReActGraph_LLMError(t *testing.T) {
	stub := &errCapGW{err: context.DeadlineExceeded}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "hi"}},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.Error(t, err)
}

func TestBuildReActGraph_TokensAccumulated(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{Content: "result", Usage: port.TokenUsage{Prompt: 10, Completion: 5, Total: 15}},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "hi"}},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, 15, out.TotalTokens)
}

func TestBuildReActGraph_TokensAccumulatedOverMultipleSteps(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{}}}, Usage: port.TokenUsage{Total: 20}},
			{Content: "done", Usage: port.TokenUsage{Total: 10}},
		},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "go"}},
		AvailableTools: []port.ToolDefinition{{Name: "calc", ProviderType: "mcp", ServerID: "test", Metadata: map[string]any{"risk_level": "read"}}},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			return guardedToolOutput("ok"), nil
		},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, 30, out.TotalTokens)
}

func TestBuildReActGraph_ContextTimeout(t *testing.T) {
	stub := &slowCapGW{delay: 200 * time.Millisecond}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "hi"}},
	}
	_, err = cg.Invoke(ctx, state, graph.RunConfig{MaxSteps: 5})
	require.Error(t, err)
}

func guardedToolOutput(content string) port.GuardedToolResult {
	return port.GuardedToolResult{ModelContent: content, Summary: content, Untrusted: true}
}
