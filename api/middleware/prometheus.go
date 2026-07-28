// Package middleware provides HTTP request middleware.

package middleware

import (
	"strconv"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// PrometheusMiddleware 记录 HTTP 请求的 Prometheus 指标
func PrometheusMiddleware(metrics *observability.PrometheusMetrics, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 增加正在处理的请求计数
		metrics.IncHTTPRequestsInFlight()
		defer func() {
			recovered := recover()
			status := c.Writer.Status()
			if recovered != nil {
				status = 500
			}
			metrics.DecHTTPRequestsInFlight()
			duration := time.Since(start).Seconds()
			path := metricsRouteLabel(c)
			metrics.IncHTTPRequest(c.Request.Method, path, status)
			metrics.RecordHTTPRequestDuration(c.Request.Method, path, duration)
			logger.Debug("request metrics recorded",
				zap.String("method", c.Request.Method),
				zap.String("path", path),
				zap.Int("status", status),
				zap.Float64("duration_seconds", duration),
			)
			if recovered != nil {
				panic(recovered)
			}
		}()

		c.Next()
	}
}

// MetricsHandler 创建 Prometheus 指标 handler
func MetricsHandler(metrics *observability.PrometheusMetrics) gin.HandlerFunc {
	return gin.WrapH(metrics.GetHandler())
}

// NamespaceMiddleware 为每个请求添加命名空间标签
func NamespaceMiddleware(namespace string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("namespace", namespace)
		c.Next()
	}
}

// TenantMiddleware 为每个请求添加租户标签
func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := c.GetHeader("X-Tenant-ID")
		if tenantID == "" {
			tenantID = "default"
		}
		c.Set("tenant_id", tenantID)
		c.Next()
	}
}

// GetTenantID 从 gin context 中获取租户 ID
func GetTenantID(c *gin.Context) string {
	if tenantID, exists := c.Get("tenant_id"); exists {
		if id, ok := tenantID.(string); ok {
			return id
		}
	}
	return "default"
}

// GetNamespace 从 gin context 中获取命名空间
func GetNamespace(c *gin.Context) string {
	if namespace, exists := c.Get("namespace"); exists {
		if ns, ok := namespace.(string); ok {
			return ns
		}
	}
	return "default"
}

// ParseIntHeader 解析整数类型的 header
func ParseIntHeader(c *gin.Context, key string, defaultValue int) int {
	value := c.GetHeader(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}
