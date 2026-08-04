// Package domain defines the collaboration bounded context types.
package domain

import "errors"

// Sentinel errors for the collaboration bounded context.
var (
	// ErrCollabNotFound hides existence from non-authorized actors.
	ErrCollabNotFound = errors.New("collab: not found")
	// ErrCollabForbidden is returned when the actor lacks control rights.
	ErrCollabForbidden = errors.New("collab: forbidden")
	// ErrCollabInvalidTransition is returned on illegal state machine moves.
	ErrCollabInvalidTransition = errors.New("collab: invalid transition")
	// ErrCollabInvalidInput is returned when a create/update payload fails validation.
	ErrCollabInvalidInput = errors.New("collab: invalid input")
	// ErrCollabConflict is returned on optimistic-lock conflicts on shared context.
	ErrCollabConflict = errors.New("collab: version conflict")
)
