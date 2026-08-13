package port

import "context"

// ModelCatalog exposes the readable model catalogue (chat + embeddings).
// models 已提升为 public 平台全局目录，方法不再区分租户维度。
// Implemented by infrastructure.Gateway. Consumed by application use-cases
// that need to surface model names to API/UX layers.
type ModelCatalog interface {
	ListChatModels() []string
	ListEmbeddingModels() []string
	ListChatModelsByTenant(ctx context.Context) ([]string, error)
	ListEmbeddingModelsByTenant(ctx context.Context) ([]string, error)
}
