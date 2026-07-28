package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pathRecordingMetrics struct {
	observability.NoopMetrics
	requestPaths  []string
	durationPaths []string
}

func (m *pathRecordingMetrics) IncHTTPRequest(_ string, path string, _ int) {
	m.requestPaths = append(m.requestPaths, path)
}

func (m *pathRecordingMetrics) RecordHTTPRequestDuration(_ string, path string, _ float64) {
	m.durationPaths = append(m.durationPaths, path)
}

func TestMetricsMiddlewareUsesBoundedRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := &pathRecordingMetrics{}
	router := gin.New()
	router.Use(MetricsMiddleware(metrics))
	router.GET("/agents/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, path := range []string{"/agents/agent-123", "/agents/agent-456"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil)) //nolint:noctx
		require.Equal(t, http.StatusNoContent, response.Code)
	}

	require.Equal(t, []string{"/agents/:id", "/agents/:id"}, metrics.requestPaths)
	require.Equal(t, metrics.requestPaths, metrics.durationPaths)
}

func TestMetricsMiddlewareUsesFixedPathForUnmatchedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := &pathRecordingMetrics{}
	router := gin.New()
	router.Use(MetricsMiddleware(metrics))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/unknown/raw-identifier-123", nil)) //nolint:noctx

	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, []string{"unmatched"}, metrics.requestPaths)
	require.Equal(t, metrics.requestPaths, metrics.durationPaths)
}
