package middleware

import (
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func ErrorHandler(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}
		ginErr := c.Errors.Last()

		// pull context fields set by earlier middleware
		requestID, _ := c.Get(traceIDKey)
		tenantID := ""
		if tc, ok := tenantdb.FromContext(c.Request.Context()); ok {
			tenantID = tc.TenantID
		}

		status := MapErrorToStatus(ginErr.Err)

		logFn := logger.Error
		if status >= 400 && status < 500 {
			logFn = logger.Warn
		}
		logFn("request error",
			zap.String("trace_id", asStr(requestID)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("tenant_id", tenantID),
			zap.Int("status", status),
			zap.Uint64("error_type", uint64(ginErr.Type)),
			zap.Error(ginErr.Err),
		)

		if !c.Writer.Written() {
			msg := ginErr.Error()
			if status >= 500 {
				msg = "internal server error"
			}
			c.JSON(status, gin.H{"error": msg})
		}
	}
}

func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func CORSMiddleware(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func TrustedProxies() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

// SecurityHeaders sets defensive HTTP response headers.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}
