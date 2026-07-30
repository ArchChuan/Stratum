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
