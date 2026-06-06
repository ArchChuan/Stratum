package llmgateway

import (
	"os"

	"go.uber.org/zap"
)

type Config struct {
	QwenAPIKey      string
	ZhipuAPIKey     string
	DefaultProvider ModelProvider
}

func LoadConfig() *Config {
	return &Config{
		QwenAPIKey:      os.Getenv("QWEN_API_KEY"),
		ZhipuAPIKey:     os.Getenv("ZHIPU_API_KEY"),
		DefaultProvider: ProviderQwen,
	}
}

func InitializeGateway(cfg *Config, logger *zap.Logger) *Gateway {
	gateway := NewGateway()

	if cfg.QwenAPIKey != "" {
		qwenClient := NewQwenClient(cfg.QwenAPIKey, logger)
		gateway.RegisterClient(ProviderQwen, qwenClient)
		gateway.RegisterEmbeddingClient(ProviderQwen, qwenClient)
	}

	if cfg.ZhipuAPIKey != "" {
		zhipuClient := NewZhipuClient(cfg.ZhipuAPIKey, logger)
		gateway.RegisterClient(ProviderZhipu, zhipuClient)
		gateway.RegisterEmbeddingClient(ProviderZhipu, zhipuClient)
	}

	if cfg.DefaultProvider != "" {
		gateway.SetDefault(cfg.DefaultProvider)
	} else {
		gateway.SetDefault(ProviderQwen)
	}

	return gateway
}
