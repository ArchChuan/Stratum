package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestTraceMiddlewareDoesNotLogRequestOrResponseBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.InfoLevel)
	router := gin.New()
	router.Use(middleware.TraceMiddleware(zap.New(core)))
	router.POST("/echo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"secret": "response-value"})
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"secret":"request-value"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	entries := logs.FilterMessage("access").All()
	if len(entries) != 1 {
		t.Fatalf("access log entries = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	for _, forbidden := range []string{"req_body", "resp_body"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("access log contains forbidden field %q", forbidden)
		}
	}
	if fields["status"] != int64(http.StatusOK) {
		t.Fatalf("status = %v, want %d", fields["status"], http.StatusOK)
	}
	if fields["resp_bytes"] == nil {
		t.Fatal("access log must retain response byte count")
	}
}

func TestTraceMiddlewareLogsStatusWrittenByErrorHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	router := gin.New()
	router.Use(middleware.TraceMiddleware(logger))
	router.Use(middleware.ErrorHandler(logger))
	router.GET("/refresh", func(c *gin.Context) {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("refresh token cookie missing")))
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/refresh", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("response status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	entries := logs.FilterMessage("access").All()
	if len(entries) != 1 {
		t.Fatalf("access log entries = %d, want 1", len(entries))
	}
	if got := entries[0].ContextMap()["status"]; got != int64(http.StatusUnauthorized) {
		t.Fatalf("access log status = %v, want %d", got, http.StatusUnauthorized)
	}
}
