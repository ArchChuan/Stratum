package constants

import "time"

// Outbox poller
const (
	MemoryOutboxPollInterval = 1 * time.Second
	MemoryOutboxBatchSize    = 50
)

// JetStream
const (
	MemoryStreamMaxAge    = 72 * time.Hour
	MemoryDLQMaxAge       = 168 * time.Hour
	MemoryRawStream       = "MEMORY_RAW"
	MemoryEnrichedStream  = "MEMORY_ENRICHED"
	MemoryDLQStream       = "MEMORY_DLQ"
	MemoryRawSubject      = "memory.raw"
	MemoryEnrichedSubject = "memory.enriched"
	MemoryDLQSubject      = "memory.dlq"
)

// Embedder
const (
	EmbedderConsumerName = "embed-worker"
	EmbedderAckWait      = 30 * time.Second
	EmbedderMaxDeliver   = 5
	EmbedderWorkerCount  = 2
)

// Enricher
const (
	EnricherConsumerName          = "enrich-worker"
	EnricherAckWait               = 60 * time.Second
	EnricherMaxDeliver            = 5
	EnricherWorkerCount           = 1
	EnricherSummaryTokenThreshold = 4096
	EnricherMaxInjectionTokens    = 500
	EnricherTopEntities           = 10
)
