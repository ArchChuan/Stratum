package infrastructure

// StaticModelCatalog implements port.ModelCatalog with hardcoded model names
// for all supported providers. Model lists are provider-fixed and do not
// depend on which tenants have API keys configured.
type StaticModelCatalog struct{}

func (StaticModelCatalog) ListChatModels() []string {
	return []string{
		// Qwen
		"qwen-max", "qwen-plus", "qwen-turbo", "qwen-long",
		// Zhipu
		"glm-4", "glm-4-flash", "glm-4-air", "glm-3-turbo",
	}
}

func (StaticModelCatalog) ListEmbeddingModels() []string {
	return []string{
		// Qwen
		"text-embedding-v3", "text-embedding-v2",
		// Zhipu
		"embedding-3",
	}
}
