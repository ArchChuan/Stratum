package domain

import "errors"

// Sentinel errors for knowledge workspace operations.
var (
	ErrWorkspaceNotFound  = errors.New("workspace not found")
	ErrWorkspaceConflict  = errors.New("workspace already exists")
	ErrWorkspaceLinked    = errors.New("workspace is still linked to one or more agents")
	ErrDuplicateDocument  = errors.New("document already exists in this workspace")
	ErrDocumentNotFound   = errors.New("document not found")
	ErrDocumentProcessing = errors.New("processing document cannot be deleted")
	ErrChunkLimitExceeded = errors.New("document exceeds maximum chunk count; please split into smaller files")
	ErrIngestQueueFull    = errors.New("ingest queue is full; please retry shortly")
	ErrForbidden          = errors.New("resource ownership forbidden")
	// ErrEditorNotEligible rejects a granted editor who does not hold role
	// admin or owner at write time (fail closed, prevents forgery).
	ErrEditorNotEligible = errors.New("editor must hold admin or owner role")
)
