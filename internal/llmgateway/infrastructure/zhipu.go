package infrastructure

import "go.uber.org/zap"

const zhipuBaseURL = "https://open.bigmodel.cn/api/paas/v4"

type ZhipuClient = OpenAICompatClient

func NewZhipuClient(apiKey string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     zhipuBaseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      []string{"glm-4-flash", "glm-4", "glm-4-air", "glm-4v"},
	}, logger)
}

func NewZhipuClientWithBase(apiKey, baseURL string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     baseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      []string{"glm-4-flash", "glm-4", "glm-4-air", "glm-4v"},
	}, logger)
}
