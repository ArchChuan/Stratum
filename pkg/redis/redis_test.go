//go:build integration

// Package redis provides Redis caching utilities.
package redis_test

import (
	"context"
	"os"
	"testing"

	pkgredis "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/redis"
	"go.uber.org/zap"
)

func TestNew_Ping(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}

	logger := zap.NewNop()
	client, err := pkgredis.New(context.Background(), url, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	if err := client.Client().Ping(context.Background()).Err(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
