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
	// MemoryOutboxPublishTimeout 限制单次 NATS Publish 的最长阻塞时间。
	// Publish 在 outbox 取出行事务提交后执行（事务内禁止网络 IO），
	// 该超时防止 NATS 慢/断连时 poll 循环卡死。
	MemoryOutboxPublishTimeout = 3 * time.Second
	// MemoryEnrichLLMTimeout 富化阶段 LLM 调用上限。
	MemoryEnrichLLMTimeout = 30 * time.Second
	// MemorySummaryLLMTimeout 摘要 LLM 调用上限（事务外执行）。
	MemorySummaryLLMTimeout = 60 * time.Second
	// PlatformModelValidationTimeout 平台模型 key 写时校验模型目录的单次 DB 预算
	// （PUT /admin/parameters 的 ValidateFn 内使用,避免目录查询挂死写路径）。
	PlatformModelValidationTimeout = 3 * time.Second
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

	MemoryBufferSizeLimit    = 8 * 1024         // flush if accumulated bytes >= 8KB
	MemoryBufferIdleTimeout  = 60 * time.Second // scanner: flush if no new message for 60s
	MemoryBufferAgeTimeout   = 5 * time.Minute  // scanner: flush if oldest message > 5min
	MemoryBufferScanInterval = 30 * time.Second // how often BufferScanner polls Redis
	// MemoryBufferScanTimeout is the per-scan operation budget. store.Scan can
	// hang on DNS/network (e.g. WSL2 lookup timeout reaches 30s); without a
	// budget the ticker cadence stalls behind the hung call. Must be <
	// MemoryBufferScanInterval so the scan can never outlive its ticker slot.
	MemoryBufferScanTimeout   = 20 * time.Second
	MemoryTenantWatchInterval = 60 * time.Second // how often TenantWatcher polls tenant list

	// MemoryBufferMinContentRunes is the minimum rune count of non-tool messages required to
	// trigger fact extraction. Flushes with less substantive content are discarded.
	// 50: filters pure ack sessions ("OK"×5≈10 runes) while allowing short factual statements
	// (e.g. "我喜欢Python"=8 chars passes when combined with other messages).
	MemoryBufferMinContentRunes = 50
)

// Memory Recall - controls retrieval behavior
const (
	// MemoryRecallTopK 是召回工具非法/缺失 limit 时的兜底条数。与 registry
	// memory.recall_top_k 的 Default=5 对齐（接入消费点后未配置 agent 走同一值，
	// 无 5→10 静默漂移）。
	MemoryRecallTopK     = 5    // max facts per recall
	MemoryRecallMinTopK  = 1    // clamp 下限
	MemoryRecallMaxTopK  = 20   // clamp 上限（工具 docstring 的合法 limit 上界）
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
	// MemoryMaxFactsPerExtraction 是单轮抽取最大事实数（prompt 软约束上限）。
	// 与写入硬上限 FactPerRoundPersistLimit=10 对齐：registry Default/VisualHint.Max
	// 与前端 Slider Max 同值，配置 N = 每轮入库 ≤N，避免"配了不生效"困惑。
	MemoryMaxFactsPerExtraction = 10
	MemoryMinFactLength         = 10   // min chars for a valid fact
	MemoryMaxFactLength         = 500  // max chars for a valid fact
	MemoryExtractLLMMaxTokens   = 4096 // JSON array of facts; 1024 truncates large conversations
	MemoryEnrichLLMTemperature  = 0.1  // 富化抽取任务温度（低温度换取字段语义稳定）
	// MemoryEnrichDefaultModel 富化模型 const 兜底（resolver 缺失/未配置时）。
	// 与 registry memory.enrich_model 的 Default 保持一致，避免双源漂移。
	MemoryEnrichDefaultModel = "qwen-turbo"
	// MemorySummaryDefaultModel 会话摘要模型 const 兜底（resolver 缺失/未配置时）。
	// 与 registry memory.summary_model 的 Default 保持一致；删除冷 config 后
	// 必须保持非空，摘要不得降级到富化模型。
	MemorySummaryDefaultModel = "qwen-plus"
	// MemoryMaxStructuredRetries 结构化 JSON 输出解析/校验失败后的带错重试次数
	// （共 MemoryMaxStructuredRetries+1 次尝试）。每次重试把具体错误位置/值/原因
	// 作为 system-role correction 丢回模型。provider 硬错误不消耗重试（fail-fast）。
	MemoryMaxStructuredRetries = 2
)

// Memory Supersede - supersede detection thresholds
const (
	MemorySupersedeCandidateMin     = 0.6  // min similarity to consider supersede
	MemorySupersedeCandidateMax     = 3    // max candidates to check per fact
	MemorySupersedeLLMCallsPerRun   = 20   // max LLM judgments per RunOnce pass
	MemoryInlineSupersedeFastThresh = 0.85 // similarity above which supersede is decided inline without LLM
	MemoryInlineSupersedeLLMPerFact = 3    // max inline LLM calls per extracted fact during extraction
	// MemorySupersedeJudgeMaxTokens 取代判定请求的 max_tokens 上限
	MemorySupersedeJudgeMaxTokens = 256
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
	MemoryProfileInterval      = 5 * time.Minute     // supersede worker poll interval
	MemoryGCInterval           = 24 * time.Hour      // garbage collection interval
	MemoryGCBatchSize          = 100                 // facts per GC batch
	MemoryGCQueueRetentionDays = 7                   // days to keep completed queue tasks
	MemoryDeletedRetention     = 30 * 24 * time.Hour // purge deleted after 30 days
	MemorySupersededRetention  = 90 * 24 * time.Hour // purge superseded after 90 days
)

