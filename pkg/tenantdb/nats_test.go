package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantSubject(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, err := tenantdb.TenantSubject(ctx, "exec.completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant.acme.exec.completed" {
		t.Errorf("want %q, got %q", "tenant.acme.exec.completed", got)
	}
}

func TestTenantSubject_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantSubject(context.Background(), "exec.completed")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantSubject_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantSubject(ctx, "exec.completed")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
