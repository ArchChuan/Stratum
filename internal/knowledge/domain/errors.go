package domain

import "errors"

// Sentinel errors for knowledge workspace operations.
var (
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrWorkspaceConflict  = errors.New("workspace already exists")
	ErrWorkspaceLinked    = errors.New("workspace is still linked to one or more agents")
	ErrDuplicateDocument  = errors.New("document already exists in this workspace")
	ErrChunkLimitExceeded = errors.New("document exceeds maximum chunk count; please split into smaller files")
	ErrIngestQueueFull    = errors.New("ingest queue is full; please retry shortly")
)
