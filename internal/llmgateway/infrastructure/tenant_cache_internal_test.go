package infrastructure

import (
	"fmt"
	"testing"
	"time"
)

func TestTenantGatewayCacheInvalidateReclaimsGeneration(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, token := cache.GetWithGeneration("tenant-1")
	if token == 0 {
		t.Fatal("load token must be allocated on cache miss")
	}
	cache.Invalidate("tenant-1")

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.generations["tenant-1"]; ok {
		t.Fatal("invalidate retained tenant generation")
	}
}

func TestTenantGatewayCacheRecreatedTenantRejectsOldLoadToken(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, oldToken := cache.GetWithGeneration("tenant-1")
	cache.Invalidate("tenant-1")
	_, _, _, newToken := cache.GetWithGeneration("tenant-1")

	if newToken == oldToken {
		t.Fatalf("recreated tenant reused load token %d", oldToken)
	}
	if cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, oldToken) {
		t.Fatal("old loader published after tenant recreation")
	}
	if !cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, newToken) {
		t.Fatal("current tenant loader was rejected")
	}
}

func TestTenantGatewayCacheExpiryRejectsOldLoadToken(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, oldToken := cache.GetWithGeneration("tenant-1")
	if !cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Millisecond, oldToken) {
		t.Fatal("initial cache fill was rejected")
	}
	time.Sleep(5 * time.Millisecond)

	if cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, oldToken) {
		t.Fatal("loader with expired token published")
	}
	_, _, hit, newToken := cache.GetWithGeneration("tenant-1")
	if hit {
		t.Fatal("expired cache entry remained visible")
	}
	if newToken == oldToken {
		t.Fatalf("expired tenant reused load token %d", oldToken)
	}
}

func TestTenantGatewayCacheChurnDoesNotRetainHistoricalTenantGenerations(t *testing.T) {
	cache := NewTenantGatewayCache()
	for i := range 1_000 {
		tenantID := fmt.Sprintf("tenant-%d", i)
		cache.GetWithGeneration(tenantID)
		cache.Invalidate(tenantID)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if got := len(cache.generations); got != 0 {
		t.Fatalf("generation map retained %d historical tenants", got)
	}
}

func TestTenantGatewayCacheSetEstablishesCurrentLoadToken(t *testing.T) {
	cache := NewTenantGatewayCache()
	cache.Set("tenant-1", NewGateway(), nil, time.Minute)
	_, _, hit, token := cache.GetWithGeneration("tenant-1")
	if !hit {
		t.Fatal("compatibility Set did not create cache entry")
	}
	if token == 0 {
		t.Fatal("compatibility Set did not establish a current token")
	}
	if !cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, token) {
		t.Fatal("current token returned after Set was rejected")
	}
}

func TestTenantGatewayCacheReleaseLoadReclaimsAbandonedMisses(t *testing.T) {
	cache := NewTenantGatewayCache()
	for i := range 1_000 {
		tenantID := fmt.Sprintf("tenant-%d", i)
		_, _, _, token := cache.GetWithGeneration(tenantID)
		cache.ReleaseLoad(tenantID, token)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if got := len(cache.generations); got != 0 {
		t.Fatalf("generation map retained %d abandoned loads", got)
	}
}

func TestTenantGatewayCacheReleaseOneSharedLoadKeepsOtherLoadCurrent(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, firstToken := cache.GetWithGeneration("tenant-1")
	_, _, _, secondToken := cache.GetWithGeneration("tenant-1")
	if firstToken != secondToken {
		t.Fatalf("concurrent misses received different tokens: %d and %d", firstToken, secondToken)
	}

	cache.ReleaseLoad("tenant-1", firstToken)
	if !cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, secondToken) {
		t.Fatal("one failed loader release rejected another loader using the shared token")
	}
}

func TestTenantGatewayCacheReleaseAllSharedLoadsReclaimsGeneration(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, token := cache.GetWithGeneration("tenant-1")
	_, _, _, secondToken := cache.GetWithGeneration("tenant-1")
	if token != secondToken {
		t.Fatalf("concurrent misses received different tokens: %d and %d", token, secondToken)
	}

	cache.ReleaseLoad("tenant-1", token)
	cache.ReleaseLoad("tenant-1", secondToken)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.generations["tenant-1"]; ok {
		t.Fatal("generation remained after all shared loads were released")
	}
}

func TestTenantGatewayCacheReleaseOldLoadDoesNotRemoveRecreatedTenant(t *testing.T) {
	cache := NewTenantGatewayCache()
	_, _, _, oldToken := cache.GetWithGeneration("tenant-1")
	cache.Invalidate("tenant-1")
	_, _, _, newToken := cache.GetWithGeneration("tenant-1")
	cache.ReleaseLoad("tenant-1", oldToken)

	if !cache.SetIfGeneration("tenant-1", NewGateway(), nil, time.Minute, newToken) {
		t.Fatal("old loader release removed recreated tenant token")
	}
}

func TestTenantGatewayCacheCompatibilityGetMissDoesNotCreateLoadToken(t *testing.T) {
	cache := NewTenantGatewayCache()
	cache.Get("tenant-1")

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.generations["tenant-1"]; ok {
		t.Fatal("compatibility Get miss retained a load token")
	}
}
