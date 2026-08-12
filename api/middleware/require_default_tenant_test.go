package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
)

func TestRequireDefaultTenant_defaultTenantAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set(middleware.ContextKeyTenantID, constants.DefaultTenantID) },
		middleware.RequireDefaultTenant(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireDefaultTenant_otherTenantDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set(middleware.ContextKeyTenantID, "tenant_acme") },
		middleware.RequireDefaultTenant(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireDefaultTenant_missingTenantDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", middleware.RequireDefaultTenant(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (fail closed), got %d", w.Code)
	}
}

// TestRequireDefaultTenant_resolvedDefaultTenantIDAllowed 回归真实运行时缺陷：
// tenants.id 是 UUID（uuid_generate_v4），EnsureDefaultTenant 从不生成字面
// "tenant_default"。门禁必须放行 bootstrap 后解析出的真实默认租户 id，否则
// 机制管理面在真实 JWT（tid 恒为 UUID）下永远 403。
func TestRequireDefaultTenant_resolvedDefaultTenantIDAllowed(t *testing.T) {
	const resolved = "11111111-2222-3333-4444-555555555555"
	constants.SetResolvedDefaultTenantID(resolved)
	defer constants.SetResolvedDefaultTenantID(constants.DefaultTenantID)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set(middleware.ContextKeyTenantID, resolved) },
		middleware.RequireDefaultTenant(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for resolved default tenant id, got %d", w.Code)
	}
}
