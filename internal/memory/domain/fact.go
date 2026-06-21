package domain

import (
	"time"

	"github.com/google/uuid"
)

// MemoryFact is the aggregate root for a memory fact
type MemoryFact struct {
	ID           string
	UserID       string
	AgentID      string
	Scope        Scope
	Content      string
	Importance   float64
	EntityNames  []string
	AccessCount  int
	LastAccessAt time.Time
	SupersededBy string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    time.Time
}

// NewFact creates a new memory fact with validation
func NewFact(userID, agentID string, scope string, content string, importance float64, entityNames []string) (*MemoryFact, error) {
	if userID == "" {
		return nil, ErrUserIDMismatch
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if content == "" {
		return nil, ErrEmptyContent
	}

	now := time.Now()
	return &MemoryFact{
		ID:           uuid.NewString(),
		UserID:       userID,
		AgentID:      agentID,
		Scope:        Scope(scope),
		Content:      content,
		Importance:   importance,
		EntityNames:  entityNames,
		AccessCount:  0,
		LastAccessAt: now,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

var statusTransitions = map[string][]string{
	"active":     {"deleted", "superseded", "archived"},
	"superseded": {},
	"archived":   {"active"},
	"deleted":    {},
}

// CanTransitionTo checks if a status transition is valid
func (f *MemoryFact) CanTransitionTo(newStatus string) bool {
	allowed, ok := statusTransitions[f.Status]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == newStatus {
			return true
		}
	}
	return false
}

// MarkDeleted soft-deletes the fact
func (f *MemoryFact) MarkDeleted() {
	f.Status = "deleted"
	f.DeletedAt = time.Now()
	f.UpdatedAt = f.DeletedAt
}

// MarkSuperseded marks the fact as superseded by a newer fact
func (f *MemoryFact) MarkSuperseded(newFactID string) error {
	if !f.CanTransitionTo("superseded") {
		return ErrInvalidStatus
	}
	f.Status = "superseded"
	f.SupersededBy = newFactID
	f.UpdatedAt = time.Now()
	return nil
}

// MarkArchived archives the fact
func (f *MemoryFact) MarkArchived() error {
	if !f.CanTransitionTo("archived") {
		return ErrInvalidStatus
	}
	f.Status = "archived"
	f.UpdatedAt = time.Now()
	return nil
}
