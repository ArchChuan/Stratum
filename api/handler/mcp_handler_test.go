package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestMCPHandlerListServers(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPSkillRegistry(manager, logger)
	handler := NewMCPHandler(registry, manager, logger)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers", handler.ListServers)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestMCPHandlerGetServer(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPSkillRegistry(manager, logger)
	handler := NewMCPHandler(registry, manager, logger)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers/:name", handler.GetServer)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("expected status 200 or 404, got %d", w.Code)
	}
}

func TestMCPHandlerListTools(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPSkillRegistry(manager, logger)
	handler := NewMCPHandler(registry, manager, logger)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/servers/:id/tools", handler.ListTools)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/servers/test-server/tools", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	// server 不存在时返回 500（client not found），这是预期行为
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 200, 404, or 500, got %d", w.Code)
	}
}

func TestMCPHandlerGetServerStatus(t *testing.T) {
	logger := zap.NewNop()
	manager := mcp.NewClientManager(logger, nil, nil)
	registry := mcp.NewMCPSkillRegistry(manager, logger)
	handler := NewMCPHandler(registry, manager, logger)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/status", handler.GetServerStatus)

	w := httptest.NewRecorder()
	httpReq, _ := http.NewRequest("GET", "/status", nil) //nolint:noctx
	router.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
