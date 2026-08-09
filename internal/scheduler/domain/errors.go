package domain

import "errors"

// Scheduled-task domain errors. Infrastructure translates storage failures
// into these; the error middleware maps them to HTTP statuses.
var (
	ErrScheduledTaskNotFound     = errors.New("scheduled task not found")
	ErrScheduledTaskForbidden    = errors.New("scheduled task access forbidden")
	ErrScheduledTaskInvalidInput = errors.New("invalid scheduled task input")
	ErrScheduledTaskInvalidCron  = errors.New("invalid cron expression")
)
