package domain_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
)

func TestDeriveSystemRole(t *testing.T) {
	tests := []struct {
		name        string
		tenantID    string
		role        string
		wantSysRole domain.SystemRole
	}{
		{
			name:        "global admin: default tenant + role=root",
			tenantID:    constants.DefaultTenantID,
			role:        "root",
			wantSysRole: domain.SystemRoleGlobalAdmin,
		},
		{
			name:        "system admin: default tenant + role=admin",
			tenantID:    constants.DefaultTenantID,
			role:        "admin",
			wantSysRole: domain.SystemRoleSystemAdmin,
		},
		{
			name:        "regular user: no default tenant",
			tenantID:    "tenant_abc123",
			role:        "admin",
			wantSysRole: domain.SystemRoleUser,
		},
		{
			name:        "regular user: empty memberships",
			tenantID:    "",
			role:        "",
			wantSysRole: domain.SystemRoleUser,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var memberships []domain.TenantMembership
			if tt.tenantID != "" {
				memberships = []domain.TenantMembership{
					{TenantID: tt.tenantID, Role: tt.role},
				}
			}
			got := domain.DeriveSystemRole(memberships)
			require.Equal(t, tt.wantSysRole, got)
		})
	}
}

// TestDeriveSystemRole_ResolvedDefaultTenantID 回归真实运行时缺陷：默认租户 id
// 是 bootstrap 解析出的 UUID，真实 membership 里永远不会出现字面 "tenant_default"。
// 覆盖已解析 id 时的 admin/root 推导，以及非默认租户 root 仍是普通用户。
func TestDeriveSystemRole_ResolvedDefaultTenantID(t *testing.T) {
	const resolved = "11111111-2222-3333-4444-555555555555"
	constants.SetResolvedDefaultTenantID(resolved)
	defer constants.SetResolvedDefaultTenantID(constants.DefaultTenantID)

	require.Equal(t, domain.SystemRoleSystemAdmin,
		domain.DeriveSystemRole([]domain.TenantMembership{{TenantID: resolved, Role: "admin"}}))
	require.Equal(t, domain.SystemRoleGlobalAdmin,
		domain.DeriveSystemRole([]domain.TenantMembership{{TenantID: resolved, Role: "root"}}))
	require.Equal(t, domain.SystemRoleUser,
		domain.DeriveSystemRole([]domain.TenantMembership{{TenantID: "tenant_acme", Role: "root"}}))
}

func TestSystemRoleAtLeast(t *testing.T) {
	tests := []struct {
		name   string
		role   domain.SystemRole
		target domain.SystemRole
		want   bool
	}{
		{
			name:   "global_admin >= system_admin",
			role:   domain.SystemRoleGlobalAdmin,
			target: domain.SystemRoleSystemAdmin,
			want:   true,
		},
		{
			name:   "system_admin >= system_admin",
			role:   domain.SystemRoleSystemAdmin,
			target: domain.SystemRoleSystemAdmin,
			want:   true,
		},
		{
			name:   "user < system_admin",
			role:   domain.SystemRoleUser,
			target: domain.SystemRoleSystemAdmin,
			want:   false,
		},
		{
			name:   "global_admin >= user",
			role:   domain.SystemRoleGlobalAdmin,
			target: domain.SystemRoleUser,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.role.AtLeast(tt.target)
			require.Equal(t, tt.want, got)
		})
	}
}
