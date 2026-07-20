package persistence

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestFactRepoCreateRejectsEmptyTenant(t *testing.T) {
	repo := NewFactRepo(nil)

	err := repo.Create(context.Background(), "", &domain.MemoryFact{ID: "fact-1"})
	if err == nil {
		t.Fatal("expected empty tenant to fail")
	}
}

func TestFactRepoCreateRejectsNilPool(t *testing.T) {
	repo := NewFactRepo(nil)

	err := repo.Create(context.Background(), lifecycleTestTenant, &domain.MemoryFact{ID: "fact-1"})
	if err == nil {
		t.Fatal("expected nil persistence pool to fail")
	}
}
