package domain

import (
	"time"
)

const EntityStatusActive = "active"

// MemoryEntity represents a recognized entity as a lightweight topic tag.
// The LLM-generated profile (画像) was removed: it had zero consumers at
// runtime while ProfileWorker kept paying LLM rebuild costs.
type MemoryEntity struct {
	ID         string
	UserID     string
	AgentID    string
	Scope      Scope
	Name       string
	EntityType string // person/project/preference/tech/location
	FactCount  int
	LastSeenAt time.Time
	Status     string // active/deleted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewEntity creates a new active entity with validation.
func NewEntity(userID, agentID, scope, name, entityType string) (*MemoryEntity, error) {
	if userID == "" {
		return nil, ErrUserIDMismatch
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, ErrEmptyContent
	}

	now := now()
	return &MemoryEntity{
		ID:         newID(),
		UserID:     userID,
		AgentID:    agentID,
		Scope:      Scope(scope),
		Name:       name,
		EntityType: entityType,
		FactCount:  0,
		LastSeenAt: now,
		Status:     EntityStatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// IncrementFactCount increments the total counter.
func (e *MemoryEntity) IncrementFactCount() {
	e.FactCount++
	e.LastSeenAt = now()
	e.UpdatedAt = now()
}

// MarkDeleted soft-deletes the entity.
func (e *MemoryEntity) MarkDeleted() {
	e.Status = "deleted"
	e.UpdatedAt = now()
}
