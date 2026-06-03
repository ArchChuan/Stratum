package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantCollection(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, err := tenantdb.TenantCollection(ctx, "knowledge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant_acme_knowledge" {
		t.Errorf("want %q, got %q", "tenant_acme_knowledge", got)
	}
}

func TestTenantCollection_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantCollection(context.Background(), "knowledge")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantCollection_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantCollection(ctx, "knowledge")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
