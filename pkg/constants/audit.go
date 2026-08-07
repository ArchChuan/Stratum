package constants

import "time"

const (
	// AuditBufferSize is the capacity of the buffered channel that decouples
	// audit event producers from the batch writer.
	AuditBufferSize = 4096
	// AuditFlushInterval is the max wait before flushing a partial batch.
	AuditFlushInterval = 100 // milliseconds
	// AuditBatchSize is the number of events accumulated before a forced flush.
	AuditBatchSize = 100
	// AuditFlushTimeout bounds a single batch insert (30s).
	AuditFlushTimeout = 30 * time.Second
	// MaxAuditBatchPending caps the in-memory batch retained for retry. While
	// the store is down the writer must keep retrying (events are only cleared
	// after a successful insert), which would otherwise grow the batch without
	// bound and OOM the process. The cap bounds memory at the cost of dropping
	// the oldest events: the newest events matter most during an outage.
	MaxAuditBatchPending = 10000
	// AuditRetentionDays is how long audit events are kept before cleanup.
	AuditRetentionDays = 180
	// AuditCleanupInterval is how often the retention cleanup worker runs.
	AuditCleanupInterval = 24 // hours
)
