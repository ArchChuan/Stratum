package constants

import "time"

// Outbox pre-filter — lightweight rules applied before INSERT INTO memory_outbox.
// Only messages passing all rules are enqueued for embedding.
const (
	// MemoryOutboxMinRunes is the minimum rune count for a message to be recorded.
	// Short acks ("OK", "好", "继续") carry no semantic value.
	MemoryOutboxMinRunes = 10
	// MemoryOutboxMaxRunes is the maximum rune count stored in the outbox payload.
	// Content beyond this is truncated to limit noise in the embedding vector.
	MemoryOutboxMaxRunes = 2000
)

// Active short-term snapshot - Phase 1 bounded overwrite memory.
const (
	ActiveSnapshotTTL               = 24 * time.Hour
	ActiveSnapshotSectionMaxItems   = 8
	ActiveSnapshotItemMaxRunes      = 240
	ActiveSnapshotTotalMaxRunes     = 1200
	ActiveSnapshotSourceRefMaxRunes = 128
	ActiveSnapshotInjectionBudget   = 600
	MemoryInjectionCharBudget       = 1800
	ActiveSnapshotReadTimeout       = 500 * time.Millisecond
)

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
	EnricherSummaryTokenThreshold = 1000
	EnricherMaxInjectionTokens    = 500
	EnricherTopEntities           = 10
	EnricherSummaryMaxMessages    = 100 // max messages fetched per summary to avoid unbounded query
	MemoryLongTermTopK            = 5
)

// Pipeline runtime safeguards.
const (
	// MemoryFetchBackoffBase 是 JetStream Fetch 失败后的初始退避，避免 NATS 抖动时 worker 100% CPU 自旋。
	MemoryFetchBackoffBase = 200 * time.Millisecond
	// MemoryFetchBackoffMax 退避上限。
	MemoryFetchBackoffMax = 10 * time.Second
	// MemoryOutboxPublishTimeout 限制 NATS Publish 在 DB 事务内的最长阻塞时间，防止 NATS 慢/断连时事务持锁过久。
	MemoryOutboxPublishTimeout = 3 * time.Second
	// MemoryEnrichLLMTimeout 富化阶段 LLM 调用上限。
	MemoryEnrichLLMTimeout = 30 * time.Second
	// MemorySummaryLLMTimeout 摘要 LLM 调用上限（事务外执行）。
	MemorySummaryLLMTimeout = 60 * time.Second
)

// Memory Buffer - controls fact extraction pipeline batching
const (
	MemoryBufferFlushSize     = 5 // flush after K messages
	MemoryBufferFlushInterval = 2 * time.Minute
	// MemoryBufferKeyTTL is a sliding safety TTL on the Redis list key.
	// Prevents leaked keys when a conversation ends before K or T flush triggers
	// (e.g. tab closed, server restart). Reset on every push so slow but active
	// conversations are never evicted prematurely. 24 h matches industry-standard
	// session-buffer lifetimes (LangChain ConversationBufferMemory, Mem0).
	MemoryBufferKeyTTL = 24 * time.Hour

	MemoryBufferSizeLimit     = 8 * 1024         // flush if accumulated bytes >= 8KB
	MemoryBufferIdleTimeout   = 60 * time.Second // scanner: flush if no new message for 60s
	MemoryBufferAgeTimeout    = 5 * time.Minute  // scanner: flush if oldest message > 5min
	MemoryBufferScanInterval  = 30 * time.Second // how often BufferScanner polls Redis
	MemoryTenantWatchInterval = 60 * time.Second // how often TenantWatcher polls tenant list

	// MemoryBufferMinContentRunes is the minimum rune count of non-tool messages required to
	// trigger fact extraction. Flushes with less substantive content are discarded.
	// 50: filters pure ack sessions ("OK"×5≈10 runes) while allowing short factual statements
	// (e.g. "我喜欢Python"=8 chars passes when combined with other messages).
	MemoryBufferMinContentRunes = 50
)

// Memory Recall - controls retrieval behavior
const (
	MemoryRecallTopK     = 10   // max facts per recall
	MemoryFrecencyLambda = 0.05 // decay rate for frecency scoring
	MemoryRRFConstant    = 60   // RRF k parameter for hybrid retrieval fusion
)

