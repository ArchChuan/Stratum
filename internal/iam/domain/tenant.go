package domain

import "time"

// Member represents a tenant membership row joined with the user.
type Member struct {
	UserID      string
	GitHubLogin string
	AvatarURL   string
	Role        string
	JoinedAt    time.Time
}

// Invitation represents a pending tenant invitation.
type Invitation struct {
	ID        string
	TenantID  string
	Email     string
	Role      string
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
	InvitedBy string
}

// UserTenantInfo describes one tenant a user belongs to.
type UserTenantInfo struct {
	TenantID  string
	Name      string
	IsDefault bool
}

// Tenant aggregates the public.tenants row used by admin flows.
type Tenant struct {
	ID          string
	Name        string
	Slug        string
	Plan        string
	Status      string
	CreatedAt   time.Time
	DeletedAt   *time.Time
	MemberCount int
}

// TenantFilter narrows admin tenant listing.
type TenantFilter struct {
	Status   string
	Page     int
	PageSize int
}

// TenantPatch carries optional fields for tenant updates. Empty strings mean "leave alone".
type TenantPatch struct {
	Plan   string
	Status string
}

// StoredSession holds the user/tenant context persisted alongside a refresh token.
type StoredSession struct {
	UserID      string
	TenantID    string
	AvatarURL   string
	GitHubLogin string
}
