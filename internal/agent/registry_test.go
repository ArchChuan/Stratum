package agent

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

type mockAgent struct {
	config *AgentConfig
}

func (m *mockAgent) GetConfig() *AgentConfig {
	return m.config
}

func (m *mockAgent) Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error) {
	return &AgentResult{
		AgentID: m.config.ID,
		Input:   input,
		Output:  "mock result",
	}, nil
}

func (m *mockAgent) Reset() {
}

func (m *mockAgent) GetMemory() []Message {
	return []Message{}
}

func TestNewRegistry(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	if registry == nil {
		t.Error("expected registry to be non-nil")
	}

	if registry.agents == nil {
		t.Error("expected agents map to be non-nil")
	}

	if len(registry.agents) != 0 {
		t.Errorf("expected empty agents map, got %d agents", len(registry.agents))
	}
}

func TestRegisterAgent(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	agent := &mockAgent{
		config: &AgentConfig{
			ID:   "test-agent",
			Name: "Test Agent",
			Type: ReActAgent,
		},
	}

	err := registry.Register(agent)
	if err != nil {
		t.Errorf("Register() failed: %v", err)
	}

	if len(registry.agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(registry.agents))
	}
}

func TestRegisterDuplicateAgent(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	agent := &mockAgent{
		config: &AgentConfig{
			ID:   "test-agent",
			Name: "Test Agent",
			Type: ReActAgent,
		},
	}

	err := registry.Register(agent)
	if err != nil {
		t.Errorf("first Register() failed: %v", err)
	}

	err = registry.Register(agent)
	if err == nil {
		t.Error("expected error when registering duplicate agent")
	}
}

func TestGetAgent(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	agent := &mockAgent{
		config: &AgentConfig{
			ID:   "test-agent",
			Name: "Test Agent",
			Type: ReActAgent,
		},
	}

	registry.Register(agent)

	retrieved, ok := registry.Get("test-agent")
	if !ok {
		t.Error("expected to find agent")
	}

	if retrieved.GetConfig().ID != "test-agent" {
		t.Errorf("expected agent ID test-agent, got %s", retrieved.GetConfig().ID)
	}
}

func TestGetNonexistentAgent(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent agent")
	}
}

func TestListAgents(t *testing.T) {
	logger := zap.NewNop()
	registry := NewRegistry(logger)

	agent1 := &mockAgent{
		config: &AgentConfig{ID: "agent-1", Name: "Agent 1", Type: ReActAgent},
	}
	agent2 := &mockAgent{
		config: &AgentConfig{ID: "agent-2", Name: "Agent 2", Type: CoTAgent},
	}

	registry.Register(agent1)
	registry.Register(agent2)

	agents := registry.GetAll()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}
