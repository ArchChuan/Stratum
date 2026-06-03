package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

// TenantClaims extends jwt.RegisteredClaims with tenant-specific fields.
type TenantClaims struct {
	jwt.RegisteredClaims
	TenantID string        `json:"tenant_id"`
	Role     tenantdb.Role `json:"role"`
}

// TenantAuthMiddleware parses the Bearer JWT (HMAC-SHA256) from the Authorization
// header and injects a TenantContext into the request context.
// global_admin tokens may have an empty TenantID; all other roles require one.
func TenantAuthMiddleware(jwtSecret []byte, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization format, expected Bearer token"})
			return
		}

		claims := &TenantClaims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			logger.Warn("invalid JWT token", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		switch claims.Role {
		case tenantdb.RoleTenantAdmin, tenantdb.RoleTenantUser, tenantdb.RoleGlobalAdmin:
		default:
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "unknown role in token"})
			return
		}

		if claims.Role != tenantdb.RoleGlobalAdmin && claims.TenantID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant_id required for non-global_admin role"})
			return
		}

		tc := &tenantdb.TenantContext{
			TenantID: claims.TenantID,
			UserID:   claims.Subject,
			Role:     claims.Role,
		}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
