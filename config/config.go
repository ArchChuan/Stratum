package config

import (
	"os"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

type Config struct {
	Port                    string
	NatsURL                 string
	MilvusHost              string
	MilvusPort              string
	OtelEndpoint            string
	PostgresURL             string
	RedisURL                string
	GitHubClientID          string
	GitHubClientSecret      string
	JWTPrivateKeyPEM        string
	GlobalAdminGitHubLogin  string
	FrontendURL             string
	GitHubCallbackURL       string
	SecureCookies           bool
	GlobalAgentSystemPrompt string
	QwenBaseURL             string
	ZhipuBaseURL            string
	Opik                    OpikConfig
	TracePayload            TracePayloadConfig
	MemoryPipeline          MemoryPipelineConfig
}

type OpikConfig struct {
	URL       string
	Project   string
	Workspace string
	APIKey    string
	Timeout   time.Duration
}

type TracePayloadConfig struct {
	Enabled   bool
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseTLS    bool
}

type MemoryPipelineConfig struct {
	Enabled               bool
	NatsURL               string
	PollInterval          time.Duration
	BatchSize             int
	EmbedWorkers          int
	EnrichWorkers         int
	EmbedAckWait          time.Duration
	EnrichAckWait         time.Duration
	MaxDeliver            int
	EnrichModel           string
	SummaryModel          string
	SummaryTokenThreshold int
	EnrichmentPrompt      string
	SummaryPrompt         string
}

func Load() (*Config, error) {
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	return &Config{
		Port:                    getEnv("PORT", "8080"),
		NatsURL:                 natsURL,
		MilvusHost:              getEnv("MILVUS_HOST", "localhost"),
		MilvusPort:              getEnv("MILVUS_PORT", "19530"),
		OtelEndpoint:            getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		PostgresURL:             getEnv("POSTGRES_URL", "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"),
		RedisURL:                getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubClientID:          getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:      getEnv("GITHUB_CLIENT_SECRET", ""),
		JWTPrivateKeyPEM:        getEnv("JWT_PRIVATE_KEY_PEM", ""),
		GlobalAdminGitHubLogin:  getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", "ArchChuan"),
		FrontendURL:             getEnv("FRONTEND_URL", "http://localhost:3002"),
		GitHubCallbackURL:       getEnv("GITHUB_CALLBACK_URL", "http://localhost:8080/auth/github/callback"),
		SecureCookies:           getEnv("SECURE_COOKIES", "") == "true",
		GlobalAgentSystemPrompt: getEnv("GLOBAL_AGENT_SYSTEM_PROMPT", ""),
		QwenBaseURL:             getEnv("QWEN_BASE_URL", ""),
		ZhipuBaseURL:            getEnv("ZHIPU_BASE_URL", ""),
		Opik: OpikConfig{
			URL:       getEnv("OPIK_URL", ""),
			Project:   getEnv("OPIK_PROJECT", "stratum"),
			Workspace: getEnv("OPIK_WORKSPACE", "default"),
			APIKey:    getEnv("OPIK_API_KEY", ""),
			Timeout:   constants.DefaultOpikTimeout,
		},
		TracePayload: TracePayloadConfig{
			Enabled:   getEnv("TRACE_PAYLOAD_ENABLED", "") == "true",
			Endpoint:  getEnv("TRACE_PAYLOAD_ENDPOINT", ""),
			AccessKey: getEnv("TRACE_PAYLOAD_ACCESS_KEY", ""),
			SecretKey: getEnv("TRACE_PAYLOAD_SECRET_KEY", ""),
			Bucket:    getEnv("TRACE_PAYLOAD_BUCKET", constants.DefaultTracePayloadBucket),
			UseTLS:    getEnv("TRACE_PAYLOAD_USE_TLS", "") == "true",
		},
		MemoryPipeline: MemoryPipelineConfig{
			Enabled:               getEnv("MEMORY_PIPELINE_ENABLED", "") == "true",
			NatsURL:               natsURL,
			PollInterval:          constants.MemoryOutboxPollInterval,
			BatchSize:             constants.MemoryOutboxBatchSize,
			EmbedWorkers:          constants.EmbedderWorkerCount,
			EnrichWorkers:         constants.EnricherWorkerCount,
			EmbedAckWait:          constants.EmbedderAckWait,
			EnrichAckWait:         constants.EnricherAckWait,
			MaxDeliver:            constants.EmbedderMaxDeliver,
			EnrichModel:           getEnv("MEMORY_ENRICH_MODEL", "qwen-turbo"),
			SummaryModel:          getEnv("MEMORY_SUMMARY_MODEL", "qwen-plus"),
			SummaryTokenThreshold: constants.EnricherSummaryTokenThreshold,
		},
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
