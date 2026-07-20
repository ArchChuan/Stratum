package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

func TestRateLimiterStoreStopTerminatesCleanup(t *testing.T) {
	store := NewRateLimiterStore(rate.Limit(1), 1)
	done := store.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rate limiter cleanup goroutine did not stop")
	}

	select {
	case <-store.Stop():
	case <-time.After(time.Second):
		t.Fatal("second Stop call should return an already-closed channel")
	}
}

func TestRedisRateLimiterStoresShareQuotaAndReturnRetryAfter(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewRedisRateLimiterStore(client, rate.Limit(1), 1)
	second := NewRedisRateLimiterStore(client, rate.Limit(1), 1)

	request := func(store *RateLimiterStore) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/limited", RateLimitByKey(store, func(*gin.Context) string { return "tenant:user" }), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limited", nil)) //nolint:noctx
		return w
	}

	if got := request(first); got.Code != http.StatusNoContent {
		t.Fatalf("first instance status=%d body=%s", got.Code, got.Body.String())
	}
	got := request(second)
	if got.Code != http.StatusTooManyRequests {
		t.Fatalf("second instance bypassed shared quota: status=%d body=%s", got.Code, got.Body.String())
	}
	if got.Header().Get("Retry-After") == "" {
		t.Fatal("rate-limited response omitted Retry-After")
	}
}

func TestRedisRateLimiterFailsClosedWhenRedisErrors(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	store := NewRedisRateLimiterStore(client, rate.Limit(1), 1)
	mini.Close()
	t.Cleanup(func() { _ = client.Close() })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/limited", RateLimit(store), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limited", nil)) //nolint:noctx

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("Redis failure status=%d body=%s", w.Code, w.Body.String())
	}
}
