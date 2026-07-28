package infrastructure

import "go.uber.org/zap"

const zhipuBaseURL = "https://open.bigmodel.cn/api/paas/v4"

var zhipuChatModels = []string{
	"glm-5.2",
	"glm-4.7-flashx", "glm-4.7-flash", "glm-4.5-flash",
	"glm-4-plus", "glm-4", "glm-4-air", "glm-4-flash", "glm-4v",
}

type ZhipuClient = OpenAICompatClient

func NewZhipuClient(apiKey string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     zhipuBaseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      append([]string(nil), zhipuChatModels...),
	}, logger)
}

func NewZhipuClientWithBase(apiKey, baseURL string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     baseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      append([]string(nil), zhipuChatModels...),
	}, logger)
}
