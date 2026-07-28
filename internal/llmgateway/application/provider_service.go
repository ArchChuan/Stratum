package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
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
//
// DDD note: this service imports infrastructure.ChatProtocol and
// infrastructure.ProviderConfig directly (plan-mandated layering
// exception — see SDD task 8 brief).
type ProviderService struct {
	repo       port.ProviderRepository
	modelRepo  port.ModelRepository
	chatProtos map[domain.ProviderKind]infrastructure.ChatProtocol
}

// NewProviderService returns a ProviderService wired with the given
// repository and protocol map.
func NewProviderService(
	repo port.ProviderRepository,
	modelRepo port.ModelRepository,
	chatProtos map[domain.ProviderKind]infrastructure.ChatProtocol,
) *ProviderService {
	return &ProviderService{
		repo:       repo,
		modelRepo:  modelRepo,
		chatProtos: chatProtos,
	}
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
	// Best-effort model discovery — log but never fail the create operation.
	if _, err := s.DiscoverModels(ctx, tenantID, p.ID); err != nil {
		// discovery failure is non-fatal
	}
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
func (s *ProviderService) Update(ctx context.Context, tenantID string, input UpdateProviderInput) (*domain.Provider, error) {
	existing, err := s.repo.Get(ctx, tenantID, input.ID)
	if err != nil {
		return nil, fmt.Errorf("provider service: get for update: %w", err)
	}
	existing.Name = input.Name
	existing.Kind = input.Kind
	existing.BaseURL = input.BaseURL
	existing.APIKey = input.APIKey
	existing.DefaultModel = input.DefaultModel
	if err := s.repo.Update(ctx, tenantID, existing); err != nil {
		return nil, fmt.Errorf("provider service: update: %w", err)
	}
	return existing, nil
}

// Delete removes a provider by ID.
func (s *ProviderService) Delete(ctx context.Context, tenantID, id string) error {
	return s.repo.Delete(ctx, tenantID, id)
}

// DiscoverModels queries the provider's API for available models and upserts
// them into the model repository. Only chat models are discovered (embedding
// model discovery is handled separately when needed).
func (s *ProviderService) DiscoverModels(ctx context.Context, tenantID, providerID string) ([]domain.Model, error) {
	provider, err := s.repo.Get(ctx, tenantID, providerID)
	if err != nil {
		return nil, fmt.Errorf("discover models: %w", err)
	}
	proto, ok := s.chatProtos[provider.Kind]
	if !ok {
		return nil, fmt.Errorf("discover models: no protocol for kind %q", provider.Kind)
	}
	cfg := infrastructure.ProviderConfig{
		Name:    provider.Name,
		BaseURL: provider.BaseURL,
		APIKey:  provider.APIKey,
	}
	names, err := proto.ListModels(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("discover models: list from provider: %w", err)
	}
	models := make([]domain.Model, 0, len(names))
	for _, name := range names {
		models = append(models, domain.Model{
			TenantID:        tenantID,
			ProviderID:      providerID,
			Name:            name,
			DisplayName:     name,
			Capabilities:    []domain.ModelCapability{domain.CapChat},
			ProviderManaged: true,
			Enabled:         true,
		})
	}
	return s.modelRepo.UpsertDiscovered(ctx, tenantID, providerID, models)
}

// HealthCheck verifies that the provider is reachable by calling the
// configured health model endpoint.
func (s *ProviderService) HealthCheck(ctx context.Context, tenantID, providerID string) error {
	provider, err := s.repo.Get(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	proto, ok := s.chatProtos[provider.Kind]
	if !ok {
		return fmt.Errorf("no protocol for kind %q", provider.Kind)
	}
	cfg := infrastructure.ProviderConfig{
		Name:        provider.Name,
		BaseURL:     provider.BaseURL,
		APIKey:      provider.APIKey,
		HealthModel: provider.DefaultModel,
	}
	return proto.Health(ctx, cfg)
}
