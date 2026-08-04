package application

import "time"

// Collab behavior constants, shared across the collab application layer.
const (
	// CollabTaskLease is the claim lease duration for a single task step.
	CollabTaskLease = 5 * time.Minute
	// MaxCollabParticipants caps the participant list of one plan.
	MaxCollabParticipants = 16
	// TaskDescriptionMaxRunes caps the plan task description length.
	TaskDescriptionMaxRunes = 2000
	// StepErrorMaxRunes caps error text persisted on a failed step.
	StepErrorMaxRunes = 2000
	// DefaultStepMaxRetries is the retry budget per generated step.
	DefaultStepMaxRetries = 3
	// SharedContextMaxBytes caps a single step output stored in shared context.
	// Worst-case context size stays bounded: MaxCollabParticipants × cap.
	SharedContextMaxBytes = 64 * 1024
	// sharedContextUpdateMaxRetries bounds optimistic-lock retries when
	// aggregating step output into the shared context.
	sharedContextUpdateMaxRetries = 3
)
