package application

import (
	"testing"
)

func TestSelectPreferredTenant(t *testing.T) {
	cases := []struct {
		name     string
		tenants  []TenantInfo
		wantID   string
		wantRole string
	}{
		{
			name: "prefers created non-default over joined and default",
			tenants: []TenantInfo{
				{TenantID: "default-1", IsDefault: true, Role: "member"},
				{TenantID: "joined-1", Role: "member"},
				{TenantID: "created-1", Role: "owner"},
			},
			wantID: "created-1", wantRole: "owner",
		},
		{
			name: "prefers joined non-default over default when no created tenant",
			tenants: []TenantInfo{
				{TenantID: "default-1", IsDefault: true, Role: "member"},
				{TenantID: "joined-1", Role: "member"},
			},
			wantID: "joined-1", wantRole: "member",
		},
		{
			name: "falls back to default tenant",
			tenants: []TenantInfo{
				{TenantID: "default-1", IsDefault: true, Role: "member"},
			},
			wantID: "default-1", wantRole: "member",
		},
		{
			name: "empty list returns empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotRole := SelectPreferredTenant(tc.tenants)
			if gotID != tc.wantID || gotRole != tc.wantRole {
				t.Fatalf("SelectPreferredTenant(%v) = (%q, %q), want (%q, %q)",
					tc.tenants, gotID, gotRole, tc.wantID, tc.wantRole)
			}
		})
	}
}
