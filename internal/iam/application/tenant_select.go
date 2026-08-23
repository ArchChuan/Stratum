package application

// SelectPreferredTenant returns the tenant a user should land in at login:
// first the non-default tenant they created (owner), else the first
// non-default tenant they joined, else the default tenant. Ties break by the
// order of tenants; an empty list returns empty values.
func SelectPreferredTenant(tenants []TenantInfo) (tenantID, role string) {
	for _, t := range tenants {
		if !t.IsDefault && t.Role == "owner" {
			return t.TenantID, t.Role
		}
	}
	for _, t := range tenants {
		if !t.IsDefault {
			return t.TenantID, t.Role
		}
	}
	if len(tenants) > 0 {
		return tenants[0].TenantID, tenants[0].Role
	}
	return "", ""
}
