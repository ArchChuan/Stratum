package orchestrator

import (
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
)

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	s := &skill.BaseSkill{
		ID:          "test-1",
		Name:        "Test Skill",
		Description: "A test skill",
		Type:        "builtin",
	}

	registry.Register(s.ID, s)

	retrieved, ok := registry.Get(s.ID)
	if !ok {
		t.Fatal("skill not found")
	}

	if retrieved.GetID() != s.ID {
		t.Errorf("expected ID %s, got %s", s.ID, retrieved.GetID())
	}

	if retrieved.GetName() != s.Name {
		t.Errorf("expected name %s, got %s", s.Name, retrieved.GetName())
	}
}

func TestRegistryNotFound(t *testing.T) {
	registry := NewRegistry()

	_, ok := registry.Get("non-existent")
	if ok {
		t.Fatal("expected skill not found")
	}
}

func TestRegistryGetAll(t *testing.T) {
	registry := NewRegistry()

	s1 := &skill.BaseSkill{ID: "skill-1", Name: "Skill 1", Type: "builtin"}
	s2 := &skill.BaseSkill{ID: "skill-2", Name: "Skill 2", Type: "builtin"}

	registry.Register(s1.ID, s1)
	registry.Register(s2.ID, s2)

	skills := registry.GetAll()
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}
}

func TestRegistryRemove(t *testing.T) {
	registry := NewRegistry()

	s := &skill.BaseSkill{ID: "test-1", Name: "Test", Type: "builtin"}
	registry.Register(s.ID, s)

	err := registry.Remove(s.ID)
	if err != nil {
		t.Errorf("Remove() failed: %v", err)
	}

	_, ok := registry.Get(s.ID)
	if ok {
		t.Error("expected skill to be removed")
	}
}

func TestRegistryRemoveNotFound(t *testing.T) {
	registry := NewRegistry()

	err := registry.Remove("non-existent")
	if err == nil {
		t.Error("expected error when removing non-existent skill")
	}
}

func TestRegistryMultipleOperations(t *testing.T) {
	registry := NewRegistry()

	// Register multiple skills
	for i := 1; i <= 5; i++ {
		s := &skill.BaseSkill{
			ID:   "skill-" + string(rune(48+i)),
			Name: "Skill " + string(rune(48+i)),
			Type: "builtin",
		}
		registry.Register(s.ID, s)
	}

	// Verify all registered
	skills := registry.GetAll()
	if len(skills) != 5 {
		t.Errorf("expected 5 skills, got %d", len(skills))
	}

	// Remove one
	registry.Remove("skill-1")
	skills = registry.GetAll()
	if len(skills) != 4 {
		t.Errorf("expected 4 skills after removal, got %d", len(skills))
	}
}
