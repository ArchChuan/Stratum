// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
)

// LLMSkillProvider 将 executors.LLMSkill 适配为 SkillProvider
type LLMSkillProvider struct {
	s *executors.LLMSkill
}

// NewLLMSkillProvider 创建 LLM provider
func NewLLMSkillProvider(s *executors.LLMSkill) *LLMSkillProvider {
	return &LLMSkillProvider{s: s}
}

func (p *LLMSkillProvider) SkillIDs() []string {
	return []string{p.s.GetID()}
}

func (p *LLMSkillProvider) Has(skillID string) bool {
	return p.s.GetID() == skillID
}

func (p *LLMSkillProvider) SkillType() string {
	return "llm"
}

func (p *LLMSkillProvider) Execute(ctx context.Context, _ string, input any) (any, error) {
	return p.s.Execute(ctx, input)
}
