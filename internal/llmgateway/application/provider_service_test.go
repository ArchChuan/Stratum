package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockProviderRepo struct {
	providers map[string]*domain.Provider
	err       error
	// getErrs 模拟读取路径解密失败（历史明文/损坏密文）：Get 报错，
	// GetMeta 不受影响——Update 必须仍能带新 key 重存该 provider。
	getErrs map[string]error
}

func (m *mockProviderRepo) Create(_ context.Context, _ string, p *domain.Provider) error {
	if m.err != nil {
		return m.err
	}
	if m.providers == nil {
		m.providers = make(map[string]*domain.Provider)
	}
	m.providers[p.ID] = p
	return nil
}

func (m *mockProviderRepo) Get(_ context.Context, _, id string) (*domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	if getErr := m.getErrs[id]; getErr != nil {
		return nil, getErr
	}
	p, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func (m *mockProviderRepo) GetMeta(_ context.Context, _, id string) (*domain.Provider, error) {
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

func (m *mockProviderRepo) List(_ context.Context, _ string) ([]domain.Provider, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]domain.Provider, 0, len(m.providers))
	for _, p := range m.providers {
		out = append(out, *p)
	}
	return out, nil
}

func (m *mockProviderRepo) Update(_ context.Context, _ string, p *domain.Provider) error {
	if m.err != nil {
		return m.err
	}
	if m.providers == nil {
		return errors.New("provider not found")
	}
	m.providers[p.ID] = p
	return nil
}

func (m *mockProviderRepo) Delete(_ context.Context, _, id string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.providers[id]; !ok {
		return errors.New("provider not found")
	}
	delete(m.providers, id)
	return nil
}

type mockModelRepo struct {
	models []domain.Model
	err    error
}

func (m *mockModelRepo) Create(_ context.Context, _ string, _ *domain.Model) error {
	return m.err
}

func (m *mockModelRepo) Get(_ context.Context, _, id string) (*domain.Model, error) {
	for _, mdl := range m.models {
		if mdl.ID == id {
			return &mdl, nil
		}
	}
	return nil, m.err
}

func (m *mockModelRepo) List(_ context.Context, _ string, _ port.ModelFilter) ([]domain.Model, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.models, nil
}

func (m *mockModelRepo) Update(_ context.Context, _ string, _ *domain.Model) error {
	return m.err
}

func (m *mockModelRepo) UpsertDiscovered(_ context.Context, _, _ string, models []domain.Model) ([]domain.Model, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.models = append(m.models, models...)
	return models, nil
}

func (m *mockModelRepo) Delete(_ context.Context, _, _ string) error {
	return m.err
}

func (m *mockModelRepo) Toggle(_ context.Context, _, _ string, _ bool) error {
	return m.err
}

func (m *mockModelRepo) SetDefaultEmbedding(_ context.Context, _, _ string, _ bool) error {
	return m.err
}

type mockProviderRuntime struct {
	listModelsFn func(ctx context.Context, provider domain.Provider) ([]port.DiscoveredModel, error)
	healthFn     func(ctx context.Context, provider domain.Provider) error
}

func (m *mockProviderRuntime) Health(ctx context.Context, provider domain.Provider) error {
	if m.healthFn != nil {
		return m.healthFn(ctx, provider)
	}
	return nil
}

