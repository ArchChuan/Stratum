package handler

import (
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
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
	v, ok := c.Get(auth.ContextKeySub)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func respondMissingTenant(c *gin.Context) {
	c.JSON(401, gin.H{"error": "tenant context required"})
}

// truncate returns s truncated to maxRunes runes (not bytes).
func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
