package infrastructure

// StaticModelCatalog implements port.ModelCatalog with hardcoded model names
// for all supported providers. Model lists are provider-fixed and do not
// depend on which tenants have API keys configured.
type StaticModelCatalog struct{}

func (StaticModelCatalog) ListChatModels() []string {
	return []string{
		// 通义千问 (Qwen)
		"qwen-max", "qwen-max-latest",
		"qwen-plus", "qwen-plus-latest",
		"qwen-turbo", "qwen-turbo-latest",
		"qwen-long",
		// 智谱 AI (Zhipu / Z.ai)
		"glm-5.2",
		"glm-4.7-flashx", "glm-4.7-flash", "glm-4.5-flash",
		"glm-4-plus", "glm-4", "glm-4-air", "glm-4-flash", "glm-4v",
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
