// Package application exposes use-case services for the llmgateway context.
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// ModelService surfaces the available chat and embedding model names. The
// underlying catalogue is provided via a consumer-side port so HTTP
// handlers do not need to import the gateway infrastructure package.
type ModelService struct {
	catalog port.ModelCatalog
}

// NewModelService wires a ModelService with the provided registry. The
// registry must satisfy port.ModelCatalog so that the legacy Catalogue
// method continues to compile (even if it returns empty results).
func NewModelService(catalog port.ModelCatalog) *ModelService {
	return &ModelService{catalog: catalog}
}

// CatalogueWithTenant returns chat and embedding model names scoped to a
// tenant. It delegates to the underlying ModelRegistry which queries
// the tenant's enabled models. Returned slices are never nil.
func (s *ModelService) CatalogueWithTenant(ctx context.Context, tenantID string) (chat, embedding []string) {
	chat, err := s.catalog.ListChatModelsByTenant(ctx)
	if err != nil || chat == nil {
		chat = []string{}
	}
	embedding, err = s.catalog.ListEmbeddingModelsByTenant(ctx)
	if err != nil || embedding == nil {
		embedding = []string{}
	}
	return chat, embedding
}