// Memory Prompt templates — 各 memory worker 的默认提示词模板唯一权威源。平台参数
// memory.*_prompt 未配置时兜底；internal/parameters 的 prompt-defaults 白名单
// 按这些键下发全文给前端展示。占位符契约与自定义 prompt 一致（%s/%d）。
const (
	// MemoryExtractionDefaultPrompt 是事实抽取模板（memory.extraction_prompt
	// 未配置时兜底）。%s(userID)/%s(agentID)/%d(maxFacts) 占位符见
	// llm_extractor.go extractionSystemPrompt 消费点。
	MemoryExtractionDefaultPrompt = `你是一个长期记忆提取助手，负责从对话中提取关于用户（%s）的有价值事实，供 AI 助手（%s）在未来对话中使用。

提取规则（严格执行）：
- 只提取用户明确陈述、确认或展现的事实
- 不提取：用户的提问、问候语、AI 助手的回复内容、工具调用的输出
- 不提取泛化描述（如"用户提到了某件事"），只提取具体事实
- 优先精确性：「用户偏好在 VS Code 中使用暗色主题」优于「用户有主题偏好」
- 最多提取 %d 条事实；宁少勿滥，低价值事实直接忽略

fact_type 分类：
- preference：用户的喜好、偏好、习惯
- skill：用户掌握的技能或专业知识
- event：已发生的具体事件（过去时）
- state：用户当前的状态或处境
- relationship：用户与某人/某组织的关系
- other：不属于以上分类的陈述性事实

只输出 JSON 数组，不加任何说明或 markdown 标记：
[{"content":"...","importance":0.0-1.0,"fact_type":"...","confidence":0.0-1.0,"entities":["实体名"]}]`

	// MemoryEnrichDefaultPrompt 是对话富化模板（memory.enrich_prompt 未配置时
	// 兜底）。%s(role)/%s(content) 占位符见 enricher_prompt.go formatEnrichmentPrompt。
	MemoryEnrichDefaultPrompt = `分析以下对话消息，提取结构化元数据。

只输出符合以下格式的 JSON，不加任何说明或 markdown 标记：
{
  "entities": [{"name": "...", "type": "person|product|concept|location|org", "confidence": 0.0-1.0}],
  "importance": 0.0-1.0,
  "token_estimate": 数字,
  "keywords": ["关键词1", "关键词2"],
  "work_context": ["当前工作、任务或约束"],
  "personal_context": ["当前明确表达的个人偏好或状态"],
  "top_of_mind": ["当前最关注的事项"]
}

规则：
- importance 评分：0.9+ 决策/承诺；0.7-0.9 具体事实/偏好；0.3-0.7 一般上下文；<0.3 无实质内容（问候/感谢/简单确认）
- entities：只提取置信度 >= 0.6 的具名实体
- keywords：3-5 个最有检索价值的词语
- token_estimate：消息内容的 token 数近似值
- 三个 context 数组仅保留当前仍活跃、明确且必要的短句；每组最多 8 项，每项不超过 240 字；不要输出密码、令牌、密钥或原始整段消息

消息（角色：%s）：
%s`

	// MemorySummaryDefaultPrompt 是会话摘要模板（memory.summary_prompt 未配置
	// 时兜底）。%s(conversation) 占位符见 enricher_prompt.go formatSummaryPrompt。
	MemorySummaryDefaultPrompt = `简洁总结以下对话，保留关键决策、确认的事实和待办事项。要求简短但完整，使用中文。

对话内容：
%s`

	// MemoryHistorySummaryDefaultPrompt 是周期历史总结指令前缀（memory.
	// history_summary_prompt 未配置时兜底），见 history_summarizer.go 消费点。
	MemoryHistorySummaryDefaultPrompt = "Summarize this bounded period of user history. Preserve decisions, goals, preferences, and durable context; omit secrets and raw payloads.\n\n"

	// MemorySupersedeDefaultPrompt 是事实取代判定模板（memory.supersede_prompt
	// 未配置时兜底）。%s(oldFact)/%s(newFact) 占位符见 llm_superseder.go 消费点。
	MemorySupersedeDefaultPrompt = `判断新事实是否应该取代旧事实。

旧事实：%s
新事实：%s

判断标准：
- 如果新事实是对旧事实的更新、纠正或推翻，则应取代（supersedes: true）
- 如果两者描述不同方面或可以并存，则不取代（supersedes: false）
- 如果新事实只是旧事实的子集或更模糊的表达，则不取代

只输出 JSON，不加任何说明：
{"supersedes": true/false, "reason": "简短说明"}`
)
