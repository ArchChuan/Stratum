package domain

// GlobalRole is the platform-wide role on users.global_role, fully independent
// of tenant membership roles (tenant_members.role). "user" is the default for
// every account; "system_admin" and "global_admin" are platform admins.
type GlobalRole string

const (
	// GlobalRoleUser is the default platform role for all non-admin accounts.
	GlobalRoleUser GlobalRole = "user"
	// GlobalRoleSystemAdmin is a platform admin promoted by a global admin via UI.
	GlobalRoleSystemAdmin GlobalRole = "system_admin"
	// GlobalRoleGlobalAdmin is the super admin. Only provisioned programmatically
	// (env GlobalAdmin + seed); never settable through the UI.
	GlobalRoleGlobalAdmin GlobalRole = "global_admin"
)

var globalRoleRank = map[GlobalRole]int{
	GlobalRoleUser:        1,
	GlobalRoleSystemAdmin: 2,
	GlobalRoleGlobalAdmin: 3,
}

// Valid reports whether r is one of the three supported global roles.
func (r GlobalRole) Valid() bool {
	_, ok := globalRoleRank[r]
	return ok
}

// AtLeast reports whether r is at or above min in the platform hierarchy
// (global_admin > system_admin > user). Unknown roles rank below user so
// guards fail closed.
func (r GlobalRole) AtLeast(min GlobalRole) bool {
	return globalRoleRank[r] >= globalRoleRank[min]
}
