package domain

import "time"

// TenantInfo is one tenant a user belongs to with their role and join timeline.
type TenantInfo struct {
	TenantID  string
	Name      string
	IsDefault bool
	Role      string
	CreatedAt time.Time
}

// CreateTenantInput holds the fields needed to create a new tenant for a GitHub user.
type CreateTenantInput struct {
	GitHubID    int64
	GitHubLogin string
	AvatarURL   string
	Name        string
	GitHubOrg   string
}

// CreateTenantResult is returned on successful tenant creation.
type CreateTenantResult struct {
	TenantID   string
	SchemaName string
	UserUUID   string
}

// JoinTenantInput holds the fields needed to join an existing tenant via invitation.
type JoinTenantInput struct {
	UserID          string
	InvitationToken string
}

// AutoJoinInput holds the fields for the AutoJoinDefaultTenant flow.
type AutoJoinInput struct {
	GitHubID         int64
	GitHubLogin      string
	AvatarURL        string
	GlobalAdminLogin string
}
