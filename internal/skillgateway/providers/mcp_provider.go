// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
)

// MCPSkillProvider 将 mcp.MCPSkillAdapter 适配为 SkillProvider
type MCPSkillProvider struct {
	adapter *mcp.MCPSkillAdapter
}

// NewMCPSkillProvider 创建 MCP provider
func NewMCPSkillProvider(adapter *mcp.MCPSkillAdapter) *MCPSkillProvider {
	return &MCPSkillProvider{adapter: adapter}
}

func (p *MCPSkillProvider) SkillIDs() []string {
	skills := p.adapter.GetAllSkills()
	ids := make([]string, 0, len(skills))
	for _, s := range skills {
		ids = append(ids, s.GetID())
	}
	return ids
}

func (p *MCPSkillProvider) Has(skillID string) bool {
	return p.adapter.GetSkill(skillID) != nil
}

func (p *MCPSkillProvider) SkillType() string {
	return "mcp"
}

func (p *MCPSkillProvider) Execute(ctx context.Context, skillID string, input any) (any, error) {
	s := p.adapter.GetSkill(skillID)
	if s == nil {
		return nil, fmt.Errorf("MCP skill not found: %s", skillID)
	}
	executor, ok := s.(interface{ Execute(any) (any, error) })
	if !ok {
		return nil, fmt.Errorf("MCP skill %s does not implement Execute", skillID)
	}
	return executor.Execute(input)
}
