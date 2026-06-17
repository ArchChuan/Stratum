package tenantnaming_test

import (
	"context"
	"testing"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

func TestTenantSubject(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "acme", UserID: "u1", Role: pgcontext.RoleTenantAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	got, err := tenantnaming.TenantSubject(ctx, "exec.completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "tenant.acme.exec.completed" {
		t.Errorf("want %q, got %q", "tenant.acme.exec.completed", got)
	}
}

func TestTenantSubject_MissingContext(t *testing.T) {
	_, err := tenantnaming.TenantSubject(context.Background(), "exec.completed")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantSubject_EmptyTenantID(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "", UserID: "admin", Role: pgcontext.RoleGlobalAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)
	_, err := tenantnaming.TenantSubject(ctx, "exec.completed")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
