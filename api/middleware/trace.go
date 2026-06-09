// Package middleware provides HTTP request middleware.

package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const traceIDHeader = "X-Request-ID"
const traceIDKey = "request_id"

// TraceMiddleware assigns a request_id and logs each request at Info level.
func TraceMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(traceIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set(traceIDKey, requestID)
		c.Header(traceIDHeader, requestID)

		start := time.Now()
		c.Next()

		logger.Info("http",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("query", c.Request.URL.RawQuery),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("request_id", requestID),
			zap.String("remote_addr", c.ClientIP()),
		)
	}
}

// GetTraceID retrieves the request_id set by TraceMiddleware.
func GetTraceID(c *gin.Context) string {
	id, _ := c.Get(traceIDKey)
	s, _ := id.(string)
	return s
}