func (m *mockProviderRuntime) ListModels(ctx context.Context, provider domain.Provider) ([]port.DiscoveredModel, error) {
	if m.listModelsFn != nil {
		return m.listModelsFn(ctx, provider)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func newTestProviderService(pr *mockProviderRepo, mr *mockModelRepo, runtime port.ProviderRuntime) *application.ProviderService {
	return application.NewProviderService(pr, mr, runtime)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestProviderService_Create_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{}
	mr := &mockModelRepo{}
	svc := newTestProviderService(pr, mr, &mockProviderRuntime{})

	input := application.CreateProviderInput{
		Name:    "My OpenAI",
		Kind:    domain.ProviderOpenAICompat,
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-test",
	}
	provider, err := svc.Create(context.Background(), "tenant-1", input)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if provider.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if provider.Name != "My OpenAI" {
		t.Errorf("Name = %q, want %q", provider.Name, "My OpenAI")
	}
	if provider.Kind != domain.ProviderOpenAICompat {
		t.Errorf("Kind = %q", provider.Kind)
	}
	if !provider.Enabled {
		t.Error("expected Enabled=true")
	}
}

type providerInvalidator struct {
	tenants []string
}

func (i *providerInvalidator) Invalidate(tenantID string) {
	i.tenants = append(i.tenants, tenantID)
}

func TestProviderServiceInvalidatesRegistryAfterUpdate(t *testing.T) {
	provider := &domain.Provider{ID: "provider-1", Enabled: true}
	pr := &mockProviderRepo{providers: map[string]*domain.Provider{"provider-1": provider}}
	invalidator := &providerInvalidator{}
	svc := application.NewProviderService(pr, &mockModelRepo{}, &mockProviderRuntime{}, invalidator)

	_, err := svc.Update(context.Background(), "tenant-1", application.UpdateProviderInput{
		ID: "provider-1", Name: "updated", Kind: domain.ProviderOpenAICompat,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(invalidator.tenants) != 1 || invalidator.tenants[0] != "tenant-1" {
		t.Fatalf("invalidations = %v", invalidator.tenants)
	}
}

func TestProviderService_Create_RepoError(t *testing.T) {
	pr := &mockProviderRepo{err: errors.New("db error")}
	mr := &mockModelRepo{}
	svc := newTestProviderService(pr, mr, nil)

	_, err := svc.Create(context.Background(), "t1", application.CreateProviderInput{
		Name: "fail", Kind: domain.ProviderOpenAICompat,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProviderService_List_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Prov A", Kind: domain.ProviderOpenAICompat, Enabled: true},
			"p2": {ID: "p2", Name: "Prov B", Kind: domain.ProviderAnthropic, Enabled: false},
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	providers, err := svc.List(context.Background(), "t1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(providers))
	}
}

func TestProviderService_Get_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Prov A", Kind: domain.ProviderOpenAICompat, Enabled: true},
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	p, err := svc.Get(context.Background(), "t1", "p1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if p.Name != "Prov A" {
		t.Errorf("Name = %q", p.Name)
	}
}

func TestProviderService_Get_NotFound(t *testing.T) {
	pr := &mockProviderRepo{}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	_, err := svc.Get(context.Background(), "t1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestProviderService_Update_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Old Name", Kind: domain.ProviderOpenAICompat, BaseURL: "https://old.url", Enabled: true},
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	input := application.UpdateProviderInput{
		ID:      "p1",
		Name:    "New Name",
		BaseURL: "https://new.url",
	}
	p, err := svc.Update(context.Background(), "t1", input)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if p.Name != "New Name" {
		t.Errorf("Name = %q, want %q", p.Name, "New Name")
	}
	if p.BaseURL != "https://new.url" {
		t.Errorf("BaseURL = %q", p.BaseURL)
	}
}

func TestProviderService_Update_NotFound(t *testing.T) {
	pr := &mockProviderRepo{}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	_, err := svc.Update(context.Background(), "t1", application.UpdateProviderInput{ID: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

// TestProviderService_Update_ResaveKeyWhenGetFailsClosed 回归：存量明文/损坏
// 密文的 provider 在 Get（解密）失败时，带新 apiKey 的 Update 必须仍能重存。
// 此前 Update 无条件先 Get 解密旧 key，重存被永久锁死（500）。
func TestProviderService_Update_ResaveKeyWhenGetFailsClosed(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Legacy", Kind: domain.ProviderOpenAICompat,
				BaseURL: "https://legacy.url", Enabled: true},
		},
		// Get 模拟解密失败；GetMeta 不受影响。
		getErrs: map[string]error{"p1": errors.New("legacy plaintext")},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	input := application.UpdateProviderInput{
		ID:      "p1",
		Name:    "Legacy",
		APIKey:  "new-key",
		BaseURL: "https://legacy.url",
	}
	p, err := svc.Update(context.Background(), "t1", input)
	if err != nil {
		t.Fatalf("Update with new api key failed despite legacy plaintext: %v", err)
	}
	if p.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", p.APIKey, "new-key")
	}
	// 重存后读取路径恢复正常：Get 不再被旧 key 卡死。
	if got := pr.providers["p1"].APIKey; got != "new-key" {
		t.Errorf("stored APIKey = %q, want %q", got, "new-key")
	}
}

func TestProviderService_Delete_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "To Delete", Kind: domain.ProviderOpenAICompat, Enabled: true},
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	if err := svc.Delete(context.Background(), "t1", "p1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if len(pr.providers) != 0 {
		t.Fatal("expected provider to be removed")
	}
}

func TestProviderService_Delete_NotFound(t *testing.T) {
	pr := &mockProviderRepo{}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	err := svc.Delete(context.Background(), "t1", "nope")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestProviderService_DiscoverModels_HappyPath(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Discover Prov", Kind: domain.ProviderOpenAICompat, BaseURL: "https://disc.test", APIKey: "sk-disc"},
		},
	}
	mr := &mockModelRepo{}
	runtime := &mockProviderRuntime{
		listModelsFn: func(_ context.Context, _ domain.Provider) ([]port.DiscoveredModel, error) {
			return []port.DiscoveredModel{{Name: "gpt-4"}, {Name: "gpt-3.5-turbo"}}, nil
		},
	}
	svc := newTestProviderService(pr, mr, runtime)

	models, err := svc.DiscoverModels(context.Background(), "t1", "p1")
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].Name != "gpt-4" {
		t.Errorf("models[0].Name = %q, want %q", models[0].Name, "gpt-4")
	}
	if !models[0].ProviderManaged {
		t.Error("expected ProviderManaged=true")
	}
}

func TestProviderService_DiscoverModels_NoProtocol(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "No Proto", Kind: domain.ProviderOllama},
		},
	}
	// Empty protocol map
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	_, err := svc.DiscoverModels(context.Background(), "t1", "p1")
	if err == nil {
		t.Fatal("expected error for missing protocol, got nil")
	}
}

