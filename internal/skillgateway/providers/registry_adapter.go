// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
)

// RegistryAdapter 将 orchestrator.Registry 适配为 SkillProvider
type RegistryAdapter struct {
	registry *orchestrator.Registry
}

// NewRegistryAdapter 创建适配器
func NewRegistryAdapter(registry *orchestrator.Registry) *RegistryAdapter {
	return &RegistryAdapter{registry: registry}
}

func (a *RegistryAdapter) SkillIDs() []string {
	skills := a.registry.GetAll()
	ids := make([]string, 0, len(skills))
	for _, s := range skills {
		ids = append(ids, s.GetID())
	}
	return ids
}

func (a *RegistryAdapter) Has(skillID string) bool {
	_, ok := a.registry.Get(skillID)
	return ok
}

func (a *RegistryAdapter) SkillType() string {
	return "registry"
}

func (a *RegistryAdapter) Execute(ctx context.Context, skillID string, input any) (any, error) {
	s, ok := a.registry.Get(skillID)
	if !ok {
		return nil, fmt.Errorf("skill not found in registry: %s", skillID)
	}
	executor, ok := s.(skill.SkillExecutor)
	if !ok {
		return nil, fmt.Errorf("skill %s does not implement SkillExecutor", skillID)
	}
	return executor.Execute(input)
}
