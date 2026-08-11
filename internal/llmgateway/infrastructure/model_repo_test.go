package infrastructure_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

func TestPgModelRepo_CRUD(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	providerRepo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := infrastructure.NewPgModelRepo(pool)
	ctx := context.Background()

	// Create a provider first (models depend on providers via FK)
	prov := &domain.Provider{
		ID:      "test-model-prov",
		Name:    "test-provider",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}
	if err := providerRepo.Create(ctx, tenantID, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	m := &domain.Model{
		ID:            "test-model-1",
		ProviderID:    prov.ID,
		Name:          "gpt-4",
		DisplayName:   "GPT-4",
		Capabilities:  []domain.ModelCapability{domain.CapChat, domain.CapVision},
		ContextWindow: 8192,
		MaxTokens:     4096,
		InputPrice:    10.0,
		OutputPrice:   30.0,
		Recommended:   true,
		Enabled:       true,
	}

	// Create
	if err := modelRepo.Create(ctx, tenantID, m); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get
	got, err := modelRepo.Get(ctx, tenantID, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != m.Name {
		t.Errorf("name: got %q, want %q", got.Name, m.Name)
	}
	if len(got.Capabilities) != 2 {
		t.Fatalf("capabilities len: got %d, want 2", len(got.Capabilities))
	}

	// List with no filter
	list, err := modelRepo.List(ctx, tenantID, port.ModelFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	// List with provider filter
	list, err = modelRepo.List(ctx, tenantID, port.ModelFilter{ProviderID: prov.ID})
	if err != nil {
		t.Fatalf("list by provider: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by provider len: got %d, want 1", len(list))
	}

	// List with capability filter
	list, err = modelRepo.List(ctx, tenantID, port.ModelFilter{Capability: domain.CapVision})
	if err != nil {
		t.Fatalf("list by capability: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by capability len: got %d, want 1", len(list))
	}

	// List with non-matching capability
	list, err = modelRepo.List(ctx, tenantID, port.ModelFilter{Capability: domain.CapEmbedding})
	if err != nil {
		t.Fatalf("list by non-matching capability: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list by non-matching capability len: got %d, want 0", len(list))
	}

	// List with enabled filter
	enabled := true
	list, err = modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		t.Fatalf("list by enabled: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by enabled len: got %d, want 1", len(list))
	}

	// Update
	m.DisplayName = "GPT-4 Turbo"
	m.ContextWindow = 128000
	if err := modelRepo.Update(ctx, tenantID, m); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = modelRepo.Get(ctx, tenantID, m.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.DisplayName != "GPT-4 Turbo" {
		t.Errorf("display name after update: got %q, want %q", got.DisplayName, "GPT-4 Turbo")
	}
	if got.ContextWindow != 128000 {
		t.Errorf("context window after update: got %d, want %d", got.ContextWindow, 128000)
	}

	// Toggle
	if err := modelRepo.Toggle(ctx, tenantID, m.ID, false); err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	got, err = modelRepo.Get(ctx, tenantID, m.ID)
	if err != nil {
		t.Fatalf("get after toggle: %v", err)
	}
	if got.Enabled {
		t.Error("expected model to be disabled after toggle")
	}

	// Toggle back
	if err := modelRepo.Toggle(ctx, tenantID, m.ID, true); err != nil {
		t.Fatalf("toggle on: %v", err)
	}

	// Delete
	if err := modelRepo.Delete(ctx, tenantID, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = modelRepo.Get(ctx, tenantID, m.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPgModelRepo_UpsertDiscovered(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	providerRepo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := infrastructure.NewPgModelRepo(pool)
	ctx := context.Background()

	// Create a provider
	prov := &domain.Provider{
		ID:      "test-upsert-prov",
		Name:    "upsert-provider",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}
	if err := providerRepo.Create(ctx, tenantID, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	// First discovery: insert two models
	discovered := []domain.Model{
		{
			Name:          "gpt-4",
			DisplayName:   "GPT-4",
			Capabilities:  []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192,
			MaxTokens:     4096,
			InputPrice:    10.0,
			OutputPrice:   30.0,
			Recommended:   true,
		},
		{
			Name:          "gpt-4-vision",
			DisplayName:   "GPT-4 Vision",
			Capabilities:  []domain.ModelCapability{domain.CapChat, domain.CapVision},
			ContextWindow: 8192,
			MaxTokens:     4096,
			InputPrice:    15.0,
			OutputPrice:   45.0,
		},
	}

	results, err := modelRepo.UpsertDiscovered(ctx, tenantID, prov.ID, discovered)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("first upsert result count: got %d, want 2", len(results))
	}

	// Second discovery: only one model remains (simulating provider removal)
	discovered = []domain.Model{
		{
			Name:          "gpt-4",
			DisplayName:   "GPT-4",
			Capabilities:  []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192,
			MaxTokens:     4096,
			InputPrice:    10.0,
			OutputPrice:   30.0,
			Recommended:   true,
		},
	}

	results, err = modelRepo.UpsertDiscovered(ctx, tenantID, prov.ID, discovered)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("second upsert result count: got %d, want 2", len(results))
	}

	// The removed model (gpt-4-vision) should be disabled
	var visionEnabled bool
	for _, m := range results {
		if m.Name == "gpt-4-vision" {
			visionEnabled = m.Enabled
		}
	}
	if visionEnabled {
		t.Error("expected gpt-4-vision to be disabled after removal from discovery")
	}

	// The remaining model should be enabled
	var gpt4Enabled bool
	for _, m := range results {
		if m.Name == "gpt-4" {
			gpt4Enabled = m.Enabled
		}
	}
	if !gpt4Enabled {
		t.Error("expected gpt-4 to remain enabled")
	}
}

// TestPgModelRepo_DefaultEmbeddingSelfClean verifies the default-embedding
// marker self-cleans when a model stops being an enabled embedding model:
// Toggle off and Update with enabled=false or capabilities without embedding
// must clear the marker, and re-enabling must NOT restore it (the user re-marks).
func TestPgModelRepo_DefaultEmbeddingSelfClean(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	providerRepo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := infrastructure.NewPgModelRepo(pool)
	ctx := context.Background()

	prov := &domain.Provider{
		ID: "test-selfclean-prov", Name: "sc-provider", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1", APIKey: "sk-test", Enabled: true,
	}
	if err := providerRepo.Create(ctx, tenantID, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	em := &domain.Model{
		ID: "test-embed-1", ProviderID: prov.ID, Name: "text-embedding-3", DisplayName: "Embed 3",
		Capabilities: []domain.ModelCapability{domain.CapEmbedding}, ContextWindow: 8192,
		MaxTokens: 2048, Recommended: true, Enabled: true,
	}
	if err := modelRepo.Create(ctx, tenantID, em); err != nil {
		t.Fatalf("create embed model: %v", err)
	}
	if err := modelRepo.SetDefaultEmbedding(ctx, tenantID, em.ID, true); err != nil {
		t.Fatalf("set default: %v", err)
	}

	t.Run("toggle off clears the marker and re-enable does not restore it", func(t *testing.T) {
		if err := modelRepo.Toggle(ctx, tenantID, em.ID, false); err != nil {
			t.Fatalf("toggle off: %v", err)
		}
		got, err := modelRepo.Get(ctx, tenantID, em.ID)
		if err != nil {
			t.Fatalf("get after toggle off: %v", err)
		}
		if got.DefaultEmbedding {
			t.Error("marker should be cleared after disabling the default model")
		}
		if err := modelRepo.Toggle(ctx, tenantID, em.ID, true); err != nil {
			t.Fatalf("toggle on: %v", err)
		}
		got, err = modelRepo.Get(ctx, tenantID, em.ID)
		if err != nil {
			t.Fatalf("get after toggle on: %v", err)
		}
		if got.DefaultEmbedding {
			t.Error("marker should NOT be restored on re-enable; the user must re-mark")
		}
	})

	t.Run("update disabling clears the marker", func(t *testing.T) {
		em.Enabled = false
		if err := modelRepo.Update(ctx, tenantID, em); err != nil {
			t.Fatalf("update disable: %v", err)
		}
		got, err := modelRepo.Get(ctx, tenantID, em.ID)
		if err != nil {
			t.Fatalf("get after update disable: %v", err)
		}
		if got.DefaultEmbedding {
			t.Error("marker should be cleared when update disables the model")
		}
	})

	t.Run("update dropping embedding capability clears the marker", func(t *testing.T) {
		em.Enabled = true
		em.Capabilities = []domain.ModelCapability{domain.CapChat}
		if err := modelRepo.Update(ctx, tenantID, em); err != nil {
			t.Fatalf("update caps: %v", err)
		}
		got, err := modelRepo.Get(ctx, tenantID, em.ID)
		if err != nil {
			t.Fatalf("get after update caps: %v", err)
		}
		if got.DefaultEmbedding {
			t.Error("marker should be cleared when the embedding capability is dropped")
		}
	})
}

func TestPgModelRepo_DeleteProviderManaged(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	providerRepo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := infrastructure.NewPgModelRepo(pool)
	ctx := context.Background()

	prov := &domain.Provider{
		ID:      "test-del-managed-prov",
		Name:    "managed-provider",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1",
		APIKey:  "sk-test",
		Enabled: true,
	}
	if err := providerRepo.Create(ctx, tenantID, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}

	m := &domain.Model{
		ID:              "test-managed-model",
		ProviderID:      prov.ID,
		Name:            "managed-model",
		ProviderManaged: true,
		Enabled:         true,
	}
	if err := modelRepo.Create(ctx, tenantID, m); err != nil {
		t.Fatalf("create managed model: %v", err)
	}

	// Deleting a provider-managed model should fail
	err := modelRepo.Delete(ctx, tenantID, m.ID)
	if err == nil {
		t.Fatal("expected error deleting provider-managed model")
	}
}
