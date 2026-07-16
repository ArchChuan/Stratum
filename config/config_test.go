package config

import (
	"os"
	"testing"
)

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
