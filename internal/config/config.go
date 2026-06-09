// Package config handles application configuration.
package config

import (
	"context"
	"os"
	"strconv"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"go.uber.org/zap"
)

type TemporalConfig struct {
	HostPort                      string
	Namespace                     string
	TaskQueue                     string
	WorkerMaxConcurrentActivities int
	WorkerMaxConcurrentWorkflows  int
}

type Config struct {
	Port                   string
	NatsURL                string
	MilvusHost             string
	MilvusPort             string
	Neo4jURI               string
	Neo4jUser              string
	Neo4jPassword          string
	OtelEndpoint           string
	QwenAPIKey             string
	ZhipuAPIKey            string
	PostgresURL            string
	RedisURL               string
	GitHubClientID         string
	GitHubClientSecret     string
	JWTPrivateKeyPEM       string
	GlobalAdminGitHubLogin string
	FrontendURL            string
	GitHubCallbackURL      string
	SecureCookies          bool
	Temporal               TemporalConfig
}

type Services struct {
	GraphRAG    *knowledge.GraphRAG
	VectorStore *vector.VectorStore
}

func Load() (*Config, error) {
	return &Config{
		Port:                   getEnv("PORT", "8080"),
		NatsURL:                getEnv("NATS_URL", "nats://localhost:4222"),
		MilvusHost:             getEnv("MILVUS_HOST", "localhost"),
		MilvusPort:             getEnv("MILVUS_PORT", "19530"),
		Neo4jURI:               getEnv("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUser:              getEnv("NEO4J_USER", "neo4j"),
		Neo4jPassword:          getEnv("NEO4J_PASSWORD", "password"),
		OtelEndpoint:           getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		QwenAPIKey:             getEnv("QWEN_API_KEY", ""),
		ZhipuAPIKey:            getEnv("ZHIPU_API_KEY", ""),
		PostgresURL:            getEnv("POSTGRES_URL", "postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"),
		RedisURL:               getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubClientID:         getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:     getEnv("GITHUB_CLIENT_SECRET", ""),
		JWTPrivateKeyPEM:       getEnv("JWT_PRIVATE_KEY_PEM", ""),
		GlobalAdminGitHubLogin: getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", "ArchChuan"),
		FrontendURL:            getEnv("FRONTEND_URL", "http://localhost:3002"),
		GitHubCallbackURL:      getEnv("GITHUB_CALLBACK_URL", "http://localhost:3002/auth/github/callback"),
		SecureCookies:          getEnv("SECURE_COOKIES", "") == "true",
		Temporal: TemporalConfig{
			HostPort:                      getEnv("TEMPORAL_HOST_PORT", "localhost:7233"),
			Namespace:                     getEnv("TEMPORAL_NAMESPACE", "clawhermes"),
			TaskQueue:                     getEnv("TEMPORAL_TASK_QUEUE", "agent-react"),
			WorkerMaxConcurrentActivities: getEnvInt("TEMPORAL_WORKER_MAX_ACTIVITIES", 20),
			WorkerMaxConcurrentWorkflows:  getEnvInt("TEMPORAL_WORKER_MAX_WORKFLOWS", 100),
		},
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

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}
