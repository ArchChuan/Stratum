package orchestrator

import (
	"fmt"
	"sync"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
)

type Registry struct {
	skills map[string]skill.Skill
	mu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]skill.Skill),
	}
}

func (r *Registry) Register(id string, s skill.Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[id] = s
}

func (r *Registry) Get(id string) (skill.Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	return s, ok
}

func (r *Registry) GetAll() []skill.Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skillList := make([]skill.Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skillList = append(skillList, s)
	}
	return skillList
}

// Remove removes a skill by ID
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.skills[id]
	if !ok {
		return fmt.Errorf("skill not found: %s", id)
	}
	delete(r.skills, id)
	return nil
}
