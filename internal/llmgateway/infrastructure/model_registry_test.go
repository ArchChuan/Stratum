package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// ---------------------------------------------------------------------------
// Mock repositories
// ---------------------------------------------------------------------------

type mockModelRepo struct {
	models    []domain.Model
	err       error
	listCalls int
}

func (m *mockModelRepo) List(ctx context.Context, filter port.ModelFilter) ([]domain.Model, error) {
	m.listCalls++
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

func (m *mockModelRepo) Create(ctx context.Context, model *domain.Model) error {
	return m.err
}

func (m *mockModelRepo) Get(ctx context.Context, id string) (*domain.Model, error) {
	for _, model := range m.models {
		if model.ID == id {
			return &model, nil
		}
	}
	return nil, m.err
}

func (m *mockModelRepo) Update(ctx context.Context, model *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.err
}

func (m *mockModelRepo) UpsertDiscovered(ctx context.Context, providerID string, models []domain.Model) ([]domain.Model, error) {
	return models, m.err
}

func (m *mockModelRepo) Delete(ctx context.Context, id string) error {
	return m.err
}

func (m *mockModelRepo) Toggle(ctx context.Context, id string, enabled bool) error {
	return m.err
}

type mockProviderRepo struct {
	providers map[string]*domain.Provider
	err       error
}

func (m *mockProviderRepo) Get(ctx context.Context, id string) (*domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	p, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func (m *mockProviderRepo) GetMeta(ctx context.Context, id string) (*domain.Provider, error) {
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

func (m *mockProviderRepo) Create(ctx context.Context, p *domain.Provider) error {
	return m.err
}

func (m *mockProviderRepo) List(ctx context.Context) ([]domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]domain.Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, *p)
	}
	return out, nil
}

func (m *mockProviderRepo) Update(ctx context.Context, p *domain.Provider, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return m.err
}

func (m *mockProviderRepo) Delete(ctx context.Context, id string) error {
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

func float64Ptr(v float64) *float64 { return &v }

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

// modelWith 构造一个 enabled 的 embedding 模型；def 控制 DefaultEmbedding 标记。
func modelWith(name, providerID string, def bool) domain.Model {
	return domain.Model{ID: name, Name: name, ProviderID: providerID,
		Capabilities:     []domain.ModelCapability{domain.CapEmbedding},
		Enabled:          true,
		DefaultEmbedding: def,
	}
}

// enabledProvider/disabledProvider 构造 OpenAI-compatible provider（newTestRegistry
// 的 embedProtos 只注册了该 kind）。
func enabledProvider() *domain.Provider {
	return &domain.Provider{Kind: domain.ProviderOpenAICompat, Enabled: true}
}

func disabledProvider() *domain.Provider {
	return &domain.Provider{Kind: domain.ProviderOpenAICompat, Enabled: false}
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

	cfg, proto, err := reg.Resolve(context.Background(), "gpt-4")
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
	cfg1, _, err := reg.Resolve(context.Background(), "model-two")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Mutate the repos to prove the registry does NOT re-read on the second call
	mr.models = nil
	delete(pr.providers, providerID)

	// Second call must still succeed from cache
	cfg2, proto2, err := reg.Resolve(context.Background(), "model-two")
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

	_, _, err := reg.Resolve(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown model, got nil")
	}
}

// TestModelRegistry_Resolve_ExplicitModelNotInCatalogFailsClosed 验证显式请求
// 的模型不在目录时 fail-closed：即使存在 provider 默认模型，也禁止静默降级
// 到默认模型（失效模型配置必须暴露给监控报警，而不是悄悄换模型）。
func TestModelRegistry_Resolve_ExplicitModelNotInCatalogFailsClosed(t *testing.T) {
	providerID := "prov-default"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Default Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://default.test", APIKey: "sk-d", DefaultModel: "orphan-default", Enabled: true,
		},
	}
	reg := newTestRegistry(&mockModelRepo{models: nil}, &mockProviderRepo{providers: providers})

	_, _, err := reg.Resolve(context.Background(), "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, infrastructure.ErrModelNotInCatalog.Error())
}

// TestModelRegistry_Resolve_EmptyModelWithoutDefaultFailsClosed 验证空模型且
// 目录中无任何默认/推荐模型时 fail-closed（无兜底模型必须走监控报警链路）。
func TestModelRegistry_Resolve_EmptyModelWithoutDefaultFailsClosed(t *testing.T) {
	reg := newTestRegistry(&mockModelRepo{models: nil}, &mockProviderRepo{providers: map[string]*domain.Provider{}})

	_, _, err := reg.Resolve(context.Background(), "")
	require.Error(t, err)
	require.ErrorContains(t, err, infrastructure.ErrNoDefaultModel.Error())
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

	_, _, err := reg.Resolve(context.Background(), "llama3")
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

	cfg, proto, err := reg.ResolveEmbedding(context.Background(), "text-embedding-3")
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

	_, _, err := reg.ResolveEmbedding(context.Background(), "claude-3")
	if err == nil {
		t.Fatal("expected error for missing embed protocol, got nil")
	}
}

