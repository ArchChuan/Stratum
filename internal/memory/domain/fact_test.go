package domain_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestNewFact(t *testing.T) {
	fact, err := domain.NewFact("", "user123", "", "", "user", "User prefers Vim", 0.85, []string{"vim", "preference"})
	if err != nil {
		t.Fatalf("NewFact failed: %v", err)
	}
	if fact.UserID != "user123" {
		t.Error("userID mismatch")
	}
	if fact.Scope != domain.ScopeUser {
		t.Error("scope mismatch")
	}
	if fact.Status != "active" {
		t.Error("expected active status")
	}
	if fact.Importance != 0.85 {
		t.Error("importance mismatch")
	}
}

func TestNewFact_Validation(t *testing.T) {
	_, err := domain.NewFact("", "", "", "", "user", "content", 0.5, nil)
	if err != domain.ErrUserIDMismatch {
		t.Errorf("expected ErrUserIDMismatch for empty userID, got %v", err)
	}

	_, err = domain.NewFact("", "user123", "", "", "invalid_scope", "content", 0.5, nil)
	if err == nil {
		t.Error("expected error for invalid scope")
	}

	_, err = domain.NewFact("", "user123", "", "", "user", "", 0.5, nil)
	if err != domain.ErrEmptyContent {
		t.Errorf("expected ErrEmptyContent, got %v", err)
	}
}

func TestFactStatusTransitions(t *testing.T) {
	fact, _ := domain.NewFact("", "user123", "", "", "user", "content", 0.5, nil)

	if fact.CanTransitionTo("deleted") {
		t.Error("active → deleted should not be allowed")
	}
	if !fact.CanTransitionTo("superseded") {
		t.Error("active → superseded should be allowed")
	}
	if !fact.CanTransitionTo("archived") {
		t.Error("active → archived should be allowed")
	}
}

func TestFactMarkSuperseded(t *testing.T) {
	fact, _ := domain.NewFact("", "user123", "", "", "user", "old fact", 0.5, nil)
	newFactID := "new-fact-uuid"

	err := fact.MarkSuperseded(newFactID)
	if err != nil {
		t.Fatalf("MarkSuperseded failed: %v", err)
	}

	if fact.Status != "superseded" {
		t.Error("status should be superseded")
	}

	if fact.SupersededBy != newFactID {
		t.Error("supersededBy mismatch")
	}
}
