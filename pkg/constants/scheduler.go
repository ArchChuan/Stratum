package constants

import "time"

// Scheduled-task behavior bounds. The domain layer references these, so they
// live in pkg/constants (not application defaults) to keep domain on
// stdlib + constants only.
const (
	// SchedulerPollInterval is the scheduler worker's per-tenant poll cadence.
	SchedulerPollInterval = 30 * time.Second
	// MaxSchedulerDueBatchSize caps the due tasks fired per tenant per poll.
	MaxSchedulerDueBatchSize = 100
	// MaxScheduledTaskNameRunes bounds scheduled task display names.
	MaxScheduledTaskNameRunes = 64
	// MaxCronExprLen bounds the stored cron expression.
	MaxCronExprLen = 128
	// MinScheduledTaskFireInterval rejects cron expressions that fire more
	// often than once per minute: fire-storm guard and keeps the RFC3339
	// second-precision idempotency key collision-free.
	MinScheduledTaskFireInterval = 60 * time.Second
	// SchedulerCronBrokenCooldown moves a task with an unparseable cron
	// expression out of the due set for one day instead of erroring every
	// poll; the admin sees last_error_message and fixes the expression.
	SchedulerCronBrokenCooldown = 24 * time.Hour
)
