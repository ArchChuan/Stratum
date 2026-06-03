//go:build integration

// Package postgres provides PostgreSQL database utilities.
package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/postgres"
	"go.uber.org/zap"
)

func TestNew_Connect(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		url = "postgres://clawhermes:clawhermes@localhost:5432/clawhermes"
	}

	logger := zap.NewNop()
	pool, err := postgres.New(context.Background(), url, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Close()

	if err := pool.DB().Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
