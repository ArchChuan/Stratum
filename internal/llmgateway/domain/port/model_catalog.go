package port

// ModelCatalog exposes the readable model catalogue (chat + embeddings).
// Implemented by infrastructure.Gateway. Consumed by application use-cases
// that need to surface model names to API/UX layers.
type ModelCatalog interface {
	ListChatModels() []string
	ListEmbeddingModels() []string

	// TODO: add ListChatModelsByTenant(ctx, tenantID) once all
	// implementations provide it (see model management refactor).
}
