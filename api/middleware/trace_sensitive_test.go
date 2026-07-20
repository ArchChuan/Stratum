package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestTraceMiddlewareDoesNotLogQueryOrJSONBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.DebugLevel)
	r := gin.New()
	r.Use(TraceMiddleware(zap.New(core)))
	r.POST("/memory", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"result": "sensitive-sentinel"})
	})
	req := httptest.NewRequest(http.MethodPost,
		"/memory?token=sensitive-sentinel",
		strings.NewReader(`{"content":"sensitive-sentinel"}`),
	) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, entry := range logs.All() {
		fields := entry.ContextMap()
		if strings.Contains(entry.Message, "sensitive-sentinel") ||
			containsLogSentinel(fields["query"]) ||
			containsLogSentinel(fields["req_body"]) ||
			containsLogSentinel(fields["resp_body"]) {
			t.Fatalf("sensitive request data reached access log: %#v", fields)
		}
	}
}

func containsLogSentinel(value any) bool {
	text, _ := value.(string)
	return strings.Contains(text, "sensitive-sentinel")
}
