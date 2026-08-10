package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadGitHubOAuthEndpointDefaults(t *testing.T) {
	t.Setenv("GITHUB_AUTHORIZE_URL", "")
	t.Setenv("GITHUB_TOKEN_URL", "")
	t.Setenv("GITHUB_USER_URL", "")
	t.Setenv("STRATUM_E2E_MODE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.GitHubAuthorizeURL != "https://github.com/login/oauth/authorize" {
		t.Fatalf("unexpected authorize URL: %q", cfg.GitHubAuthorizeURL)
	}
	if cfg.GitHubTokenURL != "https://github.com/login/oauth/access_token" {
		t.Fatalf("unexpected token URL: %q", cfg.GitHubTokenURL)
	}
	if cfg.GitHubUserURL != "https://api.github.com/user" {
		t.Fatalf("unexpected user URL: %q", cfg.GitHubUserURL)
	}
}

func TestLoadGitHubOAuthEndpointsRequireE2EMode(t *testing.T) {
	t.Setenv("GITHUB_AUTHORIZE_URL", "http://127.0.0.1:19090/login/oauth/authorize")
	t.Setenv("GITHUB_TOKEN_URL", "http://127.0.0.1:19090/login/oauth/access_token")
	t.Setenv("GITHUB_USER_URL", "http://127.0.0.1:19090/user")
	t.Setenv("STRATUM_E2E_MODE", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STRATUM_E2E_MODE") {
		t.Fatalf("expected explicit E2E mode error, got %v", err)
	}
}

func TestLoadGitHubOAuthEndpointsRejectNonLoopback(t *testing.T) {
	t.Setenv("GITHUB_AUTHORIZE_URL", "http://oauth.example.test/login/oauth/authorize")
	t.Setenv("GITHUB_TOKEN_URL", "http://oauth.example.test/login/oauth/access_token")
	t.Setenv("GITHUB_USER_URL", "http://oauth.example.test/user")
	t.Setenv("STRATUM_E2E_MODE", "true")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback validation error, got %v", err)
	}
}

func TestLoadGitHubOAuthEndpointsAcceptCompleteLoopbackSet(t *testing.T) {
	t.Setenv("GITHUB_AUTHORIZE_URL", "http://localhost:19090/login/oauth/authorize")
	t.Setenv("GITHUB_TOKEN_URL", "http://localhost:19090/login/oauth/access_token")
	t.Setenv("GITHUB_USER_URL", "http://localhost:19090/user")
	t.Setenv("STRATUM_E2E_MODE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.GitHubAuthorizeURL != "http://localhost:19090/login/oauth/authorize" ||
		cfg.GitHubTokenURL != "http://localhost:19090/login/oauth/access_token" ||
		cfg.GitHubUserURL != "http://localhost:19090/user" {
		t.Fatalf("unexpected OAuth endpoints: %#v", cfg)
	}
}

func TestLoad(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("expected Port 8080, got %s", cfg.Port)
	}
	if cfg.NatsURL != "nats://localhost:4222" {
		t.Errorf("expected NatsURL nats://localhost:4222, got %s", cfg.NatsURL)
	}
	if cfg.MilvusHost != "localhost" {
		t.Errorf("expected MilvusHost localhost, got %s", cfg.MilvusHost)
	}
	if cfg.MilvusPort != "19530" {
		t.Errorf("expected MilvusPort 19530, got %s", cfg.MilvusPort)
	}
}

