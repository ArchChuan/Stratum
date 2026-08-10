package config

import (
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

type Config struct {
	Port                string
	NatsURL             string
	MilvusHost          string
	MilvusPort          string
	OtelEndpoint        string
	PostgresURL         string
	RedisURL            string
	PasswordAuthEnabled bool
	// GuestAuthEnabled controls the unauthenticated trial endpoint POST /auth/guest.
	// Guests are provisioned into a dedicated per-guest sandbox tenant (never the
	// default tenant) and are reaped after GuestAccountTTL; set to false to
	// disable the trial entry entirely.
	GuestAuthEnabled   bool
	AdminUsername      string
	AdminPassword      string
	AvatarDir          string
	GitHubClientID     string
	GitHubClientSecret string
	GitHubAuthorizeURL string
	GitHubTokenURL     string
	GitHubUserURL      string
	JWTPrivateKeyPEM   string
	// DataEncryptionKey 是 at-rest 加密的密钥材料（provider API key / MCP
	// secret 等），独立于 JWT 签名密钥：轮换 JWT 私钥不影响密文可解，
	// 未配置时回退 JWT 私钥派生以兼容存量密文（见 pkg/crypto.ResolveDataKey）。
	DataEncryptionKey       string
	GlobalAdminGitHubLogin  string
	FrontendURL             string
	GitHubCallbackURL       string
	SecureCookies           bool
	GlobalAgentSystemPrompt string
	QwenBaseURL             string
	ZhipuBaseURL            string
	RerankBaseURL           string
	RerankAPIKey            string
	RerankModel             string
	Opik                    OpikConfig
	TracePayload            TracePayloadConfig
	MemoryPipeline          MemoryPipelineConfig
	// 热更新运行时状态（Nacos listener 写、wiring 注册回调读）
	memoryDynamic          atomic.Pointer[MemoryPipelineDynamic]
	memoryDynamicListeners []func(MemoryPipelineDynamic)
	dynamicMu              sync.RWMutex
}

// RerankConfigured reports whether an external reranker backend is available.
// BaseURL is the single switch: an empty base URL disables the backend.
// 指针接收者：Config 含 atomic.Pointer/sync.RWMutex 后不可按值复制（vet copylocks）。
func (c *Config) RerankConfigured() bool {
	return c.RerankBaseURL != ""
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
		PasswordAuthEnabled: getEnv("PASSWORD_AUTH_ENABLED", "") == "true",
		// 默认开启受限沙箱模式：前端登录页有访客试用入口，直接禁用会破坏试用流程；
		// 沙箱隔离使 guest 只能访问自己的空租户，部署方可用 GUEST_AUTH_ENABLED=false 完全关闭。
		GuestAuthEnabled:        getEnv("GUEST_AUTH_ENABLED", "true") == "true",
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
		DataEncryptionKey:       getEnv("DATA_ENCRYPTION_KEY", ""),
		GlobalAdminGitHubLogin:  getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", "ArchChuan"),
		FrontendURL:             getEnv("FRONTEND_URL", "http://localhost:3002"),
		GitHubCallbackURL:       getEnv("GITHUB_CALLBACK_URL", "http://localhost:8080/auth/github/callback"),
		SecureCookies:           getEnv("SECURE_COOKIES", "") == "true",
		GlobalAgentSystemPrompt: getEnv("GLOBAL_AGENT_SYSTEM_PROMPT", ""),
		QwenBaseURL:             getEnv("QWEN_BASE_URL", ""),
		ZhipuBaseURL:            getEnv("ZHIPU_BASE_URL", ""),
		RerankBaseURL:           getEnv("RERANK_BASE_URL", ""),
		RerankAPIKey:            getEnv("RERANK_API_KEY", ""),
		RerankModel:             getEnv("RERANK_MODEL", "rerank-v3.0"),
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

// MemoryPipelineDynamic 是运行期可热更新的 memory pipeline 调度参数
// （Nacos stratum/memory dataId 的 poll_interval/batch_size），
// 与冷生效的装配参数（Enabled、worker 数等）分离。
type MemoryPipelineDynamic struct {
	PollInterval time.Duration
	BatchSize    int
}

// OnMemoryPipelineDynamic 注册热更新回调；回调在 listener goroutine 同步调用，
// 必须非阻塞、不得持有锁（内部由 wiring 做 atomic Store）。
func (c *Config) OnMemoryPipelineDynamic(fn func(MemoryPipelineDynamic)) {
	c.dynamicMu.Lock()
	c.memoryDynamicListeners = append(c.memoryDynamicListeners, fn)
	c.dynamicMu.Unlock()
}

// ApplyMemoryPipelineDynamic 原子写入动态配置并通知所有 listener。
func (c *Config) ApplyMemoryPipelineDynamic(d MemoryPipelineDynamic) {
	c.memoryDynamic.Store(&d)
	c.dynamicMu.RLock()
	listeners := append([]func(MemoryPipelineDynamic){}, c.memoryDynamicListeners...)
	c.dynamicMu.RUnlock()
	for _, fn := range listeners {
		fn(d)
	}
}

// LoadMemoryPipelineDynamic 返回当前动态值；未设置过时为零值。
func (c *Config) LoadMemoryPipelineDynamic() MemoryPipelineDynamic {
	if d := c.memoryDynamic.Load(); d != nil {
		return *d
	}
	return MemoryPipelineDynamic{}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
