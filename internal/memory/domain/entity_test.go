package domain_test

import (
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestNewEntity(t *testing.T) {
	entity, err := domain.NewEntity("user123", "", "user", "Alice", "person")
	if err != nil {
		t.Fatalf("NewEntity failed: %v", err)
	}
	if entity.UserID != "user123" {
		t.Error("userID mismatch")
	}
	if entity.Name != "Alice" {
		t.Error("name mismatch")
	}
	if entity.EntityType != "person" {
		t.Error("type mismatch")
	}
	if entity.Status != "active" {
		t.Error("expected active status")
	}
	if entity.FactCount != 0 {
		t.Error("fact count should be 0")
	}
}

func TestEntityIncrementFactCount(t *testing.T) {
	entity, _ := domain.NewEntity("user123", "", "user", "Project X", "project")
	entity.IncrementFactCount()
	if entity.FactCount != 1 {
		t.Error("fact count should be 1")
	}
}

func TestEntityShouldRebuildProfile(t *testing.T) {
	entity, _ := domain.NewEntity("user123", "", "user", "Alice", "person")
	entity.LastProfileRebuildAt = time.Now().Add(-8 * 24 * time.Hour)
	entity.FactCountSinceRebuild = 2

	if !entity.ShouldRebuildProfile() {
		t.Error("should rebuild: >7 days since last rebuild")
	}

	entity.LastProfileRebuildAt = time.Now().Add(-1 * time.Hour)
	entity.FactCountSinceRebuild = 6

	if !entity.ShouldRebuildProfile() {
		t.Error("should rebuild: fact count delta >=5")
	}

	entity.LastProfileRebuildAt = time.Now()
	entity.FactCountSinceRebuild = 2

	if entity.ShouldRebuildProfile() {
		t.Error("should not rebuild: recent + low delta")
	}
}
