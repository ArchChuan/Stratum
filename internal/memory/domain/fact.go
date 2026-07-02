package domain

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/timeutil"
	"github.com/google/uuid"
)

const (
	FactStatusActive     = "active"
	FactStatusSuperseded = "superseded"
	FactStatusArchived   = "archived"
)

// MemoryFact is the aggregate root for a memory fact
type MemoryFact struct {
	ID             string
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	Scope          Scope
	Content        string
	Importance     float64
	EntityNames    []string
	AccessCount    int
	LastAccessAt   time.Time
	SupersededBy   string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewFact creates a new memory fact with validation
func NewFact(tenantID, userID, agentID, conversationID string, scope string, content string, importance float64, entityNames []string) (*MemoryFact, error) {
	if userID == "" {
		return nil, ErrUserIDMismatch
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if content == "" {
		return nil, ErrEmptyContent
	}

	now := timeutil.Now()
	return &MemoryFact{
		ID:             uuid.NewString(),
		TenantID:       tenantID,
		UserID:         userID,
		AgentID:        agentID,
		ConversationID: conversationID,
		Scope:          Scope(scope),
		Content:        content,
		Importance:     importance,
		EntityNames:    entityNames,
		AccessCount:    0,
		LastAccessAt:   now,
		Status:         FactStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

var statusTransitions = map[string][]string{
	FactStatusActive:     {FactStatusSuperseded, FactStatusArchived},
	FactStatusSuperseded: {},
	FactStatusArchived:   {FactStatusActive},
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

// MarkSuperseded marks the fact as superseded by a newer fact
func (f *MemoryFact) MarkSuperseded(newFactID string) error {
	if !f.CanTransitionTo("superseded") {
		return ErrInvalidStatus
	}
	f.Status = FactStatusSuperseded
	f.SupersededBy = newFactID
	f.UpdatedAt = timeutil.Now()
	return nil
}

// MarkArchived archives the fact
func (f *MemoryFact) MarkArchived() error {
	if !f.CanTransitionTo("archived") {
		return ErrInvalidStatus
	}
	f.Status = FactStatusArchived
	f.UpdatedAt = timeutil.Now()
	return nil
}
