package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

type e2eRoutesPayload struct {
	Routes []struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"routes"`
}

func TestE2ERoutesEndpointListsRegisteredRoutes(t *testing.T) {
	// 路由 dump 由 STRATUM_E2E_MODE=true 门控;测试代表 e2e 模式下的行为。
	t.Setenv("STRATUM_E2E_MODE", "true")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	registerE2ERoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e2e/routes", nil)) //nolint:noctx
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var payload e2eRoutesPayload
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /e2e/routes: %v", err)
	}
	// 端点自身 + 手动注册的 /health,至少 2 条。
	if len(payload.Routes) < 2 {
		t.Fatalf("routes=%d want >=2 (health + self)", len(payload.Routes))
	}
	found := false
	for _, route := range payload.Routes {
		if route.Method == http.MethodGet && route.Path == "/health" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("GET /health missing from /e2e/routes dump")
	}
}

func TestE2ERoutesAbsentWhenModeEnvUnset(t *testing.T) {
	// 未设置 STRATUM_E2E_MODE 时,/e2e/routes 不得注册,生产路由不暴露路由清单。
	t.Setenv("STRATUM_E2E_MODE", "")
	gin.SetMode(gin.TestMode)

	metrics := observability.NewPrometheusMetrics(zap.NewNop())
	router := NewRouter(&wiring.Container{
		Config: &config.Config{FrontendURL: "http://localhost:3002"},
		Logger: zap.NewNop(),
		Platform: &wiring.Platform{
			Metrics: metrics,
		},
		LLMGateway: &wiring.LLMGateway{},
		Skill:      &wiring.Skill{},
		Agent:      &wiring.Agent{},
		Knowledge:  &wiring.Knowledge{},
		MCP:        &wiring.MCP{},
	})

	routes := router.Routes()
	if len(routes) == 0 {
		t.Fatal("router built with zero routes; assertion would be vacuous")
	}
	for _, route := range routes {
		if route.Method == http.MethodGet && route.Path == "/e2e/routes" {
			t.Fatal("GET /e2e/routes registered without STRATUM_E2E_MODE=true")
		}
	}
}
