// Package model defines API data models and types.

package model

import "time"

// CreateTenantRequest is the body for POST /admin/tenants.
type CreateTenantRequest struct {
	Name   string `json:"name" binding:"required"`
	Slug   string `json:"slug" binding:"required"`
	Plan   string `json:"plan" binding:"required,oneof=free pro enterprise"`
	Status string `json:"status" binding:"required,oneof=active suspended"`
}

// UpdateTenantRequest is the body for PATCH /admin/tenants/:id.
type UpdateTenantRequest struct {
	Plan   string `json:"plan" binding:"omitempty,oneof=free pro enterprise"`
	Status string `json:"status" binding:"omitempty,oneof=active suspended"`
}

// TenantResponse is the shape returned by all tenant endpoints.
type TenantResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	Plan      string     `json:"plan"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// ListTenantsResponse wraps paginated tenant results.
type ListTenantsResponse struct {
	Tenants  []TenantResponse `json:"tenants"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// InviteMemberRequest is the body for POST /tenant/members/invite.
type InviteMemberRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required,oneof=member admin owner"`
}

// InviteMemberResponse is returned after creating an invitation.
type InviteMemberResponse struct {
	InvitationID  string    `json:"invitation_id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	InvitationURL string    `json:"invitation_url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// UpdateMemberRoleRequest is the body for PATCH /tenant/members/:user_id/role.
type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=member admin owner"`
}

// MemberResponse represents a single tenant member.
type MemberResponse struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

// ListMembersResponse wraps paginated member results.
type ListMembersResponse struct {
	Members  []MemberResponse `json:"members"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// UpdateSettingsRequest is the body for PATCH /tenant/settings.
type UpdateSettingsRequest struct {
	Settings map[string]interface{} `json:"settings" binding:"required"`
}

// SettingsResponse wraps the current settings JSONB.
type SettingsResponse struct {
	TenantID string                 `json:"tenant_id"`
	Settings map[string]interface{} `json:"settings"`
}
