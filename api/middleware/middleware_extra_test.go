package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

func TestBodyLimitRejectsOversizedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ErrorHandler(zap.NewNop()))
	r.Use(BodyLimit(16))
	r.POST("/upload", func(c *gin.Context) {
		var req struct {
			Data string `json:"data"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 正常体积 → 200。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// 极端情况：超过上限 → 413。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"data":"`+strings.Repeat("a", 32)+`"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestInjectTenantContextBridgesAuthKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth.tenant_id", "tenant-9")
		c.Set("auth.sub", "user-9")
		c.Set("auth.role", string(tenantdb.RoleTenantAdmin))
		c.Next()
	})
	r.Use(InjectTenantContext())
	r.GET("/x", func(c *gin.Context) {
		tc, ok := tenantdb.FromContext(c.Request.Context())
		if !ok || tc.TenantID != "tenant-9" || tc.UserID != "user-9" || tc.Role != tenantdb.RoleTenantAdmin {
			c.Status(http.StatusInternalServerError)
			return
		}
		if reqctx.TenantIDFromContext(c.Request.Context()) != "tenant-9" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)

	// 极端情况：无 auth 键 → 请求继续，不注入。
	r2 := gin.New()
	r2.Use(InjectTenantContext())
	r2.GET("/y", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/y", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestCORSAndSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSMiddleware("https://example.com"))
	r.Use(SecurityHeaders())
	r.Use(TrustedProxies())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	// 常规请求：CORS + 安全头齐全。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	require.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "PATCH")
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	require.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=31536000")

	// 极端情况：OPTIONS 预检 → 204 且短路。
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/x", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestPrometheusAndNamespaceTenantMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := observability.NewPrometheusMetrics(zap.NewNop())
	r := gin.New()
	r.Use(NamespaceMiddleware("api"))
	r.Use(TenantMiddleware())
	r.Use(PrometheusMiddleware(metrics, zap.NewNop()))
	r.GET("/agents/:id", func(c *gin.Context) {
		if GetNamespace(c) != "api" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// 极端情况：无 X-Tenant-ID → default 且请求继续。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents/a1", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)

	// 带租户头 → 透传。
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agents/a1", nil) //nolint:noctx
	req.Header.Set("X-Tenant-ID", "tenant-7")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	// MetricsHandler 可直接 serve。
	mh := MetricsHandler(metrics)
	r2 := gin.New()
	r2.GET("/metrics", mh)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil)) //nolint:noctx
	require.Equal(t, http.StatusOK, w.Code)
}

func TestGetTenantNamespaceFallbacksAndParseIntHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	// 极端情况：未设置 → default。
	require.Equal(t, "default", GetTenantID(c))
	require.Equal(t, "default", GetNamespace(c))
	// 极端情况：非 string 值 → default。
	c.Set("tenant_id", 42)
	c.Set("namespace", nil)
	require.Equal(t, "default", GetTenantID(c))
	require.Equal(t, "default", GetNamespace(c))

	// ParseIntHeader：缺省 / 非法 / 合法。
	require.Equal(t, 7, ParseIntHeader(c, "X-Missing", 7))
	c.Request.Header.Set("X-Num", "abc")
	require.Equal(t, 7, ParseIntHeader(c, "X-Num", 7))
	c.Request.Header.Set("X-Num", "42")
	require.Equal(t, 42, ParseIntHeader(c, "X-Num", 7))
}

func TestRateLimiterStoreLocalAllowAndPrune(t *testing.T) {
	store := NewRateLimiterStore(rate.Limit(1), 2)
	// 本地模式（rdb=nil）：burst 内放行，耗尽后拒绝并给出 retry。
	allowed, _, err := store.allow(t.Context(), "key-1")
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, _, err = store.allow(t.Context(), "key-1")
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, retry, err := store.allow(t.Context(), "key-1")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Greater(t, retry, time.Duration(0))
	// 不同 key 不受影响。
	allowed, _, err = store.allow(t.Context(), "key-2")
	require.NoError(t, err)
	require.True(t, allowed)

	// pruneLocked：超过 30 分钟未见的条目被清理。
	store.mu.Lock()
	store.limiters["stale"] = &ipLimiter{limiter: rate.NewLimiter(1, 1), lastSeen: time.Now().Add(-40 * time.Minute)}
	store.lastPrune = time.Now().Add(-11 * time.Minute)
	store.mu.Unlock()
	store.get("fresh")
	store.mu.Lock()
	_, staleExists := store.limiters["stale"]
	store.mu.Unlock()
	require.False(t, staleExists)
	require.Equal(t, "rate_limit:", rateLimiterKeyPrefix)
}

func TestRedisInt64AndLocalRetryAfter(t *testing.T) {
	// 极端情况：各类型输入。
	if v, err := redisInt64(int64(3)); err != nil || v != 3 {
		t.Fatalf("int64 = %v, %v", v, err)
	}
	if v, err := redisInt64("5"); err != nil || v != 5 {
		t.Fatalf("string = %v, %v", v, err)
	}
	if _, err := redisInt64("abc"); err == nil {
		t.Fatal("bad string must fail")
	}
	if _, err := redisInt64(3.14); err == nil {
		t.Fatal("unexpected type must fail")
	}
	// localRetryAfter：rate 1/s → 1s。
	if got := localRetryAfter(rate.Limit(1)); got != time.Second {
		t.Fatalf("retry after = %v", got)
	}
	if got := localRetryAfter(rate.Limit(2)); got != 500*time.Millisecond {
		t.Fatalf("retry after = %v", got)
	}
}

func TestRateLimitByKeyRejectsEmptyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewRateLimiterStore(rate.Limit(5), 50)
	r := gin.New()
	r.Use(RateLimitByKey(store, func(*gin.Context) string { return "" }))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil)) //nolint:noctx
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestTraceMiddlewareGetTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(TraceMiddleware(zap.NewNop()))
	r.GET("/x", func(c *gin.Context) {
		if GetTraceID(c) == "" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil)) //nolint:noctx
	require.Equal(t, http.StatusNoContent, w.Code)
}
