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
		defer func() {
			recovered := recover()
			status := c.Writer.Status()
			if recovered != nil {
				status = 500
			}
			metrics.DecHTTPRequestsInFlight()
			elapsed := time.Since(start).Seconds()
			path := metricsRouteLabel(c)
			metrics.IncHTTPRequest(c.Request.Method, path, status)
			metrics.RecordHTTPRequestDuration(c.Request.Method, path, elapsed)
			if recovered != nil {
				panic(recovered)
			}
		}()

		c.Next()
	}
}
