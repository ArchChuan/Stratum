package orchestrator

import (
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
)

type Registry struct {
	skills map[string]skill.Skill
}

func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]skill.Skill),
	}
}

func (r *Registry) Register(id string, s skill.Skill) {
	r.skills[id] = s
}

func (r *Registry) Get(id string) (skill.Skill, bool) {
	s, ok := r.skills[id]
	return s, ok
}

func (r *Registry) GetAll() []skill.Skill {
	skills := make([]skill.Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	return skills
}
