package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func newTestMCPHandler(t *testing.T) *MCPHandler {
	t.Helper()
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPToolRegistry(manager, logger)
	svc := mcpapp.NewMCPService(mcp.ToolRegistryAsPort(registry), mcp.ServerManagerAsPort(manager), logger)
	return NewMCPHandler(svc, logger)
}

func TestMCPHandlerListServers(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers", h.ListServers)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestMCPHandlerGetServer(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.GET("/servers/:id", h.GetServer)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("expected status 200 or 404, got %d", w.Code)
	}
}

func TestMCPHandlerListTools(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers/:id/tools", h.ListTools)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server/tools", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	// server 不存在时返回 500（client not found），这是预期行为
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 200, 404, or 500, got %d", w.Code)
	}
}

func TestMCPHandlerGetServerStatus(t *testing.T) {
	h := newTestMCPHandler(t)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/status", h.GetServerStatus)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/status", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestMCPConfigRouteRequiresTenantAdmin(t *testing.T) {
	t.Parallel()

	h := newTestMCPHandler(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.role", c.GetHeader("X-Test-Role"))
		c.Next()
	})
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	h.RegisterRoutes(router, nil, nil, []gin.HandlerFunc{middleware.RequireTenantRole("admin")})

	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{name: "member forbidden", role: "member", wantStatus: http.StatusForbidden},
		{name: "admin reaches handler", role: "admin", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/servers/missing/config", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("X-Test-Role", tt.role)
			router.ServeHTTP(recorder, req)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}
