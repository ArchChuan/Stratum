// Package config handles application configuration.
package config

import (
	"os"
)

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
	MemoryPipelineEnabled  bool
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
		PostgresURL:            getEnv("POSTGRES_URL", "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"),
		RedisURL:               getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubClientID:         getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:     getEnv("GITHUB_CLIENT_SECRET", ""),
		JWTPrivateKeyPEM:       getEnv("JWT_PRIVATE_KEY_PEM", ""),
		GlobalAdminGitHubLogin: getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", "ArchChuan"),
		FrontendURL:            getEnv("FRONTEND_URL", "http://localhost:3002"),
		GitHubCallbackURL:      getEnv("GITHUB_CALLBACK_URL", "http://localhost:8080/auth/github/callback"),
		SecureCookies:          getEnv("SECURE_COOKIES", "") == "true",
		MemoryPipelineEnabled:  getEnv("MEMORY_PIPELINE_ENABLED", "") == "true",
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
