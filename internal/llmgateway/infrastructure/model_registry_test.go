package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// ---------------------------------------------------------------------------
// Mock repositories
// ---------------------------------------------------------------------------

type mockModelRepo struct {
	models []domain.Model
	err    error
}

func (m *mockModelRepo) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := m.models
	if filter.Capability != "" {
		var filtered []domain.Model
		for _, mdl := range m.models {
			for _, cap := range mdl.Capabilities {
				if cap == filter.Capability {
					filtered = append(filtered, mdl)
					break
				}
			}
		}
		out = filtered
	}
	return out, nil
}

func (m *mockModelRepo) Create(ctx context.Context, tenantID string, model *domain.Model) error {
	return m.err
}

func (m *mockModelRepo) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
	for _, model := range m.models {
		if model.ID == id {
			return &model, nil
		}
	}
	return nil, m.err
}

func (m *mockModelRepo) Update(ctx context.Context, tenantID string, model *domain.Model) error {
	return m.err
}

func (m *mockModelRepo) UpsertDiscovered(ctx context.Context, tenantID, providerID string, models []domain.Model) ([]domain.Model, error) {
	return models, m.err
}

func (m *mockModelRepo) Delete(ctx context.Context, tenantID, id string) error {
	return m.err
}

func (m *mockModelRepo) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
	return m.err
}

type mockProviderRepo struct {
	providers map[string]*domain.Provider
	err       error
}

func (m *mockProviderRepo) Get(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	p, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func (m *mockProviderRepo) GetMeta(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	p, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	cp := *p
	cp.APIKey = ""
	return &cp, nil
}

func (m *mockProviderRepo) Create(ctx context.Context, tenantID string, p *domain.Provider) error {
	return m.err
}

func (m *mockProviderRepo) List(ctx context.Context, tenantID string) ([]domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]domain.Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, *p)
	}
	return out, nil
}

func (m *mockProviderRepo) Update(ctx context.Context, tenantID string, p *domain.Provider) error {
	return m.err
}

func (m *mockProviderRepo) Delete(ctx context.Context, tenantID, id string) error {
	return m.err
}

// ---------------------------------------------------------------------------
// Mock protocols
// ---------------------------------------------------------------------------

type mockChatProto struct {
	label string
}

func (m *mockChatProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	return nil, nil
}

func (m *mockChatProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	return nil, nil
}

