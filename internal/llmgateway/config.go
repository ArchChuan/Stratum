package llmgateway

import (
	"os"

	"go.uber.org/zap"
)

// ProviderEntry defines a provider registration with factory function.
type ProviderEntry struct {
	Provider ModelProvider
	APIKey   string
	BaseURL  string
	Factory  func(apiKey, baseURL string, logger *zap.Logger) *OpenAICompatClient
}

type Config struct {
	QwenAPIKey      string
	QwenBaseURL     string
	ZhipuAPIKey     string
	DefaultProvider ModelProvider
}

func LoadConfig() *Config {
	qwenBase := os.Getenv("QWEN_BASE_URL")
	if qwenBase == "" {
		qwenBase = qwenBaseURL
	}
	return &Config{
		QwenAPIKey:      os.Getenv("QWEN_API_KEY"),
		QwenBaseURL:     qwenBase,
		ZhipuAPIKey:     os.Getenv("ZHIPU_API_KEY"),
		DefaultProvider: ProviderQwen,
	}
}

func InitializeGateway(cfg *Config, logger *zap.Logger) *Gateway {
	gateway := NewGateway().WithLogger(logger)

	entries := []ProviderEntry{
		{
			Provider: ProviderQwen,
			APIKey:   cfg.QwenAPIKey,
			BaseURL:  cfg.QwenBaseURL,
			Factory:  NewQwenClientWithBase,
		},
		{
			Provider: ProviderZhipu,
			APIKey:   cfg.ZhipuAPIKey,
			BaseURL:  zhipuBaseURL,
			Factory:  NewZhipuClientWithBase,
		},
	}

	for _, e := range entries {
		if e.APIKey == "" {
			continue
		}
		client := e.Factory(e.APIKey, e.BaseURL, logger)
		gateway.RegisterClient(e.Provider, client)
		gateway.RegisterEmbeddingClient(e.Provider, client)
	}

	if cfg.DefaultProvider != "" {
		gateway.SetDefault(cfg.DefaultProvider)
	} else {
		gateway.SetDefault(ProviderQwen)
	}

	return gateway
}
