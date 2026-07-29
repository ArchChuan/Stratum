package middleware

import (
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
)

type delegationVerifier interface {
	VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error)
}

func RequireDelegatedScope(verifier delegationVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := verifiedDelegation(c, verifier)
		if !ok || !matchesDelegatedScope(c, claims) {
			abortDelegatedScope(c)
			return
		}
		injectDelegatedClaims(c, claims)
		c.Next()
	}
}

func verifiedDelegation(
	c *gin.Context,
	verifier delegationVerifier,
) (*platformmcp.APIDelegationClaims, bool) {
	if verifier == nil {
		return nil, false
	}
	raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if !ok || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return nil, false
	}
	claims, err := verifier.VerifyAPIDelegation(raw)
	return claims, err == nil && claims != nil
}

func matchesDelegatedScope(c *gin.Context, claims *platformmcp.APIDelegationClaims) bool {
	if claims.HTTPMethod != c.Request.Method || claims.PathTemplate != c.FullPath() {
		return false
	}
	return claims.ResourceID == "" || claims.ResourceID == c.Param("id")
}

func injectDelegatedClaims(c *gin.Context, claims *platformmcp.APIDelegationClaims) {
	c.Set(ContextKeySub, claims.Subject)
	c.Set(ContextKeyTenantID, claims.TenantID)
	c.Set(ContextKeyRole, claims.Role)

	tenant := &tenantdb.TenantContext{
		TenantID: claims.TenantID,
		UserID:   claims.Subject,
		Role:     tenantdb.Role(claims.Role),
	}
	ctx := tenantdb.WithTenant(c.Request.Context(), tenant)
	c.Request = c.Request.WithContext(reqctx.WithTenantID(ctx, claims.TenantID))
}

func abortDelegatedScope(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "delegated scope denied"})
}
