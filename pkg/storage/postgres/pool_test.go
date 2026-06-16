package postgres_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
)

// TestPoolImplementsInterfaces is a compile-time check that *Pool satisfies
// the consumer-side interfaces. It would fail to compile rather than fail
// at runtime if the contract drifts.
func TestPoolImplementsInterfaces(t *testing.T) {
	var (
		_ postgres.Querier      = (*postgres.Pool)(nil)
		_ postgres.TxBeginner   = (*postgres.Pool)(nil)
		_ postgres.TenantExecer = (*postgres.Pool)(nil)
	)
	t.Log("compile-time interface assertions hold")
}

func TestExecTenant_MissingContext(t *testing.T) {
	err := postgres.ExecTenant(context.Background(), nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestExecTenant_EmptyTenantID(t *testing.T) {
	tc := &postgres.TenantContext{TenantID: "", UserID: "admin", Role: postgres.RoleGlobalAdmin}
	ctx := postgres.WithTenant(context.Background(), tc)
	err := postgres.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}

func TestExecTenant_InvalidTenantID(t *testing.T) {
	tc := &postgres.TenantContext{TenantID: "bad tenant!", UserID: "u1", Role: postgres.RoleTenantAdmin}
	ctx := postgres.WithTenant(context.Background(), tc)
	err := postgres.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for invalid tenant_id")
	}
}

func TestProvisionTenantSchema_EmptyTenantID(t *testing.T) {
	if err := postgres.ProvisionTenantSchema(context.Background(), nil, ""); err == nil {
		t.Fatal("expected error for empty tenantID")
	}
}

func TestProvisionTenantSchema_InvalidTenantID(t *testing.T) {
	if err := postgres.ProvisionTenantSchema(context.Background(), nil, "bad tenant!"); err == nil {
		t.Fatal("expected error for invalid tenantID")
	}
}

func TestWithTenantAndFromContext(t *testing.T) {
	tc := &postgres.TenantContext{
		TenantID: "acme",
		UserID:   "user-1",
		Role:     postgres.RoleTenantAdmin,
	}
	ctx := postgres.WithTenant(context.Background(), tc)
	got, ok := postgres.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context, got none")
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID: want %q, got %q", "acme", got.TenantID)
	}
	if got.UserID != "user-1" {
		t.Errorf("UserID: want %q, got %q", "user-1", got.UserID)
	}
	if got.Role != postgres.RoleTenantAdmin {
		t.Errorf("Role: want %q, got %q", postgres.RoleTenantAdmin, got.Role)
	}
}

func TestFromContext_Missing(t *testing.T) {
	if _, ok := postgres.FromContext(context.Background()); ok {
		t.Fatal("expected no TenantContext in empty context")
	}
}

func TestGlobalAdminEmptyTenantID(t *testing.T) {
	tc := &postgres.TenantContext{TenantID: "", UserID: "admin-1", Role: postgres.RoleGlobalAdmin}
	ctx := postgres.WithTenant(context.Background(), tc)
	got, ok := postgres.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context")
	}
	if got.TenantID != "" {
		t.Errorf("global_admin should have empty TenantID, got %q", got.TenantID)
	}
}
