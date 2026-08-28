package domain

import "testing"

func TestEffectiveTenantRole(t *testing.T) {
	tests := []struct {
		name       string
		realRole   string
		globalRole string
		want       string
	}{
		{"user member unchanged", "member", "user", "member"},
		{"user admin unchanged", "admin", "user", "admin"},
		{"user owner unchanged", "owner", "user", "owner"},
		{"user non-member unchanged", "", "user", ""},
		{"system admin member elevated", "member", "system_admin", "admin"},
		{"system admin non-member elevated", "", "system_admin", "admin"},
		{"system admin admin kept", "admin", "system_admin", "admin"},
		{"system admin owner kept", "owner", "system_admin", "owner"},
		{"global admin non-member elevated", "", "global_admin", "admin"},
		{"global admin owner kept", "owner", "global_admin", "owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveTenantRole(tt.realRole, tt.globalRole); got != tt.want {
				t.Fatalf("EffectiveTenantRole(%q, %q) = %q, want %q", tt.realRole, tt.globalRole, got, tt.want)
			}
		})
	}
}

func TestGlobalRoleIsPlatformAdmin(t *testing.T) {
	tests := []struct {
		role GlobalRole
		want bool
	}{
		{GlobalRoleUser, false},
		{GlobalRoleSystemAdmin, true},
		{GlobalRoleGlobalAdmin, true},
		{GlobalRole("garbage"), false},
	}
	for _, tt := range tests {
		if got := tt.role.IsPlatformAdmin(); got != tt.want {
			t.Fatalf("GlobalRole(%q).IsPlatformAdmin() = %v, want %v", tt.role, got, tt.want)
		}
	}
}
