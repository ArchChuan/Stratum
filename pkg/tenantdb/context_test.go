package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestWithTenantAndFromContext(t *testing.T) {
	tc := &tenantdb.TenantContext{
		TenantID: "acme",
		UserID:   "user-1",
		Role:     tenantdb.RoleTenantAdmin,
	}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, ok := tenantdb.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context, got none")
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID: want %q, got %q", "acme", got.TenantID)
	}
	if got.UserID != "user-1" {
		t.Errorf("UserID: want %q, got %q", "user-1", got.UserID)
	}
	if got.Role != tenantdb.RoleTenantAdmin {
		t.Errorf("Role: want %q, got %q", tenantdb.RoleTenantAdmin, got.Role)
	}
}

func TestFromContext_Missing(t *testing.T) {
	_, ok := tenantdb.FromContext(context.Background())
	if ok {
		t.Fatal("expected no TenantContext in empty context")
	}
}

func TestGlobalAdminEmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin-1", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, ok := tenantdb.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context")
	}
	if got.TenantID != "" {
		t.Errorf("global_admin should have empty TenantID, got %q", got.TenantID)
	}
}