func TestLoadWithEnv(t *testing.T) {
	_ = os.Setenv("PORT", "9000")
	_ = os.Setenv("NATS_URL", "nats://custom:4222")
	_ = os.Setenv("MILVUS_HOST", "custom-milvus")
	_ = os.Setenv("MILVUS_PORT", "19531")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://custom:4317")
	_ = os.Setenv("QWEN_BASE_URL", "http://qwen-compatible.test/v1")
	_ = os.Setenv("ZHIPU_BASE_URL", "http://zhipu-compatible.test/v1")
	defer func() {
		_ = os.Unsetenv("PORT")
		_ = os.Unsetenv("NATS_URL")
		_ = os.Unsetenv("MILVUS_HOST")
		_ = os.Unsetenv("MILVUS_PORT")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		_ = os.Unsetenv("QWEN_BASE_URL")
		_ = os.Unsetenv("ZHIPU_BASE_URL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != "9000" {
		t.Errorf("expected Port 9000, got %s", cfg.Port)
	}
	if cfg.NatsURL != "nats://custom:4222" {
		t.Errorf("expected NatsURL nats://custom:4222, got %s", cfg.NatsURL)
	}
	if cfg.QwenBaseURL != "http://qwen-compatible.test/v1" {
		t.Errorf("expected custom Qwen base URL, got %s", cfg.QwenBaseURL)
	}
	if cfg.ZhipuBaseURL != "http://zhipu-compatible.test/v1" {
		t.Errorf("expected custom Zhipu base URL, got %s", cfg.ZhipuBaseURL)
	}
}

func TestGetEnv(t *testing.T) {
	_ = os.Setenv("TEST_VAR", "test_value")
	defer func() { _ = os.Unsetenv("TEST_VAR") }()

	if got := getEnv("TEST_VAR", "default"); got != "test_value" {
		t.Errorf("expected test_value, got %s", got)
	}
	if got := getEnv("NONEXISTENT_VAR", "default"); got != "default" {
		t.Errorf("expected default, got %s", got)
	}
}

func TestMemoryPipelineDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.MemoryPipeline.Enabled {
		t.Error("expected MemoryPipeline.Enabled=false by default")
	}
	if cfg.MemoryPipeline.NatsURL != "nats://localhost:4222" {
		t.Errorf("expected pipeline NatsURL nats://localhost:4222, got %s", cfg.MemoryPipeline.NatsURL)
	}
}

func TestLoadOpikAndTracePayloadConfig(t *testing.T) {
	t.Setenv("OPIK_URL", "http://opik.test/api")
	t.Setenv("OPIK_PROJECT", "stratum-test")
	t.Setenv("OPIK_WORKSPACE", "workspace-test")
	t.Setenv("OPIK_API_KEY", "not-a-real-key")
	t.Setenv("TRACE_PAYLOAD_ENABLED", "true")
	t.Setenv("TRACE_PAYLOAD_ENDPOINT", "minio.test:9000")
	t.Setenv("TRACE_PAYLOAD_ACCESS_KEY", "access-test")
	t.Setenv("TRACE_PAYLOAD_SECRET_KEY", "secret-test")
	t.Setenv("TRACE_PAYLOAD_BUCKET", "stratum-evidence-test")
	t.Setenv("TRACE_PAYLOAD_USE_TLS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Opik.URL != "http://opik.test/api" || cfg.Opik.Project != "stratum-test" || cfg.Opik.Workspace != "workspace-test" {
		t.Fatalf("unexpected Opik config: %#v", cfg.Opik)
	}
	if cfg.Opik.APIKey != "not-a-real-key" {
		t.Fatal("Opik API key not loaded")
	}
	if !cfg.TracePayload.Enabled || !cfg.TracePayload.UseTLS || cfg.TracePayload.Bucket != "stratum-evidence-test" {
		t.Fatalf("unexpected trace payload config: %#v", cfg.TracePayload)
	}
}

func TestMemoryPipelineDynamicApplyAndLoad(t *testing.T) {
	cfg := &Config{}
	d := MemoryPipelineDynamic{PollInterval: 5 * time.Second, BatchSize: 100}
	cfg.ApplyMemoryPipelineDynamic(d)
	got := cfg.LoadMemoryPipelineDynamic()
	if got != d {
		t.Fatalf("LoadMemoryPipelineDynamic() = %+v, want %+v", got, d)
	}
}

func TestMemoryPipelineDynamicListenerFires(t *testing.T) {
	cfg := &Config{}
	var got MemoryPipelineDynamic
	cfg.OnMemoryPipelineDynamic(func(d MemoryPipelineDynamic) { got = d })
	cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: 7 * time.Second})
	if got.PollInterval != 7*time.Second {
		t.Fatalf("listener got %+v, want PollInterval=7s", got)
	}
}

func TestMemoryPipelineDynamicZeroWhenUnset(t *testing.T) {
	cfg := &Config{}
	if got := cfg.LoadMemoryPipelineDynamic(); got != (MemoryPipelineDynamic{}) {
		t.Fatalf("expected zero value, got %+v", got)
	}
}
