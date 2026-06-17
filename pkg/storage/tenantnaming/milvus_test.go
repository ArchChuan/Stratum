package tenantnaming_test

import (
	"context"
	"testing"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

func TestTenantCollection(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "acme", UserID: "u1", Role: pgcontext.RoleTenantAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	got, err := tenantnaming.TenantCollection(ctx, "knowledge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant_acme_knowledge" {
		t.Errorf("want %q, got %q", "tenant_acme_knowledge", got)
	}
}

func TestTenantCollection_MissingContext(t *testing.T) {
	_, err := tenantnaming.TenantCollection(context.Background(), "knowledge")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantCollection_EmptyTenantID(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "", UserID: "admin", Role: pgcontext.RoleGlobalAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	_, err := tenantnaming.TenantCollection(ctx, "knowledge")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
