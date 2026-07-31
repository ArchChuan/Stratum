package config

import (
	"fmt"
	"net"
	"net/url"
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
	PasswordAuthEnabled     bool
	AdminUsername           string
	AdminPassword           string
	AvatarDir               string
	GitHubClientID          string
	GitHubClientSecret      string
	GitHubAuthorizeURL      string
	GitHubTokenURL          string
	GitHubUserURL           string
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
	InternalAPI             InternalAPIConfig
}

type InternalAPIConfig struct {
	Port                   string
	CertFile               string
	KeyFile                string
	ClientCAFile           string
	PlatformMCPDialAddress string
}

func (c InternalAPIConfig) Configured() bool {
	return c.CertFile != "" && c.KeyFile != "" && c.ClientCAFile != ""
}

func (c InternalAPIConfig) validate() error {
	configuredFiles := 0
	for _, path := range []string{c.CertFile, c.KeyFile, c.ClientCAFile} {
		if path != "" {
			configuredFiles++
		}
	}
	if configuredFiles == 0 {
		if c.PlatformMCPDialAddress != "" {
			return fmt.Errorf("Platform MCP dial address requires internal API workload TLS")
		}
		return nil
	}
	if configuredFiles == 3 {
		return validatePlatformMCPDialAddress(c.PlatformMCPDialAddress)
	}
	return fmt.Errorf("internal API TLS certificate, key, and client CA must be configured together")
}

func validatePlatformMCPDialAddress(address string) error {
	if address == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("Platform MCP dial address must be host:port")
	}
	return nil
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
	authorizeURL, tokenURL, userURL, err := githubOAuthEndpoints()
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		PasswordAuthEnabled:     getEnv("PASSWORD_AUTH_ENABLED", "") == "true",
		AdminUsername:           getEnv("STRATUM_ADMIN_USERNAME", ""),
		AdminPassword:           getEnv("STRATUM_ADMIN_PASSWORD", ""),
		AvatarDir:               getEnv("AVATAR_DIR", "/data/avatars"),
		Port:                    getEnv("PORT", "8080"),
		NatsURL:                 natsURL,
		MilvusHost:              getEnv("MILVUS_HOST", "localhost"),
		MilvusPort:              getEnv("MILVUS_PORT", "19530"),
		OtelEndpoint:            getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		PostgresURL:             getEnv("POSTGRES_URL", "postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"),
		RedisURL:                getEnv("REDIS_URL", "redis://localhost:6379"),
		GitHubClientID:          getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:      getEnv("GITHUB_CLIENT_SECRET", ""),
		GitHubAuthorizeURL:      authorizeURL,
		GitHubTokenURL:          tokenURL,
		GitHubUserURL:           userURL,
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
		InternalAPI: InternalAPIConfig{
			Port:                   getEnv("INTERNAL_API_PORT", "8443"),
			CertFile:               getEnv("INTERNAL_API_TLS_CERT_FILE", ""),
			KeyFile:                getEnv("INTERNAL_API_TLS_KEY_FILE", ""),
			ClientCAFile:           getEnv("INTERNAL_API_CLIENT_CA_FILE", ""),
			PlatformMCPDialAddress: getEnv("PLATFORM_MCP_DIAL_ADDRESS", ""),
		},
	}
	if err := cfg.InternalAPI.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func githubOAuthEndpoints() (string, string, string, error) {
	const (
		defaultAuthorizeURL = "https://github.com/login/oauth/authorize"
		defaultTokenURL     = "https://github.com/login/oauth/access_token"
		defaultUserURL      = "https://api.github.com/user"
	)
	overrides := []string{
		os.Getenv("GITHUB_AUTHORIZE_URL"),
		os.Getenv("GITHUB_TOKEN_URL"),
		os.Getenv("GITHUB_USER_URL"),
	}
	configured := 0
	for _, endpoint := range overrides {
		if endpoint != "" {
			configured++
		}
	}
	if configured == 0 {
		return defaultAuthorizeURL, defaultTokenURL, defaultUserURL, nil
	}
	if configured != len(overrides) {
		return "", "", "", fmt.Errorf("GitHub OAuth endpoint overrides must be configured together")
	}
	if os.Getenv("STRATUM_E2E_MODE") != "true" {
		return "", "", "", fmt.Errorf("GitHub OAuth endpoint overrides require STRATUM_E2E_MODE=true")
	}
	for _, endpoint := range overrides {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.User != nil ||
			(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
			return "", "", "", fmt.Errorf("GitHub OAuth endpoint overrides must use loopback HTTP URLs")
		}
	}
	return overrides[0], overrides[1], overrides[2], nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
