package domain

import (
	"errors"
	"time"
)

var ErrInvitationInvalid = errors.New("iam: invitation invalid or expired")

type TenantInvitation struct {
	TenantID  string
	Email     string
	Role      string
	InvitedBy string
	CodeHash  string
	ExpiresAt time.Time
}

type InvitationIdentity struct {
	GitHubID    int64
	GitHubLogin string
	AvatarURL   string
	Email       string
}

type InvitationJoinInput struct {
	CodeHash string
	Identity InvitationIdentity
	Now      time.Time
}

type ExistingInvitationJoinInput struct {
	CodeHash string
	UserID   string
	Now      time.Time
}

type InvitationJoinResult struct {
	UserID     string
	TenantID   string
	Role       string
	GlobalRole string
}
