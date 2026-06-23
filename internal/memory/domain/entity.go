package domain

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/google/uuid"
)

// MemoryEntity represents a recognized entity with a rolling profile summary.
type MemoryEntity struct {
	ID                    string
	UserID                string
	AgentID               string
	Scope                 Scope
	Name                  string
	EntityType            string // person/project/preference/tech/location
	Profile               string // LLM-generated rolling summary
	FactCount             int
	FactCountSinceRebuild int
	LastSeenAt            time.Time
	LastProfileRebuildAt  time.Time
	Status                string // active/deleted
	CreatedAt             time.Time
	UpdatedAt             time.Time
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

	now := time.Now()
	return &MemoryEntity{
		ID:                    uuid.NewString(),
		UserID:                userID,
		AgentID:               agentID,
		Scope:                 Scope(scope),
		Name:                  name,
		EntityType:            entityType,
		Profile:               "",
		FactCount:             0,
		FactCountSinceRebuild: 0,
		LastSeenAt:            now,
		LastProfileRebuildAt:  time.Time{}, // zero means never rebuilt
		Status:                "active",
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

// IncrementFactCount increments the total and since-rebuild counters.
func (e *MemoryEntity) IncrementFactCount() {
	e.FactCount++
	e.FactCountSinceRebuild++
	e.LastSeenAt = time.Now()
	e.UpdatedAt = time.Now()
}

// ShouldRebuildProfile checks if profile rebuild should be triggered.
// Triggers if: >7 days since last rebuild OR fact delta >=5
func (e *MemoryEntity) ShouldRebuildProfile() bool {
	if e.LastProfileRebuildAt.IsZero() {
		// Never rebuilt — trigger if we have any facts
		return e.FactCount > 0
	}

	daysSinceRebuild := time.Since(e.LastProfileRebuildAt).Hours() / 24
	if daysSinceRebuild >= float64(constants.MemoryEntityRebuildInterval.Hours()/24) {
		return true
	}
	if e.FactCountSinceRebuild >= constants.MemoryEntityRebuildFactDelta {
		return true
	}
	return false
}

// MarkDeleted soft-deletes the entity.
func (e *MemoryEntity) MarkDeleted() {
	e.Status = "deleted"
	e.UpdatedAt = time.Now()
}
