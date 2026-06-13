package middleware

import (
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
)

// InjectTenantContext bridges auth.JWTMiddleware (which sets Gin context keys)
// to tenantdb.TenantContext (which handlers read via c.Request.Context()).
func InjectTenantContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, _ := c.Get("auth.tenant_id")
		sub, _ := c.Get("auth.sub")
		role, _ := c.Get("auth.role")

		tid, _ := tenantID.(string)
		uid, _ := sub.(string)
		r, _ := role.(string)

		if tid != "" {
			tc := &tenantdb.TenantContext{
				TenantID: tid,
				UserID:   uid,
				Role:     tenantdb.Role(r),
			}
			ctx := tenantdb.WithTenant(c.Request.Context(), tc)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}
