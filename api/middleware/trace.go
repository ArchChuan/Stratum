// Package middleware provides HTTP request middleware.

package middleware

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const traceIDHeader = "X-Request-ID"
const traceIDKey = "trace_id"

// TraceMiddleware assigns a request_id and emits a structured access log after each request.
// Level: INFO for <500, WARN for 4xx client errors, ERROR for >=500.
func TraceMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(traceIDHeader)
		// Prefer OTel traceID (set by otelgin) for Jaeger ↔ log correlation.
		if span := oteltrace.SpanFromContext(c.Request.Context()); span.SpanContext().IsValid() {
			requestID = span.SpanContext().TraceID().String()
		} else if requestID == "" {
			requestID = uuid.Must(uuid.NewV7()).String()
		}
		c.Set(traceIDKey, requestID)
		c.Header(traceIDHeader, requestID)
		// Inject trace ID into the standard context so business-layer SpanFromContext
		// picks up the same ID without generating a new one.
		ctx := observability.WithTraceID(c.Request.Context(), requestID)
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		latencyMs := time.Since(start).Milliseconds()

		tenantID := ""
		userID := ""
		if tc, ok := tenantdb.FromContext(c.Request.Context()); ok {
			tenantID = tc.TenantID
			userID = tc.UserID
		}

		fields := []zap.Field{
			zap.String("trace_id", requestID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Int64("latency_ms", latencyMs),
			zap.Int("resp_bytes", c.Writer.Size()),
			zap.String("client_ip", c.ClientIP()),
			zap.String("ua", c.Request.UserAgent()),
			zap.String("tenant_id", tenantID),
			zap.String("user_id", userID),
		}
		switch {
		case status >= 500:
			logger.Error("access", fields...)
		case status >= 400:
			logger.Warn("access", fields...)
		default:
			logger.Info("access", fields...)
		}
	}
}

// GetTraceID retrieves the request_id set by TraceMiddleware.
func GetTraceID(c *gin.Context) string {
	id, _ := c.Get(traceIDKey)
	s, _ := id.(string)
	return s
}
