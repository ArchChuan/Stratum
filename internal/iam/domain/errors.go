package domain

import "errors"

// Sentinel errors for IAM tenant operations.
var (
	ErrMemberNotFound      = errors.New("iam: member not found")
	ErrTenantNotFound      = errors.New("iam: tenant not found")
	ErrDefaultTenantDelete = errors.New("iam: default tenant cannot be deleted")
	ErrUsernameTaken       = errors.New("iam: username already taken")
	ErrUserNotFound        = errors.New("iam: user not found")
	ErrForbidden           = errors.New("iam: forbidden")
	ErrUserRepoUnavailable = errors.New("iam: user repo unavailable")
)
