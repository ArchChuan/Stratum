package constants

const (
	// AuditBufferSize is the capacity of the buffered channel that decouples
	// audit event producers from the batch writer.
	AuditBufferSize = 4096
	// AuditFlushInterval is the max wait before flushing a partial batch.
	AuditFlushInterval = 100 // milliseconds
	// AuditBatchSize is the number of events accumulated before a forced flush.
	AuditBatchSize = 100
	// AuditRetentionDays is how long audit events are kept before cleanup.
	AuditRetentionDays = 180
	// AuditCleanupInterval is how often the retention cleanup worker runs.
	AuditCleanupInterval = 24 // hours
)
