package constants

import "testing"

func TestResolvedDefaultTenantIDFallsBackToLiteralBeforeBootstrap(t *testing.T) {
	SetResolvedDefaultTenantID(DefaultTenantID) // 保证干净起点（bootstrap 前）
	if got := ResolvedDefaultTenantID(); got != DefaultTenantID {
		t.Fatalf("pre-bootstrap default = %q, want literal %q", got, DefaultTenantID)
	}
}

func TestSetResolvedDefaultTenantIDOverridesLiteral(t *testing.T) {
	const resolved = "11111111-2222-3333-4444-555555555555"
	SetResolvedDefaultTenantID(resolved)
	defer SetResolvedDefaultTenantID(DefaultTenantID)
	if got := ResolvedDefaultTenantID(); got != resolved {
		t.Fatalf("resolved default = %q, want %q", got, resolved)
	}
}
