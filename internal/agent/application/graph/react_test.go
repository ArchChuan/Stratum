package graph_test

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// capGWSequence drives LLM responses in sequence; tool always returns fixed resp.
type capGWSequence struct {
	responses []port.CapabilityResponse
	idx       int
	// non-zero infinite means return this after the sequence is exhausted
	infinite  port.CapabilityResponse
	toolResp  port.CapabilityResponse
	skillReqs []port.SkillCapRequest
}

func (s *capGWSequence) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.Type == port.CapSkill {
		if req.Skill != nil {
			s.skillReqs = append(s.skillReqs, *req.Skill)
		}
		return s.toolResp, nil
	}
	if s.idx < len(s.responses) {
		r := s.responses[s.idx]
		s.idx++
		return r, nil
	}
	return s.infinite, nil
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
		toolResp: port.CapabilityResponse{Content: "42"},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "calc 6*7"}},
		SkillToolIndex: map[string]port.SkillToolRef{"calc": {SkillID: "skill-calc"}},
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
	require.Equal(t, "skill", out.ToolObservations[0].ProviderType)
	require.Equal(t, "skill-calc", out.ToolObservations[0].ProviderID)
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
		toolResp: port.CapabilityResponse{Content: "mcp result"},
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
		}},
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
	require.NotEmpty(t, stub.skillReqs)
	require.Equal(t, "mcp_search", stub.skillReqs[0].SkillID)
}

func TestBuildReActGraph_ToolCallPassesSkillVersionID(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
		toolResp: port.CapabilityResponse{Content: "42"},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "calc 6*7"}},
		SkillToolIndex: map[string]port.SkillToolRef{
			"calc": {SkillID: "skill-calc", VersionID: "version-1"},
		},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Len(t, stub.skillReqs, 1)
	require.Equal(t, "skill-calc", stub.skillReqs[0].SkillID)
	require.Equal(t, "version-1", stub.skillReqs[0].VersionID)
}

func TestBuildReActGraph_MaxIterations(t *testing.T) {
	// LLM always returns a tool call → loop until max steps hit
	stub := &capGWSequence{
		infinite: port.CapabilityResponse{
			ToolCalls: []port.ToolCall{{ID: "c1", Name: "noop", Arguments: map[string]any{}}},
		},
		toolResp: port.CapabilityResponse{Content: "ok"},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "loop"}},
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
		toolResp: port.CapabilityResponse{Content: "ok"},
	}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []port.LLMMessage{{Role: "user", Content: "go"}},
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