func TestModelRegistry_ResolveEmbedding_ModelNotFound(t *testing.T) {
	mr := &mockModelRepo{models: []domain.Model{}}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{}}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.ResolveEmbedding(context.Background(), "nope")
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

	names, err := reg.ListChatModelsByTenant(context.Background())
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

	names, err := reg.ListChatModelsByTenant(context.Background())
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

	names, err := reg.ListEmbeddingModelsByTenant(context.Background())
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

	chat, err := reg.ListChatModelsByTenant(context.Background())
	if err != nil {
		t.Fatalf("ListChatModelsByTenant: %v", err)
	}
	if len(chat) != 1 || chat[0] != "chat-ok" {
		t.Fatalf("chat models = %v, want [chat-ok]", chat)
	}
	embedding, err := reg.ListEmbeddingModelsByTenant(context.Background())
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

	if _, err := reg.ListChatModelsByTenant(context.Background()); err == nil {
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

	if _, _, err := reg.Resolve(context.Background(), "embed-only"); err == nil {
		t.Fatal("embedding-only model must not resolve for chat")
	}
	if _, _, err := reg.Resolve(context.Background(), "chat-disabled"); err == nil {
		t.Fatal("model from disabled provider must not resolve")
	}
}

func TestModelRegistry_Warm(t *testing.T) {
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

	err := reg.Warm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cache is populated — a resolve should hit cache even after wiping repos
	mr.models = nil
	delete(pr.providers, providerID)

	cfg, _, err := reg.Resolve(context.Background(), "gpt-4")
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
	_, _, err := reg.Resolve(context.Background(), "test-model")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Invalidate the tenant
	reg.Invalidate()

	// Now blow away the repos; a cache hit would still succeed,
	// but the entry was evicted so it must hit the DB again.
	mr.models = nil
	delete(pr.providers, providerID)

	_, _, err = reg.Resolve(context.Background(), "test-model")
	if err == nil {
		t.Fatal("expected error after invalidation + repo wipe, got nil")
	}
}

