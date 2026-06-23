package domain

import "github.com/byteBuilderX/stratum/pkg/constants"

// SystemRole represents derived system-level permission tier.
type SystemRole string

const (
	SystemRoleGlobalAdmin SystemRole = "global_admin" // default tenant + root
	SystemRoleSystemAdmin SystemRole = "system_admin" // default tenant + admin
	SystemRoleUser        SystemRole = "user"         // all others
)

// TenantMembership represents user's role in a tenant.
type TenantMembership struct {
	TenantID string
	Role     string
}

// DeriveSystemRole computes SystemRole from user's tenant memberships.
// Logic: if user has default tenant membership, map role; else user.
func DeriveSystemRole(memberships []TenantMembership) SystemRole {
	for _, m := range memberships {
		if m.TenantID == constants.DefaultTenantID {
			switch m.Role {
			case "root":
				return SystemRoleGlobalAdmin
			case "admin":
				return SystemRoleSystemAdmin
			}
		}
	}
	return SystemRoleUser
}

// AtLeast returns true if this role's rank is >= target's rank.
func (r SystemRole) AtLeast(target SystemRole) bool {
	rank := func(role SystemRole) int {
		switch role {
		case SystemRoleGlobalAdmin:
			return 3
		case SystemRoleSystemAdmin:
			return 2
		case SystemRoleUser:
			return 1
		default:
			return 0
		}
	}
	return rank(r) >= rank(target)
}
