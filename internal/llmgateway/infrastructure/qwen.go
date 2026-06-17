package infrastructure

import "go.uber.org/zap"

const qwenBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

type QwenClient = OpenAICompatClient

func NewQwenClient(apiKey string, logger *zap.Logger) *QwenClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "qwen",
		BaseURL:     qwenBaseURL,
		APIKey:      apiKey,
		HealthModel: "qwen-turbo",
		Models:      []string{"qwen-turbo", "qwen-plus", "qwen-max", "qwen-long"},
	}, logger)
}

func NewQwenClientWithBase(apiKey, baseURL string, logger *zap.Logger) *QwenClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "qwen",
		BaseURL:     baseURL,
		APIKey:      apiKey,
		HealthModel: "qwen-turbo",
		Models:      []string{"qwen-turbo", "qwen-plus", "qwen-max", "qwen-long"},
	}, logger)
}
