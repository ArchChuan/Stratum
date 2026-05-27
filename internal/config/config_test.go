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
}

func TestLoadWithEnv(t *testing.T) {
	os.Setenv("PORT", "9000")
	os.Setenv("NATS_URL", "nats://custom:4222")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("NATS_URL")
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
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_VAR", "test_value")
	defer os.Unsetenv("TEST_VAR")

	result := getEnv("TEST_VAR", "default")
	if result != "test_value" {
		t.Errorf("expected test_value, got %s", result)
	}

	result = getEnv("NONEXISTENT_VAR", "default")
	if result != "default" {
		t.Errorf("expected default, got %s", result)
	}
}