func TestModelRegistry_Invalidate_ClearsGlobalCache(t *testing.T) {
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

	// 预热两条缓存条目（单层全局缓存，key 为 chat:<modelName>）。
	reg.Resolve(context.Background(), "model-a")
	reg.Resolve(context.Background(), "model-b")

	// 全局 Invalidate 一次性清空全部缓存（不再区分租户维度）。
	reg.Invalidate()

	mr.models = nil
	delete(pr.providers, providerID)

	// 缓存已整体失效且 repos 已清空 → 两条都必须 fail。
	for _, name := range []string{"model-a", "model-b"} {
		if _, _, err := reg.Resolve(context.Background(), name); err == nil {
			t.Fatalf("%s should fail after global invalidation + repo wipe", name)
		}
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
	_, _, err := reg.Resolve(context.Background(), "exp-model")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Clear repos — entry should be expired by zero TTL
	mr.models = nil
	delete(pr.providers, providerID)

	_, _, err = reg.Resolve(context.Background(), "exp-model")
	if err == nil {
		t.Fatal("expected error after cache expiry + repo wipe, got nil")
	}
}

func TestModelRegistry_Resolve_ModelRepoError(t *testing.T) {
	mr := &mockModelRepo{err: errors.New("db down")}
	pr := &mockProviderRepo{}
	reg := newTestRegistry(mr, pr)

	_, _, err := reg.Resolve(context.Background(), "any")
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

	_, _, err := reg.Resolve(context.Background(), "err-model")
	if err == nil {
		t.Fatal("expected error from provider repo, got nil")
	}
}

func TestModelRegistry_ListChatModels_Error(t *testing.T) {
	mr := &mockModelRepo{err: errors.New("list error")}
	pr := &mockProviderRepo{}
	reg := newTestRegistry(mr, pr)

	_, err := reg.ListChatModelsByTenant(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveDefaultEmbeddingModel(t *testing.T) {
	// fake 数据：enabled 模型列表 + 各模型 DefaultEmbedding/provider enabled 状态
	tests := []struct {
		name      string
		models    []domain.Model              // List(Enabled, CapEmbedding) 返回
		providers map[string]*domain.Provider // providerID → provider
		want      string
	}{
		{"marked model wins over alphabetical first",
			[]domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p1", true)},
			map[string]*domain.Provider{"p1": enabledProvider()},
			"b-embed"},
		{"no marker falls back to first",
			[]domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p1", false)},
			map[string]*domain.Provider{"p1": enabledProvider()},
			"a-embed"},
		{"empty list returns empty",
			nil, map[string]*domain.Provider{}, ""},
		{"marked but provider disabled falls back to first",
			[]domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p2", true)},
			map[string]*domain.Provider{"p1": enabledProvider(), "p2": disabledProvider()},
			"a-embed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := newTestRegistry(&mockModelRepo{models: tc.models}, &mockProviderRepo{providers: tc.providers})
			got, err := reg.ResolveDefaultEmbeddingModel(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDefaultEmbeddingModel_ProviderLookupFailsClosed(t *testing.T) {
	// 模型 provider 缺失时解析必须 fail closed，不得默认放行
	models := []domain.Model{modelWith("orphan", "missing", false)}
	reg := newTestRegistry(&mockModelRepo{models: models}, &mockProviderRepo{providers: map[string]*domain.Provider{}})

	if _, err := reg.ResolveDefaultEmbeddingModel(context.Background()); err == nil {
		t.Fatal("missing provider must fail the resolution closed")
	}
}

// ---------------------------------------------------------------------------
// ModelPolicy 预计算（Task 5）
// ---------------------------------------------------------------------------

// TestResolve_reasoningFromCachedPolicy_noSecondListCall 验证 policy 预计算
// 吸收 ResolveReasoning 的 N+1：预热后 ResolveReasoning 不得再查 repo。
func TestResolve_reasoningFromCachedPolicy_noSecondListCall(t *testing.T) {
	providerID := "prov-reasoning"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Reasoning Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://reasoning.test", APIKey: "sk-r", DefaultModel: "qwen-turbo", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m-r", ProviderID: providerID, Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning}},
	}
	mr := &mockModelRepo{models: models}
	reg := newTestRegistry(mr, &mockProviderRepo{providers: providers})

	if _, _, err := reg.Resolve(context.Background(), "qwen-turbo"); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	mr.listCalls = 0
	if !reg.ResolveReasoning(context.Background(), "qwen-turbo") {
		t.Fatal("ResolveReasoning = false, want true (CapReasoning from cached policy)")
	}
	if mr.listCalls != 0 {
		t.Errorf("ResolveReasoning issued %d repo List calls, want 0 (policy precomputed)", mr.listCalls)
	}
}

// TestPolicyFor_precomputedAndMissing 验证 PolicyFor：已解析模型返回预计算
// policy；未解析模型返回 nil（不触发 DB 查询）。
func TestPolicyFor_precomputedAndMissing(t *testing.T) {
	providerID := "prov-policy"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Policy Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://policy.test", APIKey: "sk-p", DefaultModel: "m-p", Enabled: true,
		},
	}
	models := []domain.Model{
		{ID: "m-p", ProviderID: providerID, Name: "m-p", Enabled: true,
			Capabilities:  []domain.ModelCapability{domain.CapChat},
			ContextWindow: 128000, MaxTokens: 32768,
			SamplingParams: &domain.SamplingParams{Temperature: float64Ptr(0.5)}},
	}
	mr := &mockModelRepo{models: models}
	reg := newTestRegistry(mr, &mockProviderRepo{providers: providers})

	if _, _, err := reg.Resolve(context.Background(), "m-p"); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	pol := reg.PolicyFor(context.Background(), "m-p")
	if pol == nil {
		t.Fatal("PolicyFor = nil for resolved model, want precomputed policy")
	}
	if pol.ContextWindow != 128000 || pol.MaxTokens != 32768 {
		t.Errorf("policy context/max = %d/%d, want 128000/32768", pol.ContextWindow, pol.MaxTokens)
	}
	if pol.SamplingDefaults == nil || pol.SamplingDefaults.Temperature == nil || *pol.SamplingDefaults.Temperature != 0.5 {
		t.Errorf("policy sampling defaults = %+v, want temperature 0.5", pol.SamplingDefaults)
	}
	if pol.Reasoning {
		t.Error("policy.Reasoning = true, want false (no CapReasoning)")
	}
	if pol := reg.PolicyFor(context.Background(), "never-resolved"); pol != nil {
		t.Errorf("PolicyFor(never-resolved) = %+v, want nil", pol)
	}
}

// TestPolicyFor_noModelRecordFromDefaultChain 验证 ② 级兜底（provider
// default_model 未入库）policy 为 nil：权威数据缺失语义，不误报能力。
func TestPolicyFor_noModelRecordFromDefaultChain(t *testing.T) {
	providerID := "prov-default"
	providers := map[string]*domain.Provider{
		providerID: {
			ID: providerID, Name: "Default Prov", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://default.test", APIKey: "sk-d", DefaultModel: "orphan-default", Enabled: true,
		},
	}
	reg := newTestRegistry(&mockModelRepo{models: nil}, &mockProviderRepo{providers: providers})

	if _, _, err := reg.Resolve(context.Background(), ""); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if pol := reg.PolicyFor(context.Background(), "orphan-default"); pol != nil {
		t.Errorf("PolicyFor = %+v, want nil (model record absent)", pol)
	}
}
