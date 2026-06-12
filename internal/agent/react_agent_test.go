package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockCapGW drives LLM responses in sequence; tools always succeed.
type mockCapGW struct {
	mu        sync.Mutex
	responses []capgateway.CapabilityResponse
	idx       int
	toolResp  capgateway.CapabilityResponse
	err       error
}

func (m *mockCapGW) Route(_ context.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	if m.err != nil {
		return capgateway.CapabilityResponse{}, m.err
	}
	if req.Type == capgateway.CapSkill {
		return m.toolResp, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx < len(m.responses) {
		r := m.responses[m.idx]
		m.idx++
		return r, nil
	}
	return capgateway.CapabilityResponse{Content: "done"}, nil
}

func newReActAgent() *agent.BaseAgent {
	cfg := &agent.AgentConfig{
		ID:            "agent-001",
		Name:          "test-agent",
		Type:          agent.ReActAgent,
		LLMModel:      "qwen-turbo",
		SystemPrompt:  "You are helpful.",
		MaxIterations: 5,
	}
	return agent.NewBaseAgent(cfg, zap.NewNop())
}

func TestBaseAgent_ReActExecute_DirectAnswer(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []capgateway.CapabilityResponse{{Content: "42"}}}
	a.SetCapGateway(gw)

	result, err := a.Execute(context.Background(), "what is 6x7?",
		agent.WithTenantID("t1"),
	)
	require.NoError(t, err)
	require.Equal(t, "42", result.Output)
	require.Equal(t, "agent-001", result.AgentID)
	require.Equal(t, 1, result.Steps)
}

func TestBaseAgent_ReActExecute_WithToolCall(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []capgateway.CapabilityResponse{
			{ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
		toolResp: capgateway.CapabilityResponse{Content: "42"},
	}
	a.SetCapGateway(gw)

	result, err := a.Execute(context.Background(), "calc 6*7",
		agent.WithTenantID("t1"),
		agent.WithMaxSteps(10),
	)
	require.NoError(t, err)
	require.Equal(t, "The answer is 42", result.Output)
	require.Equal(t, 2, result.Steps)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "calc", result.ToolCalls[0].ToolName)
}

func TestBaseAgent_ReActExecute_CapGWNil(t *testing.T) {
	a := newReActAgent()
	// no SetCapGateway call → CapGateway is nil

	_, err := a.Execute(context.Background(), "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CapGateway not set")
}

func TestBaseAgent_ReActExecute_LLMError(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{err: errors.New("llm unavailable")}
	a.SetCapGateway(gw)

	_, err := a.Execute(context.Background(), "hello")
	require.Error(t, err)
}

func TestBaseAgent_SetCapGateway_DataRace(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []capgateway.CapabilityResponse{{Content: "ok"}}}
	var wg sync.WaitGroup
	// concurrent SetCapGateway + Execute
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.SetCapGateway(gw)
		}()
		go func() {
			defer wg.Done()
			_, _ = a.Execute(context.Background(), "ping")
		}()
	}
	wg.Wait()
}
