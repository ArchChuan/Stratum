package application_test

import (
	"context"
	"testing"
	"time"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	"go.uber.org/zap"
)

func TestCodeSkillCreation(t *testing.T) {
	cs := code.NewCodeSkill("test-1", "Test Code Skill", "A test code skill", "print('hello')", "python")

	if cs.GetID() != "test-1" {
		t.Errorf("expected ID test-1, got %s", cs.GetID())
	}

	if cs.GetName() != "Test Code Skill" {
		t.Errorf("expected name Test Code Skill, got %s", cs.GetName())
	}

	if cs.Language != "python" {
		t.Errorf("expected language python, got %s", cs.Language)
	}
}

func TestLLMSkillCreation(t *testing.T) {
	logger := zap.NewNop()
	gateway := llmgateway.NewGateway()
	ls := executors.NewLLMSkill("llm-1", "Test LLM Skill", "A test LLM skill", "", "", 0, 0, gateway, logger)

	if ls.GetID() != "llm-1" {
		t.Errorf("expected ID llm-1, got %s", ls.GetID())
	}

	if ls.GetName() != "Test LLM Skill" {
		t.Errorf("expected name Test LLM Skill, got %s", ls.GetName())
	}

	if ls.GetType() != "llm" {
		t.Errorf("expected type llm, got %s", ls.GetType())
	}
}

func TestExecutor(t *testing.T) {
	registry := &mockRegistry{
		skills: make(map[string]domain.Skill),
	}

	cs := code.NewCodeSkill("test-1", "Test", "Test", "code", "python")
	registry.skills["test-1"] = cs

	executor := application.NewExecutor(registry)

	ctx := application.ExecutionContext{
		SkillID: "test-1",
		Input:   map[string]interface{}{"test": "input"},
		Timeout: 5 * time.Second,
	}

	result := executor.Execute(ctx)

	if result.SkillID != "test-1" {
		t.Errorf("expected skill ID test-1, got %s", result.SkillID)
	}

	if result.Error != nil {
		t.Errorf("expected no error, got %v", result.Error)
	}
}

func TestExecutorTimeout(t *testing.T) {
	registry := &mockRegistry{
		skills: make(map[string]domain.Skill),
	}

	cs := &slowSkill{
		BaseSkill: &domain.BaseSkill{
			ID:   "slow-1",
			Name: "Slow Skill",
			Type: "code",
		},
	}
	registry.skills["slow-1"] = cs

	executor := application.NewExecutor(registry)

	ctx := application.ExecutionContext{
		SkillID: "slow-1",
		Input:   "test",
		Timeout: 100 * time.Millisecond,
	}

	result := executor.Execute(ctx)

	if result.Error == nil {
		t.Error("expected timeout error")
	}
}

func TestExecutorNotFound(t *testing.T) {
	registry := &mockRegistry{
		skills: make(map[string]domain.Skill),
	}

	executor := application.NewExecutor(registry)

	ctx := application.ExecutionContext{
		SkillID: "nonexistent",
		Input:   map[string]interface{}{},
		Timeout: 5 * time.Second,
	}

	result := executor.Execute(ctx)

	if result.Error == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestCodeSkillExecute(t *testing.T) {
	cs := code.NewCodeSkill("test-1", "Test", "Test", "code", "python")

	output, err := cs.Execute(context.Background(), map[string]interface{}{"key": "value"})
	if err != nil {
		t.Errorf("Execute() failed: %v", err)
	}

	if output == nil {
		t.Error("expected non-nil output")
	}
}

func TestLLMSkillExecute(t *testing.T) {
	logger := zap.NewNop()
	gateway := llmgateway.NewGateway()
	ls := executors.NewLLMSkill("llm-1", "Test", "Test", "", "", 0, 0, gateway, logger)

	output, err := ls.Execute(context.Background(), map[string]interface{}{"prompt": "test"})
	if err != nil {
		t.Logf("LLMSkill.Execute() error (expected in test env): %v", err)
	}

	if output == nil && err == nil {
		t.Error("expected either output or error")
	}
}

func TestBaseSkillGetters(t *testing.T) {
	bs := &domain.BaseSkill{
		ID:          "base-1",
		Name:        "Base Skill",
		Description: "A base skill",
		Type:        "custom",
	}

	if bs.GetID() != "base-1" {
		t.Errorf("expected ID base-1, got %s", bs.GetID())
	}

	if bs.GetName() != "Base Skill" {
		t.Errorf("expected name Base Skill, got %s", bs.GetName())
	}

	if bs.GetDescription() != "A base skill" {
		t.Errorf("expected description 'A base skill', got %s", bs.GetDescription())
	}

	if bs.GetType() != "custom" {
		t.Errorf("expected type custom, got %s", bs.GetType())
	}
}

type mockRegistry struct {
	skills map[string]domain.Skill
}

func (m *mockRegistry) Get(id string) (domain.Skill, bool) {
	s, ok := m.skills[id]
	return s, ok
}

type slowSkill struct {
	*domain.BaseSkill
}

func (s *slowSkill) Execute(_ context.Context, input interface{}) (interface{}, error) {
	time.Sleep(1 * time.Second)
	return nil, nil
}
