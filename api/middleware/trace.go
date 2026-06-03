// Package middleware provides HTTP request middleware.

package middleware

import (
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

// TraceMiddleware 为每个 HTTP 请求记录信息（当前简化实现以避免依赖问题）
func TraceMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录请求信息
		logger.Debug("request received",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("remote_addr", c.Request.RemoteAddr),
			zap.String("user_agent", c.Request.UserAgent()),
		)

		// 处理请求
		c.Next()

		// 记录响应信息
		logger.Debug("request completed",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
		)
	}
}

// GetTraceID 从 gin context 中获取 trace ID
func GetTraceID(c *gin.Context) string {
	return "" // 简化实现返回空字符串
}
