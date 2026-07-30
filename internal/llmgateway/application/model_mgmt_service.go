package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// UpdateModelInput carries the fields that can be updated on a model.
type UpdateModelInput struct {
	ID            string                   `json:"id"`
	DisplayName   string                   `json:"displayName"`
	Capabilities  []domain.ModelCapability `json:"capabilities"`
	ContextWindow int                      `json:"contextWindow"`
	MaxTokens     int                      `json:"maxTokens"`
	InputPrice    float64                  `json:"inputPrice"`
	OutputPrice   float64                  `json:"outputPrice"`
	Recommended   bool                     `json:"recommended"`
}

// ModelMgmtService wraps model CRUD operations that are initiated by
// tenant administrators (as opposed to auto-discovery or runtime resolution).
type ModelMgmtService struct {
	repo        port.ModelRepository
	invalidator ModelCacheInvalidator
}

// ModelCacheInvalidator evicts tenant-scoped runtime model resolutions after management changes.
type ModelCacheInvalidator interface {
	Invalidate(tenantID string)
}

// NewModelMgmtService returns a ModelMgmtService wired with the given repo.
func NewModelMgmtService(repo port.ModelRepository, invalidators ...ModelCacheInvalidator) *ModelMgmtService {
	service := &ModelMgmtService{repo: repo}
	if len(invalidators) > 0 {
		service.invalidator = invalidators[0]
	}
	return service
}

// List returns models matching the given filter.
func (s *ModelMgmtService) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
	return s.repo.List(ctx, tenantID, filter)
}

// Get returns a single model by ID.
func (s *ModelMgmtService) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
	return s.repo.Get(ctx, tenantID, id)
}

// Update applies partial edits to a model's display and pricing fields.
func (s *ModelMgmtService) Update(ctx context.Context, tenantID string, input UpdateModelInput) (*domain.Model, error) {
	m, err := s.repo.Get(ctx, tenantID, input.ID)
	if err != nil {
		return nil, fmt.Errorf("model mgmt: get: %w", err)
	}
	m.DisplayName = input.DisplayName
	m.Capabilities = input.Capabilities
	m.ContextWindow = input.ContextWindow
	m.MaxTokens = input.MaxTokens
	m.InputPrice = input.InputPrice
	m.OutputPrice = input.OutputPrice
	m.Recommended = input.Recommended
	if err := s.repo.Update(ctx, tenantID, m); err != nil {
		return nil, fmt.Errorf("model mgmt: update: %w", err)
	}
	s.invalidate(tenantID)
	return m, nil
}

// Toggle enables or disables a model.
func (s *ModelMgmtService) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
	if err := s.repo.Toggle(ctx, tenantID, id, enabled); err != nil {
		return err
	}
	s.invalidate(tenantID)
	return nil
}

// Delete removes a model by ID.
func (s *ModelMgmtService) Delete(ctx context.Context, tenantID, id string) error {
	if err := s.repo.Delete(ctx, tenantID, id); err != nil {
		return err
	}
	s.invalidate(tenantID)
	return nil
}

func (s *ModelMgmtService) invalidate(tenantID string) {
	if s.invalidator != nil {
		s.invalidator.Invalidate(tenantID)
	}
}
