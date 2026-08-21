package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestEntityRepoRejectsInvalidTenantBeforeUsingPool(t *testing.T) {
	repo := NewEntityRepo(nil)
	entity, err := domain.NewEntity("user123", "", "user", "unsafe", "test")
	if err != nil {
		t.Fatal(err)
	}

	err = repo.Create(context.Background(), `bad"tenant`, entity)
	if err == nil || !strings.HasPrefix(err.Error(), "postgres: invalid tenant_id") {
		t.Fatalf("expected validated tenant error, got %v", err)
	}
}
