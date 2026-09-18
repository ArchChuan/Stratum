package tenantnaming_test

import (
	"context"
	"testing"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

func ctxWithTenant(tenantID string) context.Context {
	return pgcontext.WithTenant(context.Background(), &pgcontext.TenantContext{
		TenantID: tenantID, UserID: "u1", Role: pgcontext.RoleTenantAdmin,
	})
}

func TestTenantKey(t *testing.T) {
	got, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "exec-1", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "agent:acme:stream:exec-1:1"; got != want {
		t.Errorf("TenantKey = %q, want %q", got, want)
	}
}

func TestTenantKey_MissingContext(t *testing.T) {
	if _, err := tenantnaming.TenantKey(context.Background(), "agent", "stream"); err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantKey_EmptyTenantID(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant(""), "agent", "stream"); err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}

func TestTenantKey_RejectsColonInPart(t *testing.T) {
	// execution_id 是客户端可控的。允许 ':' 会让调用方拼出穿越命名空间的 key
	// （如 execution_id="x:2" 伪造 generation 段，把别人的流读成自己的）。
	_, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "x:2", "1")
	if err == nil {
		t.Fatal("expected error for key part containing ':'")
	}
}

func TestTenantKey_RejectsEmptyPart(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "", "1"); err == nil {
		t.Fatal("expected error for empty key part")
	}
}

func TestTenantKey_RejectsEmptyPrefix(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant("acme"), ""); err == nil {
		t.Fatal("expected error for empty prefix")
	}
}
