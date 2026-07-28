// Package application exposes use-case services for the llmgateway context.
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// ModelService surfaces the available chat and embedding model names. The
// underlying catalogue is provided via a consumer-side port so HTTP
// handlers do not need to import the gateway infrastructure package.
//
// Deprecated: Prefer the tenant-aware Catalogue(ctx, tenantID) method.
// The no-argument Catalogue(ctx) variant returns empty slices when backed
// by a ModelRegistry that has no tenant context.
type ModelService struct {
	catalog  port.ModelCatalog             // backward-compatible interface
	registry *infrastructure.ModelRegistry // tenant-aware resolution
}

// NewModelService wires a ModelService with the provided registry. The
// registry must satisfy port.ModelCatalog so that the legacy Catalogue
// method continues to compile (even if it returns empty results).
func NewModelService(registry *infrastructure.ModelRegistry) *ModelService {
	return &ModelService{
		catalog:  registry,
		registry: registry,
	}
}

// Catalogue returns chat and embedding model names. Returned slices are
// never nil (callers can iterate without nil checks).
//
// Deprecated: Use Catalogue(ctx, tenantID) for tenant-scoped results.
func (s *ModelService) Catalogue(_ context.Context) (chat, embedding []string) {
	chat = s.catalog.ListChatModels()
	if chat == nil {
		chat = []string{}
	}
	embedding = s.catalog.ListEmbeddingModels()
	if embedding == nil {
		embedding = []string{}
	}
	return chat, embedding
}

// CatalogueWithTenant returns chat and embedding model names scoped to a
// tenant. It delegates to the underlying ModelRegistry which queries
// the tenant's enabled models. Returned slices are never nil.
func (s *ModelService) CatalogueWithTenant(ctx context.Context, tenantID string) (chat, embedding []string) {
	chat, err := s.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil || chat == nil {
		chat = []string{}
	}
	embedding, err = s.registry.ListEmbeddingModelsByTenant(ctx, tenantID)
	if err != nil || embedding == nil {
		embedding = []string{}
	}
	return chat, embedding
}
