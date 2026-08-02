package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestRequireDelegatedScopeEnforcesExactRouteScope(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		claims     *platformmcp.APIDelegationClaims
		verifyErr  error
		wantStatus int
	}{
		{name: "allows exact scope", method: http.MethodGet, path: "/internal/models/resource-1", claims: validDelegationClaims(), wantStatus: http.StatusNoContent},
		{name: "rejects wrong method", method: http.MethodPost, path: "/internal/models/resource-1", claims: validDelegationClaims(), wantStatus: http.StatusForbidden},
		{name: "rejects wrong path template", method: http.MethodGet, path: "/internal/models/resource-1", claims: delegationClaimsWithPath("/internal/workflows/:id"), wantStatus: http.StatusForbidden},
		{name: "rejects wrong resource", method: http.MethodGet, path: "/internal/models/resource-2", claims: validDelegationClaims(), wantStatus: http.StatusForbidden},
		{name: "rejects invalid token profile", method: http.MethodGet, path: "/internal/models/resource-1", claims: validDelegationClaims(), verifyErr: errors.New("wrong audience"), wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &delegationVerifierFake{claims: tc.claims, err: tc.verifyErr}
			router := delegatedScopeTestRouter(verifier)
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer delegation")
			res := httptest.NewRecorder()

			router.ServeHTTP(res, req)

			if res.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d", res.Code, tc.wantStatus)
			}
		})
	}
}

func TestRequireDelegatedScopeInjectsTenantContext(t *testing.T) {
	verifier := &delegationVerifierFake{claims: validDelegationClaims()}
	router := gin.New()
	router.GET("/internal/models/:id", RequireDelegatedScope(verifier, observability.NoopMetrics{}), func(c *gin.Context) {
		tenant, ok := tenantdb.FromContext(c.Request.Context())
		if !ok || tenant.TenantID != "tenant-1" || tenant.UserID != "user-1" || string(tenant.Role) != "admin" {
			c.Status(http.StatusInternalServerError)
			return
		}
		if reqctx.TenantIDFromContext(c.Request.Context()) != "tenant-1" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/internal/models/resource-1", nil)
	req.Header.Set("Authorization", "Bearer delegation")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want %d", res.Code, http.StatusNoContent)
	}
}

func delegatedScopeTestRouter(verifier delegationVerifier) *gin.Engine {
	router := gin.New()
	router.Any("/internal/models/:id", RequireDelegatedScope(verifier, observability.NoopMetrics{}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return router
}

func validDelegationClaims() *platformmcp.APIDelegationClaims {
	return &platformmcp.APIDelegationClaims{
		TenantID: "tenant-1", HTTPMethod: http.MethodGet, PathTemplate: "/internal/models/:id",
		ResourceID: "resource-1", Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-1"},
	}
}

func delegationClaimsWithPath(path string) *platformmcp.APIDelegationClaims {
	claims := validDelegationClaims()
	claims.PathTemplate = path
	return claims
}

type delegationVerifierFake struct {
	claims *platformmcp.APIDelegationClaims
	err    error
}

func (f *delegationVerifierFake) VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error) {
	return f.claims, f.err
}
