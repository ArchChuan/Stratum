package domain

import "testing"

func TestGlobalRoleValid(t *testing.T) {
	cases := []struct {
		role GlobalRole
		want bool
	}{
		{GlobalRoleUser, true}, {GlobalRoleSystemAdmin, true}, {GlobalRoleGlobalAdmin, true},
		{GlobalRole(""), false}, {GlobalRole("owner"), false}, {GlobalRole("admin"), false},
	}
	for _, tc := range cases {
		if got := tc.role.Valid(); got != tc.want {
			t.Errorf("GlobalRole(%q).Valid() = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestGlobalRoleAtLeast(t *testing.T) {
	cases := []struct {
		role GlobalRole
		min  GlobalRole
		want bool
	}{
		{GlobalRoleGlobalAdmin, GlobalRoleGlobalAdmin, true},
		{GlobalRoleGlobalAdmin, GlobalRoleSystemAdmin, true},
		{GlobalRoleGlobalAdmin, GlobalRoleUser, true},
		{GlobalRoleSystemAdmin, GlobalRoleGlobalAdmin, false},
		{GlobalRoleSystemAdmin, GlobalRoleSystemAdmin, true},
		{GlobalRoleSystemAdmin, GlobalRoleUser, true},
		{GlobalRoleUser, GlobalRoleSystemAdmin, false},
		{GlobalRoleUser, GlobalRoleUser, true},
		// 未知角色 fail closed：比 user 还低。
		{GlobalRole(""), GlobalRoleUser, false},
		{GlobalRole("owner"), GlobalRoleUser, false},
	}
	for _, tc := range cases {
		if got := tc.role.AtLeast(tc.min); got != tc.want {
			t.Errorf("GlobalRole(%q).AtLeast(%q) = %v, want %v", tc.role, tc.min, got, tc.want)
		}
	}
}
