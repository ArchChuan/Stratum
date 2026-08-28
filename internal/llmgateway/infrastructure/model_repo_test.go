package infrastructure_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

// newModelTestRepos 创建共享 pool 的 provider/model repo，并返回公共
// providers/models 表清理函数。public 表全局共享，测试间靠唯一 ID + cleanup
// 隔离（providers 被 models FK 引用，先删 models 再删 providers）。
func newModelTestRepos(t *testing.T) (*pgxpool.Pool, *infrastructure.PgProviderRepo, *infrastructure.PgModelRepo, func(providerID string, modelIDs ...string)) {
	pool := postgrestest.NewPool(t)
	providerRepo := infrastructure.NewPgProviderRepo(pool, testRepoKey, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := infrastructure.NewPgModelRepo(pool)
	cleanup := func(providerID string, modelIDs ...string) {
		t.Cleanup(func() {
			for _, id := range modelIDs {
				_, _ = pool.Exec(context.Background(), `DELETE FROM public.models WHERE id=$1`, id)
			}
			_, _ = pool.Exec(context.Background(), `DELETE FROM public.providers WHERE id=$1`, providerID)
		})
	}
	return pool, providerRepo, modelRepo, cleanup
}

func newSeedProvider(prefix string) *domain.Provider {
	return &domain.Provider{
		ID:   fmt.Sprintf("test-%s-%d", prefix, time.Now().UnixNano()),
		Name: "test-provider", Kind: domain.ProviderOpenAICompat,
		BaseURL: "https://test.example.com/v1", APIKey: "sk-test", Enabled: true,
	}
}

func TestPgModelRepo_CRUD(t *testing.T) {
	_, providerRepo, modelRepo, cleanup := newModelTestRepos(t)
	ctx := context.Background()

	// Create a provider first (models depend on providers via FK)
	prov := newSeedProvider("model-crud-prov")
	if err := providerRepo.Create(ctx, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	cleanup(prov.ID)

	m := &domain.Model{
		ID:            fmt.Sprintf("test-model-1-%d", time.Now().UnixNano()),
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
		// 039 迁移给 fallback_candidates 加了 NOT NULL 约束，Update 写该列，
		// nil 切片会被 pgx 写成 NULL 而违反约束（既有测试缺陷，与本特性无关）。
		FallbackCandidates: []string{},
	}
	cleanup(prov.ID, m.ID)

	// Create
	if err := modelRepo.Create(ctx, m); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get
	got, err := modelRepo.Get(ctx, m.ID)
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
	list, err := modelRepo.List(ctx, port.ModelFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len: got %d, want 1", len(list))
	}

	// List with provider filter
	list, err = modelRepo.List(ctx, port.ModelFilter{ProviderID: prov.ID})
	if err != nil {
		t.Fatalf("list by provider: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by provider len: got %d, want 1", len(list))
	}

	// List with capability filter
	list, err = modelRepo.List(ctx, port.ModelFilter{Capability: domain.CapVision})
	if err != nil {
		t.Fatalf("list by capability: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by capability len: got %d, want 1", len(list))
	}

	// List with non-matching capability
	list, err = modelRepo.List(ctx, port.ModelFilter{Capability: domain.CapEmbedding})
	if err != nil {
		t.Fatalf("list by non-matching capability: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list by non-matching capability len: got %d, want 0", len(list))
	}

	// List with enabled filter
	enabled := true
	list, err = modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		t.Fatalf("list by enabled: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list by enabled len: got %d, want 1", len(list))
	}

	// Update
	m.DisplayName = "GPT-4 Turbo"
	m.ContextWindow = 128000
	if err := modelRepo.Update(ctx, m, "t1", nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = modelRepo.Get(ctx, m.ID)
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
	if err := modelRepo.Toggle(ctx, m.ID, false); err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	got, err = modelRepo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("get after toggle: %v", err)
	}
	if got.Enabled {
		t.Error("expected model to be disabled after toggle")
	}

	// Toggle back
	if err := modelRepo.Toggle(ctx, m.ID, true); err != nil {
		t.Fatalf("toggle on: %v", err)
	}

	// Delete
	if err := modelRepo.Delete(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = modelRepo.Get(ctx, m.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPgModelRepo_UpsertDiscovered(t *testing.T) {
	_, providerRepo, modelRepo, cleanup := newModelTestRepos(t)
	ctx := context.Background()

	prov := newSeedProvider("upsert-prov")
	if err := providerRepo.Create(ctx, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	cleanup(prov.ID)

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

	results, err := modelRepo.UpsertDiscovered(ctx, prov.ID, discovered)
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

	results, err = modelRepo.UpsertDiscovered(ctx, prov.ID, discovered)
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

func TestPgModelRepo_DeleteProviderManagedAllowed(t *testing.T) {
	_, providerRepo, modelRepo, cleanup := newModelTestRepos(t)
	ctx := context.Background()

	prov := newSeedProvider("del-managed-prov")
	if err := providerRepo.Create(ctx, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	m := &domain.Model{
		ID:              fmt.Sprintf("test-managed-model-%d", time.Now().UnixNano()),
		ProviderID:      prov.ID,
		Name:            "managed-model",
		ProviderManaged: true,
		Enabled:         true,
	}
	cleanup(prov.ID, m.ID)
	if err := modelRepo.Create(ctx, m); err != nil {
		t.Fatalf("create managed model: %v", err)
	}

	// provider-managed 模型允许删除；删除后 Get 报 not found。
	err := modelRepo.Delete(ctx, m.ID)
	if err != nil {
		t.Fatalf("delete provider-managed model should succeed: %v", err)
	}
	if _, err := modelRepo.Get(ctx, m.ID); err == nil {
		t.Fatal("expected not found after delete")
	}
}

// TestPgModelRepo_UpsertDiscovered_PreservesUserToggleOff 守护核心语义：重新发现时
// 存量模型开关保持现状（含用户手动关闭），只有新发现的模型默认开启。
func TestPgModelRepo_UpsertDiscovered_PreservesUserToggleOff(t *testing.T) {
	_, providerRepo, modelRepo, cleanup := newModelTestRepos(t)
	ctx := context.Background()

	prov := newSeedProvider("upsert-toggle-prov")
	if err := providerRepo.Create(ctx, prov); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	cleanup(prov.ID)

	newModel := func(name string) domain.Model {
		return domain.Model{
			Name:          name,
			DisplayName:   name,
			Capabilities:  []domain.ModelCapability{domain.CapChat},
			ContextWindow: 8192,
			MaxTokens:     4096,
			InputPrice:    10.0,
			OutputPrice:   30.0,
			Recommended:   true,
		}
	}
	enabledByName := func(models []domain.Model) map[string]bool {
		m := make(map[string]bool, len(models))
		for _, mm := range models {
			m[mm.Name] = mm.Enabled
		}
		return m
	}

	// First discovery: two models, both enabled by default.
	first, err := modelRepo.UpsertDiscovered(ctx, prov.ID,
		[]domain.Model{newModel("gpt-4"), newModel("gpt-4-vision")})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	var gpt4ID string
	for _, mm := range first {
		if mm.Name == "gpt-4" {
			gpt4ID = mm.ID
		}
	}
	if gpt4ID == "" {
		t.Fatal("first upsert result missing gpt-4 id")
	}

	// User manually disables gpt-4; gpt-4-vision stays untouched.
	if err := modelRepo.Toggle(ctx, gpt4ID, false); err != nil {
		t.Fatalf("toggle off gpt-4: %v", err)
	}

	// Re-discovery reports all three models: gpt-4 and gpt-4-vision are existing
	// (gpt-4 must stay off — user intent preserved), gpt-4-turbo is newly added.
	second, err := modelRepo.UpsertDiscovered(ctx, prov.ID,
		[]domain.Model{newModel("gpt-4"), newModel("gpt-4-vision"), newModel("gpt-4-turbo")})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	enabled := enabledByName(second)

	if enabled["gpt-4"] {
		t.Error("expected gpt-4 to stay disabled after re-discovery (user toggle preserved)")
	}
	if !enabled["gpt-4-vision"] {
		t.Error("expected gpt-4-vision to remain enabled")
	}
	if !enabled["gpt-4-turbo"] {
		t.Error("expected newly discovered gpt-4-turbo to be enabled by default")
	}
}
