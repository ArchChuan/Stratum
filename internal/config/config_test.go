package config

import (
	"os"
	"testing"

	"go.uber.org/zap"
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

	if cfg.Neo4jURI != "bolt://localhost:7687" {
		t.Errorf("expected Neo4jURI bolt://localhost:7687, got %s", cfg.Neo4jURI)
	}
}

func TestLoadWithEnv(t *testing.T) {
	_ = os.Setenv("PORT", "9000")
	_ = os.Setenv("NATS_URL", "nats://custom:4222")
	_ = os.Setenv("MILVUS_HOST", "custom-milvus")
	_ = os.Setenv("MILVUS_PORT", "19531")
	_ = os.Setenv("NEO4J_URI", "bolt://custom:7687")
	_ = os.Setenv("NEO4J_USER", "custom-user")
	_ = os.Setenv("NEO4J_PASSWORD", "custom-pass")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://custom:4317")
	_ = os.Setenv("OPENAI_API_KEY", "sk-test-key")

	defer func() {
		_ = os.Unsetenv("PORT")
		_ = os.Unsetenv("NATS_URL")
		_ = os.Unsetenv("MILVUS_HOST")
		_ = os.Unsetenv("MILVUS_PORT")
		_ = os.Unsetenv("NEO4J_URI")
		_ = os.Unsetenv("NEO4J_USER")
		_ = os.Unsetenv("NEO4J_PASSWORD")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		_ = os.Unsetenv("OPENAI_API_KEY")
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

	if cfg.MilvusHost != "custom-milvus" {
		t.Errorf("expected MilvusHost custom-milvus, got %s", cfg.MilvusHost)
	}

	if cfg.MilvusPort != "19531" {
		t.Errorf("expected MilvusPort 19531, got %s", cfg.MilvusPort)
	}

	if cfg.Neo4jURI != "bolt://custom:7687" {
		t.Errorf("expected Neo4jURI bolt://custom:7687, got %s", cfg.Neo4jURI)
	}

	if cfg.Neo4jUser != "custom-user" {
		t.Errorf("expected Neo4jUser custom-user, got %s", cfg.Neo4jUser)
	}

	if cfg.Neo4jPassword != "custom-pass" {
		t.Errorf("expected Neo4jPassword custom-pass, got %s", cfg.Neo4jPassword)
	}

	if cfg.OpenAIAPIKey != "sk-test-key" {
		t.Errorf("expected OpenAIAPIKey sk-test-key, got %s", cfg.OpenAIAPIKey)
	}
}

func TestGetEnv(t *testing.T) {
	_ = os.Setenv("TEST_VAR", "test_value")
	defer func() { _ = os.Unsetenv("TEST_VAR") }()

	result := getEnv("TEST_VAR", "default")
	if result != "test_value" {
		t.Errorf("expected test_value, got %s", result)
	}

	result = getEnv("NONEXISTENT_VAR", "default")
	if result != "default" {
		t.Errorf("expected default, got %s", result)
	}
}

func TestGetEnvEmpty(t *testing.T) {
	_ = os.Setenv("EMPTY_VAR", "")
	defer func() { _ = os.Unsetenv("EMPTY_VAR") }()

	result := getEnv("EMPTY_VAR", "default")
	if result != "default" {
		t.Errorf("expected default for empty env var, got %s", result)
	}
}

func TestConfigStruct(t *testing.T) {
	cfg := &Config{
		Port:          "8080",
		NatsURL:       "nats://localhost:4222",
		MilvusHost:    "localhost",
		MilvusPort:    "19530",
		Neo4jURI:      "bolt://localhost:7687",
		Neo4jUser:     "neo4j",
		Neo4jPassword: "password",
		OtelEndpoint:  "http://localhost:4317",
		OpenAIAPIKey:  "sk-test",
	}

	if cfg.Port != "8080" {
		t.Errorf("expected port 8080, got %s", cfg.Port)
	}

	if cfg.MilvusHost != "localhost" {
		t.Errorf("expected localhost, got %s", cfg.MilvusHost)
	}

	if cfg.Neo4jUser != "neo4j" {
		t.Errorf("expected neo4j user, got %s", cfg.Neo4jUser)
	}
}

func TestServicesStruct(t *testing.T) {
	services := &Services{
		GraphRAG:    nil,
		VectorStore: nil,
	}

	if services.GraphRAG != nil {
		t.Error("expected nil GraphRAG")
	}

	if services.VectorStore != nil {
		t.Error("expected nil VectorStore")
	}
}

func TestLoadAuthFields(t *testing.T) {
	_ = os.Setenv("GITHUB_CLIENT_ID", "gh-id")
	_ = os.Setenv("GITHUB_CLIENT_SECRET", "gh-secret")
	_ = os.Setenv("JWT_PRIVATE_KEY_PEM", "test-pem")
	_ = os.Setenv("GLOBAL_ADMIN_GITHUB_LOGIN", "byteBuilderX")
	defer func() {
		_ = os.Unsetenv("GITHUB_CLIENT_ID")
		_ = os.Unsetenv("GITHUB_CLIENT_SECRET")
		_ = os.Unsetenv("JWT_PRIVATE_KEY_PEM")
		_ = os.Unsetenv("GLOBAL_ADMIN_GITHUB_LOGIN")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.GitHubClientID != "gh-id" {
		t.Errorf("GitHubClientID: got %s", cfg.GitHubClientID)
	}
	if cfg.GitHubClientSecret != "gh-secret" {
		t.Errorf("GitHubClientSecret: got %s", cfg.GitHubClientSecret)
	}
	if cfg.JWTPrivateKeyPEM != "test-pem" {
		t.Errorf("JWTPrivateKeyPEM: got %s", cfg.JWTPrivateKeyPEM)
	}
	if cfg.GlobalAdminGitHubLogin != "byteBuilderX" {
		t.Errorf("GlobalAdminGitHubLogin: got %s", cfg.GlobalAdminGitHubLogin)
	}
}

func TestInitializeServices(t *testing.T) {
	logger := zap.NewNop()
	cfg := &Config{
		Neo4jURI:      "bolt://localhost:7687",
		Neo4jUser:     "neo4j",
		Neo4jPassword: "password",
		MilvusHost:    "localhost",
		MilvusPort:    "19530",
	}

	services, err := InitializeServices(cfg, logger)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if services == nil { //nolint:staticcheck
		t.Error("expected non-nil GraphRAG")
	}

	if services.VectorStore == nil { //nolint:staticcheck
		t.Error("expected non-nil VectorStore")
	}
}
