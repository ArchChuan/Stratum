package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequireSystemRole_Allows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Inject system_admin claims
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySystemRole, string(domain.SystemRoleSystemAdmin))
		c.Next()
	})

	r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequireSystemRole_Denies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Inject regular user claims
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySystemRole, string(domain.SystemRoleUser))
		c.Next()
	})

	r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRequireSystemRole_GlobalAdminCanAccessSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Inject global_admin claims
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySystemRole, string(domain.SystemRoleGlobalAdmin))
		c.Next()
	})

	r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestRequireSystemRole_MissingRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}
