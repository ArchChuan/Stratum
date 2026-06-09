package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	agentworkflow "github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/workflow"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

// mockWorkflowRun implements client.WorkflowRun.
type mockWorkflowRun struct {
	result *agentworkflow.ReActResult
	err    error
}

func (m *mockWorkflowRun) GetID() string    { return "test-wf-id" }
func (m *mockWorkflowRun) GetRunID() string { return "test-run-id" }
func (m *mockWorkflowRun) Get(_ context.Context, valuePtr interface{}) error {
	if m.err != nil {
		return m.err
	}
	if p, ok := valuePtr.(**agentworkflow.ReActResult); ok {
		*p = m.result
	}
	return nil
}
func (m *mockWorkflowRun) GetWithOptions(ctx context.Context, valuePtr interface{}, _ client.WorkflowRunGetOptions) error {
	return m.Get(ctx, valuePtr)
}

// mockTemporalClient implements agent.TemporalWorkflowStarter.
type mockTemporalClient struct {
	run client.WorkflowRun
	err error
}

func (m *mockTemporalClient) ExecuteWorkflow(_ context.Context, _ client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
	return m.run, m.err
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

	wfResult := &agentworkflow.ReActResult{
		Output: "42",
		Steps:  1,
	}
	mockClient := &mockTemporalClient{run: &mockWorkflowRun{result: wfResult}}
	mockGW := &mockCapGateway{}

	a.SetTemporalClient(mockClient)
	a.SetCapGateway(mockGW)

	result, err := a.Execute(context.Background(), "what is 6x7?")
	require.NoError(t, err)
	require.Equal(t, "42", result.Output)
	require.Equal(t, "agent-001", result.AgentID)
}

func TestBaseAgent_ReActExecute_TemporalError(t *testing.T) {
	a := newReActAgent()

	mockClient := &mockTemporalClient{err: errors.New("temporal unavailable")}
	mockGW := &mockCapGateway{}

	a.SetTemporalClient(mockClient)
	a.SetCapGateway(mockGW)

	_, err := a.Execute(context.Background(), "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "temporal unavailable")
}

// mockCapGateway satisfies capgateway.CapabilityGateway (unused in these tests but required by SetCapGateway).
type mockCapGateway struct{}

func (m *mockCapGateway) Route(_ context.Context, _ capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	return capgateway.CapabilityResponse{}, nil
}
