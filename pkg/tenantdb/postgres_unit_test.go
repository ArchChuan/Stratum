package tenantdb_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestExecTenant_MissingContext_Unit(t *testing.T) {
	err := tenantdb.ExecTenant(context.Background(), nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestExecTenant_EmptyTenantID_Unit(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	err := tenantdb.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}

func TestExecTenant_InvalidTenantID_Unit(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "bad tenant!", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	err := tenantdb.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for invalid tenant_id")
	}
}
