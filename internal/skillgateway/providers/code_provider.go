// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
)

// CodeSkillProvider 将 skill.CodeSkill 适配为 SkillProvider
type CodeSkillProvider struct {
	s *skill.CodeSkill
}

// NewCodeSkillProvider 创建 Code provider
func NewCodeSkillProvider(s *skill.CodeSkill) *CodeSkillProvider {
	return &CodeSkillProvider{s: s}
}

func (p *CodeSkillProvider) SkillIDs() []string {
	return []string{p.s.GetID()}
}

func (p *CodeSkillProvider) Has(skillID string) bool {
	return p.s.GetID() == skillID
}

func (p *CodeSkillProvider) SkillType() string {
	return "code"
}

func (p *CodeSkillProvider) Execute(_ context.Context, _ string, input any) (any, error) {
	return p.s.Execute(input)
}
