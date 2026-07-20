package http

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestProtectedRoutesRejectRequestsWhenJWTServiceMissing(t *testing.T) {
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

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "skills", method: http.MethodGet, path: "/skills"},
		{name: "agents", method: http.MethodGet, path: "/agents"},
		{name: "knowledge", method: http.MethodGet, path: "/knowledge/workspaces"},
		{name: "mcp", method: http.MethodGet, path: "/mcp/servers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 for %s %s, got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
			}
		})
	}
}

func TestBaseAuthRoutesRegisterWithoutGitHubOAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewPrometheusMetrics(zap.NewNop())
	router := NewRouter(&wiring.Container{
		Config: &config.Config{FrontendURL: "http://localhost:3002"}, Logger: zap.NewNop(),
		Platform:   &wiring.Platform{JWTService: iamtoken.NewJWTService(key), Metrics: metrics},
		LLMGateway: &wiring.LLMGateway{}, Skill: &wiring.Skill{}, Agent: &wiring.Agent{},
		Knowledge: &wiring.Knowledge{}, MCP: &wiring.MCP{},
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)) //nolint:noctx
	if w.Code == http.StatusNotFound {
		t.Fatal("refresh route was removed because GitHub OAuth is not configured")
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", nil)) //nolint:noctx
	if w.Code == http.StatusNotFound {
		t.Fatal("oauth exchange route was not registered with base auth routes")
	}
}
