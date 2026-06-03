// Package middleware provides HTTP request middleware.

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Context key constants matching internal/auth/middleware.go
const (
	ctxGlobalRole = "auth.global_role"
	ctxRole       = "auth.role"
)

// RequireGlobalAdmin aborts with 403 unless the request context has global_role == "global_admin".
func RequireGlobalAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get(ctxGlobalRole)
		if role != "global_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "global admin role required",
			})
			return
		}
		c.Next()
	}
}

// RequireTenantRole aborts with 403 unless the tenant role is at or above minRole.
// Role hierarchy: owner > admin > member.
func RequireTenantRole(minRole string) gin.HandlerFunc {
	rank := map[string]int{"member": 1, "admin": 2, "owner": 3}
	required := rank[minRole]
	if required == 0 {
		panic("require_role: unknown minRole: " + minRole)
	}

	return func(c *gin.Context) {
		roleVal, _ := c.Get(ctxRole)
		roleStr, _ := roleVal.(string)
		if rank[roleStr] < required {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "insufficient tenant role",
			})
			return
		}
		c.Next()
	}
}
