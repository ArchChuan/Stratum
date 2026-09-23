package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
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

// stallingRedis 接受 TCP 连接但从不回包，模拟「Redis 黑洞」：建连成功、命令永远
// 等不到响应。只有请求侧的预算（而不是客户端默认的 dial/read 超时）能把它截断。
func stallingRedis(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return listener.Addr().String()
}

// TestRedisRateLimiterFailsClosedWithinBudgetWhenRedisStalls 钉住限流依赖的超时预算：
// Redis 不是「拒绝连接」而是「没有响应」时，请求必须在 RateLimitRedisTimeout 量级
// 内 fail closed，而不是挂在 go-redis 默认的 ReadTimeout×MaxRetries 上把连接和
// goroutine 一起拖住。
//
// 客户端刻意经 pkg/storage/redis.New 构造（生产路径），因为预算能否生效取决于
// Options.ContextTimeoutEnabled——裸 goredis.NewClient 默认忽略调用方 deadline。
func TestRedisRateLimiterFailsClosedWithinBudgetWhenRedisStalls(t *testing.T) {
	wrapped, err := pkgredis.New("redis://"+stallingRedis(t), zap.NewNop())
	if err != nil {
		t.Fatalf("build redis client: %v", err)
	}
	t.Cleanup(func() { _ = wrapped.Close() })
	store := NewRedisRateLimiterStore(wrapped.Client(), rate.Limit(1), 1)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/limited", RateLimit(store), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	start := time.Now()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limited", nil)) //nolint:noctx
	elapsed := time.Since(start)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("stalled Redis status=%d body=%s", w.Code, w.Body.String())
	}
	if budget := constants.RateLimitRedisTimeout; elapsed > budget+time.Second {
		t.Fatalf("stalled Redis held the request for %v, want fail closed within ~%v", elapsed, budget)
	}
}