func TestProviderService_DiscoverModels_ProviderNotFound(t *testing.T) {
	pr := &mockProviderRepo{}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	_, err := svc.DiscoverModels(context.Background(), "t1", "nope")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestProviderService_HealthCheck_Success(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Healthy Prov", Kind: domain.ProviderOpenAICompat, BaseURL: "https://h.test", APIKey: "sk-h", DefaultModel: "gpt-4"},
		},
	}
	runtime := &mockProviderRuntime{
		healthFn: func(_ context.Context, _ domain.Provider) error { return nil },
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, runtime)

	if err := svc.HealthCheck(context.Background(), "t1", "p1"); err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
}

func TestProviderService_HealthCheck_Failure(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Unhealthy", Kind: domain.ProviderOpenAICompat, BaseURL: "https://bad.test", APIKey: "sk-bad"},
		},
	}
	runtime := &mockProviderRuntime{
		healthFn: func(_ context.Context, _ domain.Provider) error {
			return errors.New("connection refused")
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, runtime)

	err := svc.HealthCheck(context.Background(), "t1", "p1")
	if err == nil {
		t.Fatal("expected health check error, got nil")
	}
}

func TestProviderService_HealthCheck_NoProtocol(t *testing.T) {
	pr := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "No Proto", Kind: domain.ProviderOllama},
		},
	}
	svc := newTestProviderService(pr, &mockModelRepo{}, nil)

	err := svc.HealthCheck(context.Background(), "t1", "p1")
	if err == nil {
		t.Fatal("expected error for missing protocol, got nil")
	}
}
