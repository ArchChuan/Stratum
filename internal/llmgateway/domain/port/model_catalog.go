package port

import "context"

// ModelCatalog exposes the readable model catalogue (chat + embeddings).
// Implemented by infrastructure.Gateway. Consumed by application use-cases
// that need to surface model names to API/UX layers.
type ModelCatalog interface {
	ListChatModels() []string
	ListEmbeddingModels() []string
	ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error)
	ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error)
}
