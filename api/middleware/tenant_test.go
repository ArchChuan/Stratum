package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func init() {
	gin.SetMode(gin.TestMode)
}

var testSecret = []byte("test-secret-key")

func makeToken(tenantID, userID string, role tenantdb.Role, secret []byte, ttl time.Duration) string {
	claims := middleware.TenantClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		TenantID: tenantID,
		Role:     role,
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	return tok
}

func setupTenantRouter() (*gin.Engine, *zap.Logger) {
	logger := zap.NewNop()
	r := gin.New()
	r.Use(middleware.TenantAuthMiddleware(testSecret, logger))
	r.GET("/protected", func(c *gin.Context) {
		tc, ok := tenantdb.FromContext(c.Request.Context())
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no tenant ctx"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"tenant_id": tc.TenantID, "role": tc.Role})
	})
	return r, logger
}

func TestTenantAuthMiddleware_ValidToken(t *testing.T) {
	r, _ := setupTenantRouter()
	tok := makeToken("acme", "user-1", tenantdb.RoleTenantAdmin, testSecret, 15*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestTenantAuthMiddleware_MissingHeader(t *testing.T) {
	r, _ := setupTenantRouter()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestTenantAuthMiddleware_InvalidToken(t *testing.T) {
	r, _ := setupTenantRouter()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestTenantAuthMiddleware_GlobalAdmin_EmptyTenantID(t *testing.T) {
	r, _ := setupTenantRouter()
	tok := makeToken("", "admin-1", tenantdb.RoleGlobalAdmin, testSecret, 15*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for global_admin, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestTenantAuthMiddleware_NonAdminEmptyTenantID(t *testing.T) {
	r, _ := setupTenantRouter()
	tok := makeToken("", "user-1", tenantdb.RoleTenantUser, testSecret, 15*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin with empty tenant_id, got %d", w.Code)
	}
}

func TestTenantAuthMiddleware_ExpiredToken(t *testing.T) {
	r, _ := setupTenantRouter()
	tok := makeToken("acme", "user-1", tenantdb.RoleTenantAdmin, testSecret, -1*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}
