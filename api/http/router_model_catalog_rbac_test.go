package http

import (
	"os"
	"strings"
	"testing"
)

func TestModelCatalogueRequiresTenantMemberContext(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	protectedRoute := `models := r.Group("/models", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)`
	if !strings.Contains(source, protectedRoute) {
		t.Fatal("model catalogue must require authenticated tenant member context")
	}
	if strings.Contains(source, `r.GET("/models", modelHandler.ListModels)`) {
		t.Fatal("model catalogue must not remain on the anonymous health surface")
	}
}

// TestAdminModelCatalogueMutationsRequireSystemAdmin guards the RBAC surface of
// /admin/providers and /admin/models mutations: they must require at least a
// platform system-admin claim, never global-admin-only (平台管理员需能管理模型).
func TestAdminModelCatalogueMutationsRequireSystemAdmin(t *testing.T) {
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, `adminMW := middleware.RequireSystemAdmin()`) {
		t.Fatal("admin model/provider mutations must require at least system_admin")
	}
	if strings.Contains(source, `adminMW := middleware.RequireGlobalAdmin()`) {
		t.Fatal("admin model/provider mutations must not be gated on global_admin only")
	}
}
