package middleware

import (
	"net/http"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/gin-gonic/gin"
)

// RequireSystemRole rejects the request unless the JWT system_role meets the minimum.
func RequireSystemRole(minRole domain.SystemRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		roleStr, exists := c.Get(ContextKeySystemRole)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing system role"})
			return
		}

		role := domain.SystemRole(roleStr.(string))
		if !role.AtLeast(minRole) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient privileges"})
			return
		}

		c.Next()
	}
}
