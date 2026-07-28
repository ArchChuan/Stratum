package infrastructure_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

func TestPgProviderRepo_CRUD(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool)
	ctx := context.Background()

	p := &domain.Provider{
		ID:      "test-prov-1",
		Name:    "test-qwen",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}

	// Create
	if err := repo.Create(ctx, tenantID, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get
	got, err := repo.Get(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != p.Name {
		t.Errorf("name: got %q, want %q", got.Name, p.Name)
	}
	if got.Kind != p.Kind {
		t.Errorf("kind: got %q, want %q", got.Kind, p.Kind)
	}

	// List
	list, err := repo.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	// Update
	p.Name = "test-qwen-2"
	p.Kind = domain.ProviderAnthropic
	if err := repo.Update(ctx, tenantID, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = repo.Get(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "test-qwen-2" {
		t.Errorf("name after update: got %q, want %q", got.Name, "test-qwen-2")
	}
	if got.Kind != domain.ProviderAnthropic {
		t.Errorf("kind after update: got %q, want %q", got.Kind, domain.ProviderAnthropic)
	}
	if !got.UpdatedAt.After(got.CreatedAt) {
		t.Error("expected updated_at to advance after update")
	}

	// Delete
	if err := repo.Delete(ctx, tenantID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.Get(ctx, tenantID, p.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPgProviderRepo_GetNotFound(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool)
	ctx := context.Background()

	_, err := repo.Get(ctx, tenantID, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestPgProviderRepo_DeleteNotFound(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool)
	ctx := context.Background()

	// Delete nonexistent should succeed (no rows affected is not an error in this implementation)
	err := repo.Delete(ctx, tenantID, "nonexistent")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent provider")
	}
}

func TestPgProviderRepo_ListEmpty(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	repo := infrastructure.NewPgProviderRepo(pool)
	ctx := context.Background()

	list, err := repo.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list len: got %d, want 0", len(list))
	}
}
