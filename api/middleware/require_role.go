// Package middleware provides HTTP request middleware.

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
)

// Context key constants matching internal/iam/application/middleware.go
const (
	ctxGlobalRole = "auth.global_role"
	ctxRole       = "auth.role"
)

// RequirePlatformAdmin aborts with 403 unless the request context's
// global_role is at or above minRole. Fail-closed: missing or invalid role → 403.
func RequirePlatformAdmin(minRole iamdomain.GlobalRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleVal, _ := c.Get(ctxGlobalRole)
		role, _ := roleVal.(string)
		if !iamdomain.GlobalRole(role).AtLeast(minRole) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "insufficient platform role",
			})
			return
		}
		c.Next()
	}
}

// RequireSystemAdmin requires at least system_admin platform role.
func RequireSystemAdmin() gin.HandlerFunc {
	return RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin)
}

// RequireGlobalAdmin aborts with 403 unless the request context has
// global_role == "global_admin".
func RequireGlobalAdmin() gin.HandlerFunc {
	return RequirePlatformAdmin(iamdomain.GlobalRoleGlobalAdmin)
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
		// Platform admins are treated as at least "admin" in every tenant
		// (defense in depth: switch-tenant/refresh already sign an elevated
		// role, but a stale or downgraded token must not lock out a platform
		// admin). owner is never granted implicitly.
		if grVal, ok := c.Get(ctxGlobalRole); ok {
			if grStr, _ := grVal.(string); iamdomain.GlobalRole(grStr).IsPlatformAdmin() && rank[roleStr] < rank["admin"] {
				roleStr = "admin"
			}
		}
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
