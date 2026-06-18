package port

import "github.com/byteBuilderX/stratum/internal/skill/domain"

// SkillInput is the normalized creation payload passed to SkillFactory.
type SkillInput struct {
	Type         string
	Name         string
	Description  string
	Code         string
	Language     string
	SystemPrompt string
	Model        string
	Temperature  float32
	MaxTokens    int
	URL          string
	Method       string
	Headers      map[string]string
	BodyTemplate string
	TimeoutSec   int
}

// SkillFactory constructs executable skills from creation payloads.
// The implementation lives in infrastructure/executors; application only sees this interface.
type SkillFactory interface {
	Build(id string, in SkillInput) (domain.Skill, error)
}
