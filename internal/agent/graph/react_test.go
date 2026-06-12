package graph_test

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/graph"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
)

// capGWSequence drives LLM responses in sequence; tool always returns fixed resp.
type capGWSequence struct {
	responses []capgateway.CapabilityResponse
	idx       int
	// non-zero infinite means return this after the sequence is exhausted
	infinite capgateway.CapabilityResponse
	toolResp capgateway.CapabilityResponse
}

func (s *capGWSequence) Route(_ context.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	if req.Type == capgateway.CapSkill {
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

func (s *slowCapGW) Route(ctx context.Context, _ capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	select {
	case <-ctx.Done():
		return capgateway.CapabilityResponse{}, ctx.Err()
	case <-time.After(s.delay):
		return capgateway.CapabilityResponse{Content: "slow"}, nil
	}
}

type errCapGW struct{ err error }

func (e *errCapGW) Route(_ context.Context, _ capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	return capgateway.CapabilityResponse{}, e.err
}

func TestBuildReActGraph_DirectAnswer(t *testing.T) {
	stub := &capGWSequence{
		responses: []capgateway.CapabilityResponse{{Content: "42"}},
	}
	cg, err := graph.BuildReActGraph(stub)
	require.NoError(t, err)

	state := graph.ReActState{
		TenantID:     "t1",
		Model:        "qwen-turbo",
		SystemPrompt: "You are helpful.",
		Messages:     []capgateway.LLMMessage{{Role: "user", Content: "what is 6x7?"}},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, "42", out.Output)
	require.Equal(t, 1, out.Steps)
}

func TestBuildReActGraph_ToolCall(t *testing.T) {
	stub := &capGWSequence{
		responses: []capgateway.CapabilityResponse{
			{ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
		toolResp: capgateway.CapabilityResponse{Content: "42"},
	}
	cg, err := graph.BuildReActGraph(stub)
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []capgateway.LLMMessage{{Role: "user", Content: "calc 6*7"}},
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, "The answer is 42", out.Output)
	require.Equal(t, 2, out.Steps)
	require.Len(t, out.AllToolCalls, 1)
	require.Equal(t, "calc", out.AllToolCalls[0].Name)
}

func TestBuildReActGraph_MaxIterations(t *testing.T) {
	// LLM always returns a tool call → loop until max steps hit
	stub := &capGWSequence{
		infinite: capgateway.CapabilityResponse{
			ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "noop", Arguments: map[string]any{}}},
		},
		toolResp: capgateway.CapabilityResponse{Content: "ok"},
	}
	cg, err := graph.BuildReActGraph(stub)
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []capgateway.LLMMessage{{Role: "user", Content: "loop"}},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 4})
	require.ErrorContains(t, err, "max steps")
}

func TestBuildReActGraph_LLMError(t *testing.T) {
	stub := &errCapGW{err: context.DeadlineExceeded}
	cg, err := graph.BuildReActGraph(stub)
	require.NoError(t, err)

	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}},
	}
	_, err = cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.Error(t, err)
}

func TestBuildReActGraph_ContextTimeout(t *testing.T) {
	stub := &slowCapGW{delay: 200 * time.Millisecond}
	cg, err := graph.BuildReActGraph(stub)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	state := graph.ReActState{
		Model:    "qwen-turbo",
		Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}},
	}
	_, err = cg.Invoke(ctx, state, graph.RunConfig{MaxSteps: 5})
	require.Error(t, err)
}
