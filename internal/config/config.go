package config

import (
	"context"
	"os"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"go.uber.org/zap"
)

type Config struct {
	Port              string
	NatsURL           string
	MilvusHost        string
	MilvusPort        string
	Neo4jURI          string
	Neo4jUser         string
	Neo4jPassword     string
	OtelEndpoint      string
	OpenAIAPIKey      string
}

type Services struct {
	GraphRAG    *knowledge.GraphRAG
	VectorStore *vector.VectorStore
}

func Load() (*Config, error) {
	return &Config{
		Port:              getEnv("PORT", "8080"),
		NatsURL:           getEnv("NATS_URL", "nats://localhost:4222"),
		MilvusHost:        getEnv("MILVUS_HOST", "localhost"),
		MilvusPort:        getEnv("MILVUS_PORT", "19530"),
		Neo4jURI:          getEnv("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUser:         getEnv("NEO4J_USER", "neo4j"),
		Neo4jPassword:     getEnv("NEO4J_PASSWORD", "password"),
		OtelEndpoint:      getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		OpenAIAPIKey:     getEnv("OPENAI_API_KEY", ""),
	}, nil
}

func InitializeServices(cfg *Config, logger *zap.Logger) (*Services, error) {
	ctx := context.Background()

	graphrag := knowledge.NewGraphRAG(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword, logger)
	if err := graphrag.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Neo4j", zap.Error(err))
		// 不中断启动，继续运行
	}

	vectorStore := vector.NewVectorStore(cfg.MilvusHost, cfg.MilvusPort, logger)
	if err := vectorStore.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Milvus", zap.Error(err))
		// 不中断启动，继续运行
	}

	return &Services{
		GraphRAG:    graphrag,
		VectorStore: vectorStore,
	}, nil
}

func (s *Services) Close() error {
	if err := s.GraphRAG.Close(); err != nil {
		return err
	}
	if err := s.VectorStore.Close(); err != nil {
		return err
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
