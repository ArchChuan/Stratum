// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
)

// CodeSkillProvider 将 code.CodeSkill 适配为 SkillProvider
type CodeSkillProvider struct {
	s *code.CodeSkill
}

// NewCodeSkillProvider 创建 Code provider
func NewCodeSkillProvider(s *code.CodeSkill) *CodeSkillProvider {
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

func (p *CodeSkillProvider) Execute(ctx context.Context, _ string, input any) (any, error) {
	return p.s.Execute(ctx, input)
}
