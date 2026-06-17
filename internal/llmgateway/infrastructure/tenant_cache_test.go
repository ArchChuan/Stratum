package infrastructure_test

import (
	"testing"
	"time"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

func TestTenantGatewayCache_SetAndGet(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	gw := llmgateway.NewGateway()

	cache.Set("tenant-1", gw, nil, 5*time.Minute)

	got, _, ok := cache.Get("tenant-1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != gw {
		t.Fatal("expected same gateway pointer")
	}
}

func TestTenantGatewayCache_Miss(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	_, _, ok := cache.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestTenantGatewayCache_Expiry(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	gw := llmgateway.NewGateway()

	cache.Set("tenant-1", gw, nil, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	_, _, ok := cache.Get("tenant-1")
	if ok {
		t.Fatal("expected cache miss after expiry")
	}
}

func TestTenantGatewayCache_Invalidate(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	gw := llmgateway.NewGateway()

	cache.Set("tenant-1", gw, nil, 5*time.Minute)
	cache.Invalidate("tenant-1")

	_, _, ok := cache.Get("tenant-1")
	if ok {
		t.Fatal("expected cache miss after invalidate")
	}
}
