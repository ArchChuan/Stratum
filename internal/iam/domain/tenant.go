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
