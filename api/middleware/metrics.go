// Package middleware provides HTTP request middleware.

package middleware

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
)

const unmatchedRouteLabel = "unmatched"

func metricsRouteLabel(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return unmatchedRouteLabel
}

// MetricsMiddleware records HTTP request metrics via the pluggable MetricsProvider.
func MetricsMiddleware(metrics observability.MetricsProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.IncHTTPRequestsInFlight()

		c.Next()

		metrics.DecHTTPRequestsInFlight()
		elapsed := time.Since(start).Seconds()
		path := metricsRouteLabel(c)
		metrics.IncHTTPRequest(c.Request.Method, path, c.Writer.Status())
		metrics.RecordHTTPRequestDuration(c.Request.Method, path, elapsed)
	}
}