func (m *mockChatProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error {
	return nil
}

func (m *mockChatProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

type mockEmbedProto struct{}

func (m *mockEmbedProto) CreateEmbeddings(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.EmbeddingRequest) (*infrastructure.EmbeddingResponse, error) {
	return nil, nil
}

func (m *mockEmbedProto) BatchSize() int {
	return 10
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestRegistry(modelRepo *mockModelRepo, providerRepo *mockProviderRepo) *infrastructure.ModelRegistry {
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{
		domain.ProviderOpenAICompat: &mockChatProto{label: "openai"},
		domain.ProviderAnthropic:    &mockChatProto{label: "anthropic"},
	}
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{
		domain.ProviderOpenAICompat: &mockEmbedProto{},
	}
	return infrastructure.NewModelRegistry(
		modelRepo,
		providerRepo,
		chatProtos,
		embedProtos,
		5*time.Minute,
	)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestModelRegistry_Resolve_HappyPath(t *testing.T) {
	providerID := "prov-1"
	providers := map[string]*domain.Provider{
		providerID: {
			ID:           providerID,
			Name:         "Test OpenAI",
			Kind:         domain.ProviderOpenAICompat,
			BaseURL:      "https://api.openai.com",
			APIKey:       "sk-test",
			DefaultModel: "gpt-4",
			Enabled:      true,
		},
	}
	models := []domain.Model{
		{ID: "mod-1", ProviderID: providerID, Name: "gpt-4", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	cfg, proto, err := reg.Resolve(context.Background(), "tenant-1", "gpt-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "Test OpenAI" {
		t.Errorf("cfg.Name = %q, want %q", cfg.Name, "Test OpenAI")
	}
	if cfg.BaseURL != "https://api.openai.com" {
		t.Errorf("cfg.BaseURL = %q", cfg.BaseURL)
	}
	if cfg.APIKey != "sk-test" {
		t.Errorf("cfg.APIKey = %q", cfg.APIKey)
	}
	if proto == nil {
		t.Fatal("proto is nil, expected ChatProtocol")
	}
}

func TestModelRegistry_Resolve_CacheHit(t *testing.T) {
	providerID := "prov-2"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Cache Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://cache.test", APIKey: "sk-cache", DefaultModel: "m2", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m2", ProviderID: providerID, Name: "model-two", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	// First call populates cache
	cfg1, _, err := reg.Resolve(context.Background(), "t1", "model-two")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Mutate the repos to prove the registry does NOT re-read on the second call
	mr.models = nil
	delete(pr.providers, providerID)

	// Second call must still succeed from cache
	cfg2, proto2, err := reg.Resolve(context.Background(), "t1", "model-two")
	if err != nil {
		t.Fatalf("second resolve (cache): %v", err)
	}
	if cfg1.Name != cfg2.Name {
		t.Errorf("cached config name changed: %q vs %q", cfg1.Name, cfg2.Name)
	}
	if proto2 == nil {
		t.Fatal("cached proto is nil")
	}
}

func TestModelRegistry_Resolve_ModelNotFound(t *testing.T) {
	mr := &mockModelRepo{models: []domain.Model{}}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{}}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.Resolve(context.Background(), "t1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown model, got nil")
	}
}

func TestModelRegistry_Resolve_NoChatProtocol(t *testing.T) {
	providerID := "prov-3"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Ollama Inst", Kind: domain.ProviderOllama,
			BaseURL: "http://ollama:11434", APIKey: "", DefaultModel: "llama3", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m3", ProviderID: providerID, Name: "llama3", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	// Registry only has OpenAI/Anthropic chat protos — Ollama is not registered
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{}
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{}
	reg := infrastructure.NewModelRegistry(
		&mockModelRepo{models: models},
		&mockProviderRepo{providers: providers},
		chatProtos,
		embedProtos,
		5*time.Minute,
	)

	_, _, err := reg.Resolve(context.Background(), "t1", "llama3")
	if err == nil {
		t.Fatal("expected error for missing chat protocol, got nil")
	}
}

func TestModelRegistry_ResolveEmbedding_HappyPath(t *testing.T) {
	providerID := "prov-embed"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Embed Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://embed.test", APIKey: "sk-embed", DefaultModel: "text-embedding-3", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "me1", ProviderID: providerID, Name: "text-embedding-3", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	cfg, proto, err := reg.ResolveEmbedding(context.Background(), "t1", "text-embedding-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "Embed Prov" {
		t.Errorf("cfg.Name = %q", cfg.Name)
	}
	if proto == nil {
		t.Fatal("embed proto is nil")
	}
	if proto.BatchSize() != 10 {
		t.Errorf("BatchSize() = %d, want 10", proto.BatchSize())
	}
}

func TestModelRegistry_ResolveEmbedding_NoEmbedProtocol(t *testing.T) {
	providerID := "prov-noembed"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Anthropic No Embed", Kind: domain.ProviderAnthropic,
			BaseURL: "https://api.anthropic.com", APIKey: "sk-ant", DefaultModel: "claude-3", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "me2", ProviderID: providerID, Name: "claude-3", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}

	// Registry's embed protos map only includes OpenAI — Anthropic not registered
	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.ResolveEmbedding(context.Background(), "t1", "claude-3")
	if err == nil {
		t.Fatal("expected error for missing embed protocol, got nil")
	}
}

func TestModelRegistry_ResolveEmbedding_ModelNotFound(t *testing.T) {
	mr := &mockModelRepo{models: []domain.Model{}}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{}}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.ResolveEmbedding(context.Background(), "t1", "nope")
	if err == nil {
		t.Fatal("expected error for unknown embedding model, got nil")
	}
}

func TestModelRegistry_ListChatModels(t *testing.T) {
	models := []domain.Model{
		{ID: "m1", ProviderID: "openai", Name: "gpt-4", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m2", ProviderID: "anthropic", Name: "claude-3", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapVision}},
		{ID: "m3", ProviderID: "openai", Name: "ada-002", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}
	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{
		"openai":    {ID: "openai", Kind: domain.ProviderOpenAICompat, Enabled: true},
		"anthropic": {ID: "anthropic", Kind: domain.ProviderAnthropic, Enabled: true},
	}}
	reg := newTestRegistry(mr, pr)

	names, err := reg.ListChatModelsByTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("got %d chat models, want 2", len(names))
	}
	if names[0] != "claude-3" || names[1] != "gpt-4" {
		t.Errorf("expected sorted chat models, got %v", names)
	}
}

func TestModelRegistry_ListChatModels_Empty(t *testing.T) {
	mr := &mockModelRepo{models: []domain.Model{}}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{}}
	reg := newTestRegistry(mr, pr)

	names, err := reg.ListChatModelsByTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected empty slice, got %v", names)
	}
}

