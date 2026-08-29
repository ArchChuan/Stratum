package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/gin-gonic/gin"
)

func TestRequireGlobalAdmin_allowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "global_admin") }, middleware.RequireGlobalAdmin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireGlobalAdmin_denied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", middleware.RequireGlobalAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_ownerAllowedForAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.role", "owner") }, middleware.RequireTenantRole("admin"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireTenantRole_memberDeniedForAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.role", "member") }, middleware.RequireTenantRole("admin"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_noRoleDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", middleware.RequireTenantRole("member"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_globalForGlobalMin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "global_admin") },
		middleware.RequirePlatformAdmin(iamdomain.GlobalRoleGlobalAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_systemForSystemMin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "system_admin") },
		middleware.RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_systemDeniedForGlobalMin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "system_admin") },
		middleware.RequirePlatformAdmin(iamdomain.GlobalRoleGlobalAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_userDeniedForSystemMin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "user") },
		middleware.RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_missingDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", middleware.RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequirePlatformAdmin_garbageRoleDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "owner") },
		middleware.RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireGlobalAdmin_delegatesToPlatform(t *testing.T) {
	// 行为回归：system_admin 不得通过 RequireGlobalAdmin。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Set("auth.global_role", "system_admin") },
		middleware.RequireGlobalAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_platformAdminMemberElevated(t *testing.T) {
	// system_admin + stale member role must pass the admin guard.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		c.Set("auth.global_role", "system_admin")
		c.Set("auth.role", "member")
	}, middleware.RequireTenantRole("admin"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireTenantRole_platformAdminMemberDeniedOwner(t *testing.T) {
	// Platform admin must NOT be elevated to owner: owner guard still rejects.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		c.Set("auth.global_role", "global_admin")
		c.Set("auth.role", "member")
	}, middleware.RequireTenantRole("owner"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_nonAdminMemberStillDenied(t *testing.T) {
	// Regression: ordinary member unaffected by platform-admin elevation.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		c.Set("auth.global_role", "user")
		c.Set("auth.role", "member")
	}, middleware.RequireTenantRole("admin"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
