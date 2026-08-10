package application

// Scheduler-scoped constants shared across this package's files.
// Cross-package bounds (max name length, min fire interval, batch size)
// live in pkg/constants/scheduler.go — the domain layer references them.
const (
	// idempotencyKeyFormat is schedule-id@next_fire_at (RFC3339 UTC). The
	// next_fire_at must be the DB row's current value, identical across
	// worker instances, so concurrent polls produce the same key.
	idempotencyKeyFormat     = "%s@%s"
	schedulerCreatedByPrefix = "scheduler:"
	scheduleTypeCron         = "cron"
)
