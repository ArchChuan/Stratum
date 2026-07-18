package infrastructure_test

import (
	"sync"
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

func TestTenantGatewayCache_InvalidateRejectsStaleGenerationSet(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	staleGateway := llmgateway.NewGateway()
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	setResult := make(chan bool, 1)
	go func() {
		_, _, hit, generation := cache.GetWithGeneration("tenant-1")
		if hit {
			setResult <- true
			return
		}
		close(loadStarted)
		<-releaseLoad
		setResult <- cache.SetIfGeneration("tenant-1", staleGateway, map[string]string{"qwen": "fake-key-a"}, time.Minute, generation)
	}()
	<-loadStarted
	cache.Invalidate("tenant-1")
	close(releaseLoad)
	if <-setResult {
		t.Fatal("stale loader restored an invalidated gateway")
	}
	if _, _, ok := cache.Get("tenant-1"); ok {
		t.Fatal("stale gateway must not be cached")
	}
}

func TestTenantGatewayCache_ConcurrentGenerationAndKeysClone(t *testing.T) {
	cache := llmgateway.NewTenantGatewayCache()
	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			for range 100 {
				_, keys, hit, generation := cache.GetWithGeneration("tenant-1")
				if hit {
					keys["qwen"] = "caller-mutated-fake-key"
				}
				cache.SetIfGeneration("tenant-1", llmgateway.NewGateway(), map[string]string{"qwen": "fake-key-a"}, time.Minute, generation)
				if i%8 == 0 {
					cache.Invalidate("tenant-1")
				}
			}
		}(i)
	}
	wg.Wait()

	_, keys, ok := cache.Get("tenant-1")
	if ok && keys["qwen"] != "fake-key-a" {
		t.Fatalf("cache key clone was mutated by caller: %q", keys["qwen"])
	}
}
