package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tenantStatusQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RequireActiveTenant aborts with 403 when the current tenant's status is not "active".
// Must run after JWTMiddleware (which sets auth.tenant_id).
// db may be nil (dev mode without DB); in that case the check is skipped.
func RequireActiveTenant(db *pgxpool.Pool) gin.HandlerFunc {
	if db == nil {
		return requireActiveTenant(nil)
	}
	return requireActiveTenant(db)
}

func requireActiveTenant(db tenantStatusQuerier) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil {
			c.Next()
			return
		}

		tenantIDVal, exists := c.Get("auth.tenant_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
			return
		}
		tenantID, _ := tenantIDVal.(string)
		if tenantID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
			return
		}

		var status string
		err := db.QueryRow(c.Request.Context(),
			"SELECT status FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant is not active"})
			} else {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "tenant status unavailable"})
			}
			return
		}

		if status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "租户已被禁用，无法执行此操作",
			})
			return
		}

		c.Next()
	}
}
