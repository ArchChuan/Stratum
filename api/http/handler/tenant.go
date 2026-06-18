package handler

import (
	"errors"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
)

func tenantIDFromCtx(c *gin.Context) (string, bool) {
	tc, ok := tenantdb.FromContext(c.Request.Context())
	if !ok || tc.TenantID == "" {
		return "", false
	}
	return tc.TenantID, true
}

func userIDFromCtx(c *gin.Context) (string, bool) {
	v, ok := c.Get(middleware.ContextKeySub)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// errMissingTenant is the canonical sentinel for missing tenant context. Routed
// through ErrorHandler middleware so the response shape stays uniform.
var errMissingTenant = errors.New("tenant context required")

func respondMissingTenant(c *gin.Context) {
	_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errMissingTenant))
}
