package domain

// Tenant role constants mirroring tenant_members.role values. Centralized so
// role promotion (EffectiveTenantRole) and guards agree on ranks.
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

var tenantRoleRank = map[string]int{RoleMember: 1, RoleAdmin: 2, RoleOwner: 3}

// EffectiveTenantRole returns the user's effective role inside a tenant.
// Platform admins (system_admin/global_admin) are treated as at least "admin"
// in every tenant — including tenants they are not a member of — without ever
// being promoted above their real role past admin. "owner" is never granted
// implicitly. realRole may be empty (non-member).
func EffectiveTenantRole(realRole, globalRole string) string {
	if !GlobalRole(globalRole).IsPlatformAdmin() {
		return realRole
	}
	if tenantRoleRank[realRole] >= tenantRoleRank[RoleAdmin] {
		return realRole
	}
	return RoleAdmin
}
