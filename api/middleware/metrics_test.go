package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pathRecordingMetrics struct {
	observability.NoopMetrics
	mu            sync.Mutex
	requestPaths  []string
	durationPaths []string
	statuses      []int
	inflight      int
}

func (m *pathRecordingMetrics) IncHTTPRequest(_ string, path string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestPaths = append(m.requestPaths, path)
	m.statuses = append(m.statuses, status)
}

func (m *pathRecordingMetrics) RecordHTTPRequestDuration(_ string, path string, _ float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durationPaths = append(m.durationPaths, path)
}

func (m *pathRecordingMetrics) IncHTTPRequestsInFlight() { m.mu.Lock(); m.inflight++; m.mu.Unlock() }
func (m *pathRecordingMetrics) DecHTTPRequestsInFlight() { m.mu.Lock(); m.inflight--; m.mu.Unlock() }

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

func TestMetricsMiddlewareFinalizesPanickingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	metrics := &pathRecordingMetrics{}
	router := gin.New()
	router.Use(gin.Recovery(), MetricsMiddleware(metrics))
	router.GET("/agents/:id", func(_ *gin.Context) { panic("boom") })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/agents/agent-123", nil)) //nolint:noctx

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, []string{"/agents/:id"}, metrics.requestPaths)
	require.Equal(t, []string{"/agents/:id"}, metrics.durationPaths)
	require.Equal(t, []int{http.StatusInternalServerError}, metrics.statuses)
	require.Zero(t, metrics.inflight)
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
