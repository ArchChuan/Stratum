package middleware

import (
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"github.com/gin-gonic/gin"
)

// MetricsMiddleware records HTTP request metrics via the pluggable MetricsProvider.
func MetricsMiddleware(metrics observability.MetricsProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.IncHTTPRequestsInFlight()

		c.Next()

		metrics.DecHTTPRequestsInFlight()
		elapsed := time.Since(start).Seconds()
		metrics.IncHTTPRequest(c.Request.Method, c.Request.URL.Path, c.Writer.Status())
		metrics.RecordHTTPRequestDuration(c.Request.Method, c.Request.URL.Path, elapsed)
	}
}