func TestModelRegistry_ListEmbeddingModels(t *testing.T) {
	models := []domain.Model{
		{ID: "m1", ProviderID: "openai", Name: "ada-002", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
		{ID: "m2", ProviderID: "openai", Name: "gpt-4", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
	}
	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{
		"openai": {ID: "openai", Kind: domain.ProviderOpenAICompat, Enabled: true},
	}}
	reg := newTestRegistry(mr, pr)

	names, err := reg.ListEmbeddingModelsByTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 1 || names[0] != "ada-002" {
		t.Errorf("expected [ada-002], got %v", names)
	}
}

func TestModelRegistry_ListModelsExcludesDisabledAndUnsupportedProviders(t *testing.T) {
	models := []domain.Model{
		{ID: "chat-enabled", ProviderID: "enabled", Name: "chat-ok", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "chat-disabled", ProviderID: "disabled", Name: "chat-disabled", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "embed-unsupported", ProviderID: "anthropic", Name: "embed-unsupported", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}
	providers := map[string]*domain.Provider{
		"enabled":   {ID: "enabled", Kind: domain.ProviderOpenAICompat, Enabled: true},
		"disabled":  {ID: "disabled", Kind: domain.ProviderOpenAICompat, Enabled: false},
		"anthropic": {ID: "anthropic", Kind: domain.ProviderAnthropic, Enabled: true},
	}
	reg := newTestRegistry(&mockModelRepo{models: models}, &mockProviderRepo{providers: providers})

	chat, err := reg.ListChatModelsByTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("ListChatModelsByTenant: %v", err)
	}
	if len(chat) != 1 || chat[0] != "chat-ok" {
		t.Fatalf("chat models = %v, want [chat-ok]", chat)
	}
	embedding, err := reg.ListEmbeddingModelsByTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("ListEmbeddingModelsByTenant: %v", err)
	}
	if len(embedding) != 0 {
		t.Fatalf("embedding models = %v, want []", embedding)
	}
}

func TestModelRegistry_ListModelsFailsClosedWhenProviderLookupFails(t *testing.T) {
	models := []domain.Model{{
		ID: "orphan", ProviderID: "missing", Name: "orphan", Enabled: true,
		Capabilities: []domain.ModelCapability{domain.CapChat},
	}}
	reg := newTestRegistry(&mockModelRepo{models: models}, &mockProviderRepo{providers: map[string]*domain.Provider{}})

	if _, err := reg.ListChatModelsByTenant(context.Background(), "t1"); err == nil {
		t.Fatal("missing provider must fail the catalogue closed")
	}
}

