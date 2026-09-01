package http

import (
	"os"
	"strings"
	"testing"
)

func readRouterSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestPlatformAdminReadRoutesRequireTenantMember guards the read-only surface of
// 平台管理 pages: every GET backing a 平台管理 page must be registered on a
// member-protected group so all logged-in tenant members can view the data.
func TestPlatformAdminReadRoutesRequireTenantMember(t *testing.T) {
	source := readRouterSource(t)
	if !strings.Contains(source, `platformRead := r.Group("/admin", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)`) {
		t.Fatal("platform admin read group must require authenticated tenant member context")
	}
	for _, line := range []string{
		`platformRead.GET("/tenants", adminHandler.ListTenants)`,
		`platformRead.GET("/tenants/:id", adminHandler.GetTenant)`,
		`platformRead.GET("/admins", adminHandler.ListAdmins)`,
		`registerParameterReadRoutes(platformRead, c)`,
		`readGroup.GET("/parameters/schema", paramHandler.Schema)`,
		`readGroup.GET("/parameters", paramHandler.List)`,
		`readGroup.GET("/parameters/versions/:groupKey", paramHandler.Versions)`,
	} {
		if !strings.Contains(source, line) {
			t.Fatalf("read route must be on member-protected group: %s", line)
		}
	}
	// 平台审计分组头存在，且旧 JWT+RequireSystemAdmin 门控必须被移除（分组定义在
	// 多行内，不做整行匹配）。
	if !strings.Contains(source, `platformAudit := r.Group("/admin/audit/platform",`) {
		t.Fatal("platform audit read group must be registered")
	}
	if strings.Contains(source, "platformAudit := r.Group(\"/admin/audit/platform\",\n\t\t\tmiddleware.JWTMiddleware") {
		t.Fatal("platform audit read group must not remain gated on system_admin")
	}
}

// TestPlatformAdminWriteRoutesRequireSystemAdmin guards the write surface: every
// 平台管理 mutation must stay on the system_admin-gated adminGroup (fail-closed).
func TestPlatformAdminWriteRoutesRequireSystemAdmin(t *testing.T) {
	source := readRouterSource(t)
	for _, line := range []string{
		`adminGroup.POST("/tenants", adminHandler.CreateTenant)`,
		`adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)`,
		`adminGroup.DELETE("/tenants/:id", middleware.RequireGlobalAdmin(), adminHandler.DeleteTenant)`,
		`registerParameterWriteRoutes(adminGroup, c)`,
		`adminGroup.PUT("/parameters", paramHandler.Update)`,
		`adminGroup.POST("/parameters/versions/:groupKey", paramHandler.CreateDraft)`,
		`adminGroup.GET("/users", adminHandler.SearchUsers)`,
	} {
		if !strings.Contains(source, line) {
			t.Fatalf("write route must stay on system_admin group: %s", line)
		}
	}
	if strings.Contains(source, `adminGroup := r.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())`) {
		t.Fatal("platform admin group must require system_admin, never global_admin only")
	}
}
