package tenantnaming_test

import (
	"context"
	"testing"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

func TestTenantLabel(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "acme", UserID: "u1", Role: pgcontext.RoleTenantAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	got, err := tenantnaming.TenantLabel(ctx, "Document")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "T_acme_Document" {
		t.Errorf("want %q, got %q", "T_acme_Document", got)
	}
}

func TestTenantLabel_MissingContext(t *testing.T) {
	_, err := tenantnaming.TenantLabel(context.Background(), "Document")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantLabel_EmptyTenantID(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "", UserID: "admin", Role: pgcontext.RoleGlobalAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	_, err := tenantnaming.TenantLabel(ctx, "Document")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
