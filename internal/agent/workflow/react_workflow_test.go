package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/workflow"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// mockCapGW is a simple controllable mock for CapabilityGateway.
type mockCapGW struct {
	responses []capgateway.CapabilityResponse
	errors    []error
	calls     []capgateway.CapabilityRequest
	idx       int
}

func (m *mockCapGW) Route(_ context.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	m.calls = append(m.calls, req)
	i := m.idx
	m.idx++
	if i < len(m.errors) && m.errors[i] != nil {
		return capgateway.CapabilityResponse{}, m.errors[i]
	}
	if i < len(m.responses) {
		return m.responses[i], nil
	}
	return capgateway.CapabilityResponse{}, errors.New("mock: no more responses")
}

type WorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	gw  *mockCapGW
}

func (s *WorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.gw = &mockCapGW{}
	deps := &workflow.ActivityDeps{CapGateway: s.gw}
	s.env.RegisterActivityWithOptions(deps.ExecuteCapabilityActivity, activity.RegisterOptions{
		Name: workflow.ExecuteCapabilityActivityName,
	})
}

func (s *WorkflowTestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *WorkflowTestSuite) TestReActWorkflow_DirectAnswer() {
	s.gw.responses = []capgateway.CapabilityResponse{
		{Type: capgateway.CapLLM, Content: "4"},
	}
	req := workflow.ReActRequest{
		TraceID:  "tr1",
		TenantID: "t1",
		Input:    "What is 2+2?",
		AgentCfg: workflow.AgentWorkflowConfig{ID: "agent1", LLMModel: "qwen-turbo", MaxIterations: 5},
	}

	s.env.ExecuteWorkflow(workflow.ReActWorkflow, req)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.NoError(s.T(), s.env.GetWorkflowError())
	var result workflow.ReActResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	require.Equal(s.T(), "4", result.Output)
	require.Equal(s.T(), 1, result.Steps)
	require.Len(s.T(), s.gw.calls, 1)
	require.Equal(s.T(), capgateway.CapLLM, s.gw.calls[0].Type)
}

func (s *WorkflowTestSuite) TestReActWorkflow_OneToolCall() {
	s.gw.responses = []capgateway.CapabilityResponse{
		// First LLM call: returns tool call
		{
			Type:      capgateway.CapLLM,
			ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "get_weather", Arguments: map[string]any{"city": "Beijing"}}},
		},
		// Skill execution
		{Type: capgateway.CapSkill, Output: "sunny, 25°C"},
		// Second LLM call: final answer
		{Type: capgateway.CapLLM, Content: "Beijing is sunny, 25°C"},
	}
	req := workflow.ReActRequest{
		TraceID:        "tr2",
		TenantID:       "t1",
		Input:          "Weather in Beijing?",
		AgentCfg:       workflow.AgentWorkflowConfig{ID: "agent1", LLMModel: "qwen-turbo", MaxIterations: 5},
		AvailableTools: []capgateway.ToolDefinition{{Name: "get_weather", Description: "weather"}},
	}

	s.env.ExecuteWorkflow(workflow.ReActWorkflow, req)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.NoError(s.T(), s.env.GetWorkflowError())
	var result workflow.ReActResult
	require.NoError(s.T(), s.env.GetWorkflowResult(&result))
	require.Equal(s.T(), "Beijing is sunny, 25°C", result.Output)
	require.Len(s.T(), result.ToolCalls, 1)
	require.Equal(s.T(), "get_weather", result.ToolCalls[0].Name)
	// LLM(1) + Skill(1) + LLM(2)
	require.Len(s.T(), s.gw.calls, 3)
	require.Equal(s.T(), capgateway.CapLLM, s.gw.calls[0].Type)
	require.Equal(s.T(), capgateway.CapSkill, s.gw.calls[1].Type)
	require.Equal(s.T(), "get_weather", s.gw.calls[1].Skill.SkillID)
	require.Equal(s.T(), capgateway.CapLLM, s.gw.calls[2].Type)
}

func (s *WorkflowTestSuite) TestReActWorkflow_MaxIterationsReached() {
	// Always returns tool calls → triggers max iterations
	toolResp := capgateway.CapabilityResponse{
		Type:      capgateway.CapLLM,
		ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "tool_a"}},
	}
	skillResp := capgateway.CapabilityResponse{Type: capgateway.CapSkill, Output: "ok"}
	s.gw.responses = []capgateway.CapabilityResponse{
		toolResp, skillResp,
		toolResp, skillResp,
	}
	req := workflow.ReActRequest{
		Input:    "loop forever",
		AgentCfg: workflow.AgentWorkflowConfig{MaxIterations: 2},
	}

	s.env.ExecuteWorkflow(workflow.ReActWorkflow, req)
	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
}

func TestWorkflowSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuite))
}
