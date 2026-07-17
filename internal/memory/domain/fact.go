package domain

import (
	"math"
	"time"
)

const (
	FactStatusActive     = "active"
	FactStatusSuperseded = "superseded"
	FactStatusArchived   = "archived"
)

// FactSource 来源类型常量（Phase 0）
const (
	FactSourceLLMExtraction = "llm_extraction"
	FactSourceExplicitUser  = "explicit_user"
	FactSourceManualAPI     = "manual_api"
)

// factCategoryAllowSet 合法分类白名单（Phase 0）
var factCategoryAllowSet = map[string]bool{
	"preference":   true,
	"skill":        true,
	"event":        true,
	"state":        true,
	"relationship": true,
	"other":        true,
}

var factSourceAllowSet = map[string]bool{
	FactSourceLLMExtraction: true,
	FactSourceExplicitUser:  true,
	FactSourceManualAPI:     true,
}

// factTypeToCategory maps LLM fact_type strings to canonical Category values.
// Unknown values fall back to "other" safely.
var factTypeToCategory = map[string]string{
	"preference":   "preference",
	"skill":        "skill",
	"event":        "event",
	"state":        "state",
	"relationship": "relationship",
	"other":        "other",
}

// FactTypeToCategory converts an LLM-returned fact_type to a canonical category.
// Any unknown value returns "other".
func FactTypeToCategory(factType string) string {
	if cat, ok := factTypeToCategory[factType]; ok {
		return cat
	}
	return "other"
}

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
	// Phase 0 additions
	Category   string  // 分类白名单: preference|skill|event|state|relationship|other
	Confidence float64 // [0,1] 置信度
	Source     string  // 来源: llm_extraction|explicit_user|manual_api
	// ─────────────────
	EntityNames   []string
	AccessCount   int
	FrecencyScore float64
	LastAccessAt  time.Time
	SupersededBy  string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewFact creates a new memory fact with validation.
// Category defaults to "other", Confidence defaults to Importance, Source defaults to "llm_extraction".
// Maintains backward compatibility with all existing callers.
func NewFact(tenantID, userID, agentID, conversationID string, scope string, content string, importance float64, entityNames []string) (*MemoryFact, error) {
	return NewFactWithMeta(tenantID, userID, agentID, conversationID, scope, content, importance, importance, "other", FactSourceLLMExtraction, entityNames)
}

// NewFactWithMeta creates a fact with explicit category, confidence, and source.
// category must be in factCategoryAllowSet; confidence must be in [0,1].
func NewFactWithMeta(tenantID, userID, agentID, conversationID string, scope string, content string, importance, confidence float64, category, source string, entityNames []string) (*MemoryFact, error) {
	if userID == "" {
		return nil, ErrUserIDMismatch
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if content == "" {
		return nil, ErrEmptyContent
	}
	if !factCategoryAllowSet[category] {
		return nil, ErrInvalidCategory
	}
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return nil, ErrConfidenceOutOfRange
	}
	if !factSourceAllowSet[source] {
		return nil, ErrInvalidSource
	}

	now := now()
	return &MemoryFact{
		ID:             newID(),
		TenantID:       tenantID,
		UserID:         userID,
		AgentID:        agentID,
		ConversationID: conversationID,
		Scope:          Scope(scope),
		Content:        content,
		Importance:     importance,
		Category:       category,
		Confidence:     confidence,
		Source:         source,
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
	f.UpdatedAt = now()
	return nil
}

// MarkArchived archives the fact
func (f *MemoryFact) MarkArchived() error {
	if !f.CanTransitionTo("archived") {
		return ErrInvalidStatus
	}
	f.Status = FactStatusArchived
	f.UpdatedAt = now()
	return nil
}