func TestModelRegistry_ResolveRequiresEnabledProviderAndMatchingCapability(t *testing.T) {
	models := []domain.Model{
		{ID: "embed", ProviderID: "enabled", Name: "embed-only", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
		{ID: "chat", ProviderID: "disabled", Name: "chat-disabled", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}
	providers := map[string]*domain.Provider{
		"enabled":  {ID: "enabled", Kind: domain.ProviderOpenAICompat, Enabled: true},
		"disabled": {ID: "disabled", Kind: domain.ProviderOpenAICompat, Enabled: false},
	}
	reg := newTestRegistry(&mockModelRepo{models: models}, &mockProviderRepo{providers: providers})

	if _, _, err := reg.Resolve(context.Background(), "t1", "embed-only"); err == nil {
		t.Fatal("embedding-only model must not resolve for chat")
	}
	if _, _, err := reg.Resolve(context.Background(), "t1", "chat-disabled"); err == nil {
		t.Fatal("model from disabled provider must not resolve")
	}
}

func TestModelRegistry_WarmTenant(t *testing.T) {
	providerID := "prov-warm"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Warm Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://warm.test", APIKey: "sk-warm", DefaultModel: "gpt-4", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m1", ProviderID: providerID, Name: "gpt-4", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
	}
	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	err := reg.WarmTenant(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cache is populated — a resolve should hit cache even after wiping repos
	mr.models = nil
	delete(pr.providers, providerID)

	cfg, _, err := reg.Resolve(context.Background(), "t1", "gpt-4")
	if err != nil {
		t.Fatal("expected cache hit after warm, got:", err)
	}
	if cfg.Name != "Warm Prov" {
		t.Errorf("unexpected config name: %s", cfg.Name)
	}
}

func TestModelRegistry_Invalidate(t *testing.T) {
	providerID := "prov-inv"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Inv Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://inv.test", APIKey: "sk-inv", DefaultModel: "m", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m-inv", ProviderID: providerID, Name: "test-model", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	// Populate cache
	_, _, err := reg.Resolve(context.Background(), "t-inv", "test-model")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Invalidate the tenant
	reg.Invalidate("t-inv")

	// Now blow away the repos; a cache hit would still succeed,
	// but the entry was evicted so it must hit the DB again.
	mr.models = nil
	delete(pr.providers, providerID)

	_, _, err = reg.Resolve(context.Background(), "t-inv", "test-model")
	if err == nil {
		t.Fatal("expected error after invalidation + repo wipe, got nil")
	}
}

func TestModelRegistry_Invalidate_OtherTenant(t *testing.T) {
	providerID := "prov-other"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Other Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://other.test", APIKey: "sk-other", DefaultModel: "x", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m-a", ProviderID: providerID, Name: "model-a", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-b", ProviderID: providerID, Name: "model-b", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}
	reg := newTestRegistry(mr, pr)

	// Populate cache for two tenants
	reg.Resolve(context.Background(), "tenant-a", "model-a")
	reg.Resolve(context.Background(), "tenant-b", "model-b")

	// Invalidate only tenant-a
	reg.Invalidate("tenant-a")

	// tenant-b must still be served from cache
	mr.models = nil
	delete(pr.providers, providerID)

	_, _, err := reg.Resolve(context.Background(), "tenant-b", "model-b")
	if err != nil {
		t.Fatalf("tenant-b should still be cached: %v", err)
	}

	// tenant-a must now fail (cache cleared + repos empty)
	_, _, err = reg.Resolve(context.Background(), "tenant-a", "model-a")
	if err == nil {
		t.Fatal("tenant-a should fail after invalidation + repo wipe")
	}
}

func TestModelRegistry_Resolve_CacheExpiry(t *testing.T) {
	providerID := "prov-exp"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Exp Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://exp.test", APIKey: "sk-exp", DefaultModel: "m-exp", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m-exp", ProviderID: providerID, Name: "exp-model", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{providers: providers}

	// Use a zero TTL so entries expire immediately
	expProto := map[domain.ProviderKind]infrastructure.ChatProtocol{
		domain.ProviderOpenAICompat: &mockChatProto{label: "openai"},
	}
	reg := infrastructure.NewModelRegistry(mr, pr, expProto, nil, 0)

	// First call populates cache
	_, _, err := reg.Resolve(context.Background(), "t-exp", "exp-model")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Clear repos — entry should be expired by zero TTL
	mr.models = nil
	delete(pr.providers, providerID)

	_, _, err = reg.Resolve(context.Background(), "t-exp", "exp-model")
	if err == nil {
		t.Fatal("expected error after cache expiry + repo wipe, got nil")
	}
}

func TestModelRegistry_Resolve_ModelRepoError(t *testing.T) {
	mr := &mockModelRepo{err: errors.New("db down")}
	pr := &mockProviderRepo{}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.Resolve(context.Background(), "t1", "any")
	if err == nil {
		t.Fatal("expected error from model repo, got nil")
	}
}

func TestModelRegistry_Resolve_ProviderRepoError(t *testing.T) {
	providerID := "prov-err"
	models := []domain.Model{
		{ID: "m-err", ProviderID: providerID, Name: "err-model", Enabled: true},
	}

	mr := &mockModelRepo{models: models}
	pr := &mockProviderRepo{err: errors.New("provider db down")}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.Resolve(context.Background(), "t1", "err-model")
	if err == nil {
		t.Fatal("expected error from provider repo, got nil")
	}
}

func TestModelRegistry_ListChatModels_Error(t *testing.T) {
	mr := &mockModelRepo{err: errors.New("list error")}
	pr := &mockProviderRepo{}
	reg := newTestRegistry(mr, pr)

	_, err := reg.ListChatModelsByTenant(context.Background(), "t1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