// Memory GC - controls soft-delete cleanup
const (
	MemorySoftDeleteRetention = 30 * 24 * time.Hour // 30 days
)

// Dynamic long-term History policy. Values are centralized so workers,
// persistence, and injection cannot silently diverge.
const (
	HistoryAggregationMinEntries = 5
	HistoryAggregationBatchSize  = 50
	HistoryRecentMaxSegments     = 12
	HistoryEarlierMaxSegments    = 12
	HistoryRecentPromotionAge    = 90 * 24 * time.Hour
	HistoryEarlierPromotionAge   = 365 * 24 * time.Hour
	HistoryWorkerInterval        = 6 * time.Hour
	HistoryOperationTimeout      = 30 * time.Second
	HistoryReadTimeout           = 500 * time.Millisecond
	HistoryInjectionTopN         = 3
	HistoryInjectionCharBudget   = 500
	HistoryArchiveInactiveAge    = 180 * 24 * time.Hour
	HistoryProtectedImportance   = 0.8
	HistoryProtectedConfidence   = 0.8
)

// Memory Quota - per-user limits
const (
	MemoryFactQuotaPerUser = 5000 // max facts per user
)

// Memory Extraction - LLM extraction limits
const (
	MemoryMaxFactsPerExtraction = 20   // max facts extracted per message
	MemoryMinFactLength         = 10   // min chars for a valid fact
	MemoryMaxFactLength         = 500  // max chars for a valid fact
	MemoryExtractLLMMaxTokens   = 4096 // JSON array of facts; 1024 truncates large conversations
)

// Memory Entity - entity profile rebuild triggers
const (
	MemoryEntityRebuildFactDelta = 5                  // rebuild after N new facts
	MemoryEntityRebuildInterval  = 7 * 24 * time.Hour // or after 7 days
)

// Memory Supersede - supersede detection thresholds
const (
	MemorySupersedeCandidateMin     = 0.6  // min similarity to consider supersede
	MemorySupersedeCandidateMax     = 3    // max candidates to check per fact
	MemorySupersedeLLMCallsPerRun   = 20   // max LLM judgments per RunOnce pass
	MemoryInlineSupersedeFastThresh = 0.85 // similarity above which supersede is decided inline without LLM
	MemoryInlineSupersedeLLMPerFact = 3    // max inline LLM calls per extracted fact during extraction
)

// Facts quality filter — Phase 0 hardening
const (
	// FactConfidenceMin 写入前低置信过滤阈值；低于此值的事实在持久化前被丢弃
	FactConfidenceMin = 0.3
	// FactInjectionConfidenceMin 注入器读取阈值；只注入 confidence >= 此值的 active 事实
	FactInjectionConfidenceMin = 0.4
	// FactPerRoundPersistLimit 单轮抽取最多持久化的事实数；超出部分按质量排序后截断
	FactPerRoundPersistLimit = 10
	// FactInjectionTopN 注入器每次取的最大事实数
	FactInjectionTopN = 8
	// FactInjectionCharBudget 注入器事实段的最大字符数；超出时截断
	FactInjectionCharBudget = 1200
	// FactInjectionTimeout 注入器读取超时；超时降级为空而不是错误
	FactInjectionTimeout = 3 * time.Second
)

// Memory Workers - background processing intervals and batch sizes
const (
	MemoryExtractionBatchSize  = 10                  // facts per extraction queue poll
	MemoryExtractionLease      = 5 * time.Minute     // reclaim processing tasks after worker loss
	MemorySupersedeBatchSize   = 20                  // facts per supersede judgment batch
	MemoryEmbedInterval        = 10 * time.Second    // embed worker poll interval
	MemoryEmbedBatchSize       = 50                  // facts per embed batch
	MemoryProfileInterval      = 5 * time.Minute     // profile rebuild poll interval
	MemoryProfileBatchSize     = 10                  // entities per profile rebuild batch
	MemoryGCInterval           = 24 * time.Hour      // garbage collection interval
	MemoryGCBatchSize          = 100                 // facts per GC batch
	MemoryGCQueueRetentionDays = 7                   // days to keep completed queue tasks
	MemoryDeletedRetention     = 30 * 24 * time.Hour // purge deleted after 30 days
	MemorySupersededRetention  = 90 * 24 * time.Hour // purge superseded after 90 days
)
