package domain

import "errors"

// Sentinel errors for knowledge workspace operations.
var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceConflict = errors.New("workspace already exists")
	ErrWorkspaceLinked   = errors.New("workspace is still linked to one or more agents")
)
