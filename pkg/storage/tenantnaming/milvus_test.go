package tenantnaming_test

import (
	"context"
	"strings"
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

func TestTenantCollection_SanitizesUUIDTenantID(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "019047ac-0000-7000-9000-000000000001", UserID: "u1", Role: pgcontext.RoleTenantAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)

	got, err := tenantnaming.TenantCollection(ctx, "knowledge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "tenant_019047ac_0000_7000_9000_000000000001_knowledge"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestWorkspacePartitionKeepsSafeWorkspaceID(t *testing.T) {
	got := tenantnaming.WorkspacePartition("workspace_123")
	if got != "workspace_123" {
		t.Fatalf("expected safe workspace ID to remain unchanged, got %q", got)
	}
}

func TestWorkspacePartitionHashesUnsafeWorkspaceID(t *testing.T) {
	got := tenantnaming.WorkspacePartition("项目资料")
	if len(got) != 17 {
		t.Fatalf("expected hash prefix plus 16 hex chars, got %q", got)
	}
	if got[0] != 'h' {
		t.Fatalf("expected hashed partition to start with h, got %q", got)
	}
}

func TestWorkspaceCollectionSanitizesTenantAndWorkspace(t *testing.T) {
	tc := &pgcontext.TenantContext{TenantID: "tenant-1", UserID: "u1", Role: pgcontext.RoleTenantAdmin}
	ctx := pgcontext.WithTenant(context.Background(), tc)

	got, err := tenantnaming.WorkspaceCollection(ctx, "workspace-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got == "tenant_tenant-1_kb_workspace-1" {
		t.Fatalf("expected unsafe tenant/workspace characters to be sanitized, got %q", got)
	}
	if !strings.HasPrefix(got, "tenant_tenant_1_kb_h") {
		t.Fatalf("expected tenant prefix to be preserved, got %q", got)
	}
}
