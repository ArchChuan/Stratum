package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantLabel(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, err := tenantdb.TenantLabel(ctx, "Document")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "T_acme_Document" {
		t.Errorf("want %q, got %q", "T_acme_Document", got)
	}
}

func TestTenantLabel_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantLabel(context.Background(), "Document")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantLabel_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantLabel(ctx, "Document")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
