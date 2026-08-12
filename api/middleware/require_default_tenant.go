package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// RequireDefaultTenant aborts with 403 unless the request tenant context is the
// default tenant. 平台管理面（机制基线、评测闭环）依附默认租户迭代机制参数——
// 基线建立依赖真实业务场景打磨，普通租户不参与机制管理。比较的是 bootstrap 后
// 解析的真实默认租户 id（tenants.id 是 UUID，绝非字面 "tenant_default"）。
// 未注入租户上下文（缺失 auth.tenant_id）或 bootstrap 未运行时 fail closed。
func RequireDefaultTenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantVal, _ := c.Get(ContextKeyTenantID)
		tenantID, _ := tenantVal.(string)
		if tenantID != constants.ResolvedDefaultTenantID() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "platform management requires default tenant",
			})
			return
		}
		c.Next()
	}
}
