// Package config handles application configuration.
package config

import (
	"context"
	"os"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"go.uber.org/zap"
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
	OpenAIAPIKey           string
	PostgresURL            string
	RedisURL               string
	GitHubClientID         string
	GitHubClientSecret     string
	JWTPrivateKeyPEM       string
	GlobalAdminGitHubLogin string
	FrontendURL            string
	GitHubCallbackURL      string
	SecureCookies          bool
}

type Services struct {
	GraphRAG    *knowledge.GraphRAG
	VectorStore *vector.VectorStore
}

func Load() (*Config, error) {
	return &Config{
		Port:               getEnv("PORT", "8080"),
		NatsURL:            getEnv("NATS_URL", "nats://localhost:4222"),
		MilvusHost:         getEnv("MILVUS_HOST", "localhost"),
		MilvusPort:         getEnv("MILVUS_PORT", "19530"),
		Neo4jURI:           getEnv("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUser:          getEnv("NEO4J_USER", "neo4j"),
		Neo4jPassword:      getEnv("NEO4J_PASSWORD", "password"),
		OtelEndpoint:       getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		OpenAIAPIKey:       getEnv("OPENAI_API_KEY", ""),
		PostgresURL:        getEnv("POSTGRES_URL", "postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"),
		RedisURL:           getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubClientID:     getEnv("GITHUB_CLIENT_ID", "Ov23liGDZ841fQatJmA0"),
		GitHubClientSecret: getEnv("GITHUB_CLIENT_SECRET", "98542166e21b0a6f7928393ecb051c56021a145f"),
		JWTPrivateKeyPEM: getEnv("JWT_PRIVATE_KEY_PEM", `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAgPbyfEGfTQNNi9tbTYWcX4ZJcG/QLj/RE5/w+QPboC/nK2Hs
mUKxW8euz4bSPhaqEfS2W3iNe6zNtSUWlb/keRRbiBj5N34ciBwYQOlCUZdpwr02
HVsxSTnm3uVfJKXJPq24f6I+NrNHoWzjAKbJIM/+BVu3HggwsB6+1RS7yZL9VJ1r
MQ+7w17wGUnc82V+PB59oElgzDUmJoT0OMw2TGH9qbLrPa2nA/OXz56jU7FkTGdv
w3yPoM2+Tb6gUTH5DYGjT/QZ4MKSdJLA9SB5xRWmInc/g66oV70S1eNSK0d0a95Y
VU9WBRsLawPuf8QzaZ+apzK2V+oZjFCHHnh+5QIDAQABAoIBACcgRMMT+auYw+8Z
duLXFXEZwbAeDC/r5peon6g81mYMAntz8x8wT7TDqTKG+cQihih6cNThhzMtYx+B
CBAYrs1ZMsfgo8OFPEzDEUyoOBme8VRGqWNQpmxL59JaDnqE3cBpXh9C7tMTozjD
Wz94Wm7dC3k+sRiobXURbt4gszRdOAzDOomAy7CcyzTHjqLOuTIzkmP12R/yPm8A
uAHsCCu+6GgUwHOusgYhCbLlQ2IA/wNP6luZqHWFYC3UCH0Kboqz5HBsoCvj1JSP
U9Tuf0/i1XQpckWWf+sHhd/z74jW1rgKZ3xR4DjWD5miZ49x61ltzyGE0QD6swMB
BZf1lkUCgYEAy5xIy0ibF77MMKrij7mv4bFY3G8WQTV5n7kSDbiZkXDn7+9Hjbc5
0vYUWhQMC602Te8YILWeeTk+La0Ffdjm0lHQjne+VR17KVK6CpnRhFr1lY6m79RM
8t8QyqseWGb1suufbCmCUgwQusHwGVxwIodnjCpY8FTSqWWi/xI05D8CgYEAoiXH
s009RjqQ6/xBBJqavZtbv47nOOeB7czg9WBbTZQRuRK3X9ZQ8KPAupXmYaKJPLQG
qFnLCAHzbgd6d2UB4hshgcQFTFieqSFiW3q1BKdsOxQx3/cqHp4IDvS1WgNGXey+
ma62Yvh7pqIFkG/uLF40zgeVOz6XnK2B/gZTg9sCgYEAgyAtCS3DI/GuUpFawzDU
gkbScXPhIzGrGB/57ng5/h52YGD69dtQE/qCdNiAQWzVki8unLIaUvt4fbX12Ww8
iqpB495d5zbLQHuUcItLES/7BMwP2lghDjB2Ae9d5ZS5Gvb/fork8K3wgDWxyMNt
O+9z0iLbkDswSAO6iwZQpcUCgYAAlDX0U/BGEet2jD4HMC4hQy6+rlnxABKcsMCU
37Uzv7WYfZKeCvvbABquD970tknbJ6FmdHufGbKuz+QGDRxGnGYwOmzyataWMAJT
5UpEK/zc8SOEczN5TIMm2oTTP3O+3huIHPGVxOFcJPP0IhItomB549kKjxyneI8g
QxGFRQKBgQCazWPIJsa/If7OhhfVbd+A9HHQI6Czb88CX9eP0SxUNgl8HQ4SCaLX
RurnRKigqFXzJiW6imVcPOu1NT5I/tMPojFxqk/lFJpygk0DipKHqZx6jfzVcZVX
Xs7H7wghedooc+rnxfIKujoU34fW7t32blpGW6dSM/qwFPiEHXfkLQ==
-----END RSA PRIVATE KEY-----`),
		GlobalAdminGitHubLogin: getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", "ArchChuan"),
		FrontendURL:            getEnv("FRONTEND_URL", "http://localhost:3002"),
		GitHubCallbackURL:      getEnv("GITHUB_CALLBACK_URL", "http://localhost:3002/auth/github/callback"),
		SecureCookies:          getEnv("SECURE_COOKIES", "") == "true",
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
