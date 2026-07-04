// Package middleware provides HTTP request middleware.

package middleware

import (
	"bytes"
	"io"
	"strings"
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
const maxBodyLogSize = 2048

// bodyWriter wraps gin.ResponseWriter to capture the response body for logging.
type bodyWriter struct {
	gin.ResponseWriter
	body      *bytes.Buffer
	truncated bool
}

func (bw *bodyWriter) Write(b []byte) (int, error) {
	if !bw.truncated {
		available := maxBodyLogSize - bw.body.Len()
		if available > 0 {
			if len(b) <= available {
				bw.body.Write(b)
			} else {
				bw.body.Write(b[:available])
				bw.truncated = true
			}
		} else {
			bw.truncated = true
		}
	}
	return bw.ResponseWriter.Write(b)
}

func (bw *bodyWriter) WriteString(s string) (int, error) {
	if !bw.truncated {
		available := maxBodyLogSize - bw.body.Len()
		if available > 0 {
			if len(s) <= available {
				bw.body.WriteString(s)
			} else {
				bw.body.WriteString(s[:available])
				bw.truncated = true
			}
		} else {
			bw.truncated = true
		}
	}
	return bw.ResponseWriter.WriteString(s)
}

func (bw *bodyWriter) captured() string {
	s := bw.body.String()
	if bw.truncated {
		return s + "...[truncated]"
	}
	return s
}

// sensitivePathPrefixes — body logging skipped to avoid PII/credential leakage.
var sensitivePathPrefixes = []string{"/auth/"}

func isSensitivePath(path string) bool {
	for _, prefix := range sensitivePathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isJSONContent(contentType string) bool {
	return strings.Contains(contentType, "application/json")
}

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

		sensitive := isSensitivePath(c.Request.URL.Path)

		// capture request body (non-sensitive JSON only); restore body for downstream handlers
		var reqBody string
		if !sensitive && c.Request.Body != nil && isJSONContent(c.Request.Header.Get("Content-Type")) {
			raw, _ := io.ReadAll(c.Request.Body)
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
			if len(raw) > maxBodyLogSize {
				reqBody = string(raw[:maxBodyLogSize]) + "...[truncated]"
			} else {
				reqBody = string(raw)
			}
		}

		// wrap writer to capture response body
		bw := &bodyWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = bw

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
			zap.String("query", c.Request.URL.RawQuery),
			zap.Int("status", status),
			zap.Int64("latency_ms", latencyMs),
			zap.Int("resp_bytes", c.Writer.Size()),
			zap.String("client_ip", c.ClientIP()),
			zap.String("ua", c.Request.UserAgent()),
			zap.String("tenant_id", tenantID),
			zap.String("user_id", userID),
		}
		if reqBody != "" {
			fields = append(fields, zap.String("req_body", reqBody))
		}
		if !sensitive && isJSONContent(c.Writer.Header().Get("Content-Type")) {
			if s := bw.captured(); s != "" {
				fields = append(fields, zap.String("resp_body", s))
			}
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
