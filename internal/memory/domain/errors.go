// Package domain holds memory entities, value objects, and sentinels.
package domain

import "errors"

// ErrEntryNotFound is returned when a memory entry lookup misses.
var ErrEntryNotFound = errors.New("memory entry not found")

// ErrSessionNotFound is returned when a session has no entries / summary.
var ErrSessionNotFound = errors.New("memory session not found")

// Memory v2 error sentinels.
var (
	// ErrFactNotFound is returned when a memory fact lookup misses.
	ErrFactNotFound = errors.New("memory fact not found")

	// ErrEntityNotFound is returned when a memory entity lookup misses.
	ErrEntityNotFound = errors.New("memory entity not found")

	// ErrAgentMemoryDisabled is returned when memory operations are attempted on an agent with memory disabled.
	ErrAgentMemoryDisabled = errors.New("agent memory disabled")

	// ErrScopeMismatch is returned when read/write scope validation fails.
	ErrScopeMismatch = errors.New("memory scope mismatch")

	// ErrFactQuotaExceeded is returned when a user exceeds their fact quota.
	ErrFactQuotaExceeded = errors.New("memory fact quota exceeded")

	// ErrFactAlreadyDeleted is returned when attempting to operate on a soft-deleted fact.
	ErrFactAlreadyDeleted = errors.New("memory fact already deleted")

	// ErrInvalidStatus is returned when an invalid status transition is attempted.
	ErrInvalidStatus = errors.New("invalid memory fact status")

	// ErrUserIDMismatch is returned when userID validation fails.
	ErrUserIDMismatch = errors.New("memory fact userID required")

	// ErrEmptyContent is returned when fact content is empty.
	ErrEmptyContent = errors.New("memory fact content cannot be empty")
)
