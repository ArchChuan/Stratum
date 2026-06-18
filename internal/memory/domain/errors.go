// Package domain holds memory entities, value objects, and sentinels.
package domain

import "errors"

// ErrEntryNotFound is returned when a memory entry lookup misses.
var ErrEntryNotFound = errors.New("memory entry not found")

// ErrSessionNotFound is returned when a session has no entries / summary.
var ErrSessionNotFound = errors.New("memory session not found")
