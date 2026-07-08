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

func TestTenantGatewayCache_CopiesAPIKeys(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	gw := llmgateway.NewGateway()
	keys := map[string]string{"qwen": "secret"}

	cache.Set("tenant-1", gw, keys, 5*time.Minute)
	keys["qwen"] = "mutated-before-get"

	_, got, ok := cache.Get("tenant-1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got["qwen"] != "secret" {
		t.Fatalf("cache retained caller-owned map, got %q", got["qwen"])
	}

	got["qwen"] = "mutated-after-get"
	_, gotAgain, ok := cache.Get("tenant-1")
	if !ok {
		t.Fatal("expected second cache hit")
	}
	if gotAgain["qwen"] != "secret" {
		t.Fatalf("Get returned cache-owned map, got %q", gotAgain["qwen"])
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
