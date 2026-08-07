package application

import (
	"context"
	"fmt"

	"strings"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// genULID generates a unique identifier using UUID v7 (time-ordered).
func genULID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// CreateProviderInput carries the fields required to create a new provider.
type CreateProviderInput struct {
	Name    string              `json:"name"`
	Kind    domain.ProviderKind `json:"kind"`
	BaseURL string              `json:"baseUrl"`
	APIKey  string              `json:"apiKey"`
}

// UpdateProviderInput carries the fields that can be updated on a provider.
type UpdateProviderInput struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Kind         domain.ProviderKind `json:"kind"`
	BaseURL      string              `json:"baseUrl"`
	APIKey       string              `json:"apiKey"`
	DefaultModel string              `json:"defaultModel"`
}

// ProviderService orchestrates LLM provider CRUD operations and
// provider-managed tasks such as model discovery and health checks.
type ProviderService struct {
	repo        port.ProviderRepository
	modelRepo   port.ModelRepository
	runtime     port.ProviderRuntime
	invalidator ModelCacheInvalidator
}

// NewProviderService returns a ProviderService wired with the given
// repository and protocol map.
func NewProviderService(
	repo port.ProviderRepository,
	modelRepo port.ModelRepository,
	runtime port.ProviderRuntime,
	invalidators ...ModelCacheInvalidator,
) *ProviderService {
	service := &ProviderService{
		repo:      repo,
		modelRepo: modelRepo,
		runtime:   runtime,
	}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
}

// Create persists a new provider and kicks off best-effort model discovery.
func (s *ProviderService) Create(ctx context.Context, tenantID string, input CreateProviderInput) (*domain.Provider, error) {
	p := &domain.Provider{
		ID:       genULID(),
		TenantID: tenantID,
		Name:     input.Name,
		Kind:     input.Kind,
		BaseURL:  input.BaseURL,
		APIKey:   input.APIKey,
		Enabled:  true,
	}
	if err := s.repo.Create(ctx, tenantID, p); err != nil {
		return nil, fmt.Errorf("provider service: create: %w", err)
	}
	s.invalidate(tenantID)
	// Best-effort model discovery — log but never fail the create operation.
	_, _ = s.DiscoverModels(ctx, tenantID, p.ID)
	return p, nil
}

// List returns all providers for a tenant.
func (s *ProviderService) List(ctx context.Context, tenantID string) ([]domain.Provider, error) {
	return s.repo.List(ctx, tenantID)
}

// Get returns a single provider by ID.
func (s *ProviderService) Get(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
	return s.repo.Get(ctx, tenantID, id)
}

// Update applies partial updates to an existing provider.
// An empty APIKey means "keep existing". 元数据经 GetMeta 读取（不解密旧 key）：
// 存量明文/损坏密文的 provider 带新 key 重新保存必须可用，先解密旧 key
// 会把该 provider 永久锁死（Get 保持 fail closed 不变）。
func (s *ProviderService) Update(ctx context.Context, tenantID string, input UpdateProviderInput) (*domain.Provider, error) {
	existing, err := s.repo.GetMeta(ctx, tenantID, input.ID)
	if err != nil {
		return nil, fmt.Errorf("provider service: get for update: %w", err)
	}
	existing.Name = input.Name
	existing.Kind = input.Kind
	existing.BaseURL = input.BaseURL
	existing.DefaultModel = input.DefaultModel
	if input.APIKey != "" {
		existing.APIKey = input.APIKey
	}
	if err := s.repo.Update(ctx, tenantID, existing); err != nil {
		return nil, fmt.Errorf("provider service: update: %w", err)
	}
	s.invalidate(tenantID)
	return existing, nil
}

// Delete removes a provider by ID. Associated models are cascade-deleted by FK.
func (s *ProviderService) Delete(ctx context.Context, tenantID, id string) error {
	if err := s.repo.Delete(ctx, tenantID, id); err != nil {
		return fmt.Errorf("provider service: delete: %w", err)
	}
	s.invalidate(tenantID)
	return nil
}

// DiscoverModels queries the provider's API for available models and upserts
// them into the model repository. Model capabilities are inferred from the
// model name — models containing "embed" are classified as embedding, others
// default to chat.
func (s *ProviderService) DiscoverModels(ctx context.Context, tenantID, providerID string) ([]domain.Model, error) {
	provider, err := s.repo.Get(ctx, tenantID, providerID)
	if err != nil {
		return nil, fmt.Errorf("discover models: %w", err)
	}
	if s.runtime == nil {
		return nil, fmt.Errorf("discover models: no runtime for kind %q", provider.Kind)
	}
	discovered, err := s.runtime.ListModels(ctx, *provider)
	if err != nil {
		return nil, fmt.Errorf("discover models: list from provider: %w", err)
	}
	models := make([]domain.Model, 0, len(discovered))
	for _, dm := range discovered {
		models = append(models, domain.Model{
			TenantID:        tenantID,
			ProviderID:      providerID,
			Name:            dm.Name,
			DisplayName:     dm.Name,
			Capabilities:    inferCapabilities(dm.Name),
			ContextWindow:   dm.ContextWindow,
			MaxTokens:       dm.MaxOutputTokens,
			ProviderManaged: true,
			Enabled:         true,
		})
	}
	upserted, err := s.modelRepo.UpsertDiscovered(ctx, tenantID, providerID, models)
	if err != nil {
		return nil, err
	}
	s.invalidate(tenantID)
	return upserted, nil
}

func (s *ProviderService) invalidate(tenantID string) {
	if s.invalidator != nil {
		s.invalidator.Invalidate(tenantID)
	}
}

// inferCapabilities deduces model capabilities from the model name using
// provider-agnostic naming conventions. All major LLM providers follow the
// pattern of including "embed" in embedding model names.
func inferCapabilities(name string) []domain.ModelCapability {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "embed") {
		return []domain.ModelCapability{domain.CapEmbedding}
	}
	return []domain.ModelCapability{domain.CapChat}
}

// HealthCheck verifies that the provider is reachable by calling the
// configured health model endpoint.
func (s *ProviderService) HealthCheck(ctx context.Context, tenantID, providerID string) error {
	provider, err := s.repo.Get(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	if s.runtime == nil {
		return fmt.Errorf("no protocol for kind %q", provider.Kind)
	}
	return s.runtime.Health(ctx, *provider)
}
