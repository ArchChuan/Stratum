// Package auth provides JWT token management and authentication.
package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Context keys for values injected by JWTMiddleware.
const (
	ContextKeySub        = "auth.sub"
	ContextKeyTenantID   = "auth.tenant_id"
	ContextKeyRole       = "auth.role"
	ContextKeyGlobalRole = "auth.global_role"
	ContextKeyJTI        = "auth.jti"
)

// JWTMiddleware validates the Bearer token and injects claims into the Gin context.
func JWTMiddleware(svc *JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := svc.Verify(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(ContextKeySub, claims.Sub)
		c.Set(ContextKeyTenantID, claims.TenantID)
		c.Set(ContextKeyRole, claims.Role)
		c.Set(ContextKeyGlobalRole, claims.GlobalRole)
		c.Set(ContextKeyJTI, claims.JTI)
		c.Next()
	}
}
