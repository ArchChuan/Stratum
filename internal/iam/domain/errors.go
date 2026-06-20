package domain

import "errors"

// Sentinel errors for IAM tenant operations.
var (
	ErrMemberNotFound      = errors.New("iam: member not found")
	ErrTenantNotFound      = errors.New("iam: tenant not found")
	ErrDefaultTenantDelete = errors.New("iam: default tenant cannot be deleted")
)
