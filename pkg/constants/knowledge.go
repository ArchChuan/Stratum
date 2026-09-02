package constants

import (
	"fmt"
	"regexp"
	"time"
)

const (
	// MaxUploadFileSize is the maximum file size for document uploads (100MB).
	MaxUploadFileSize = 100 * 1024 * 1024

	// CollectionPrefix is the unified prefix for all knowledge workspace collections.
	CollectionPrefix = "kb"

	// MaxChunksPerDocument caps chunk count per ingest job to bound memory
	// and processing time. Documents above this threshold are rejected up-front.
	MaxChunksPerDocument = 5000

	// MaxConcurrentIngest caps the number of concurrently running ingest jobs
	// across the process to protect embed backends and DB connection pool.
	MaxConcurrentIngest = 3

	// IngestQueueCapacity caps how many ingest jobs may be queued waiting for
	// a concurrency slot before the API returns 429.
	IngestQueueCapacity = 20

	// IngestStatusProcessing/Completed/Failed are enum values for
	// knowledge_docs.ingest_status.
	IngestStatusProcessing = "processing"
	IngestStatusCompleted  = "completed"
	IngestStatusFailed     = "failed"

	// RerankHTTPTimeout bounds a single external reranker call (10s).
	RerankHTTPTimeout = 10 * time.Second

	// NoAnswerReason* 是 RAG 无答案信号的固定枚举值（跨 context 单一事实源：
	// knowledge 与 agent 两侧共同消费，禁止各自拼字符串；值进入响应契约与
	// 指标 label，禁止动态拼接）。
	NoAnswerReasonNoSources            = "no_sources"
	NoAnswerReasonThresholdFiltered    = "threshold_filtered"
	NoAnswerReasonAccessRestricted     = "access_restricted"
	NoAnswerReasonInsufficientEvidence = "insufficient_evidence"
	NoAnswerReasonUnsupportedMode      = "unsupported_mode"

	// MaxMilvusFilterLen is the maximum byte length of a Milvus filter
	// expression (docs: filters with large `in` lists may fail). When a
	// doc-level whitelist exceeds this bound the vector leg degrades to
	// empty results while the keyword leg keeps filtering — never a
	// filterless full-collection search.
	MaxMilvusFilterLen = 60000
)

const (
	// RerankHTTPRetryMax is the retry budget for transient reranker failures.
	RerankHTTPRetryMax = 2
	// RerankMaxCandidates caps the documents sent to an external reranker.
	RerankMaxCandidates = 50
	// RerankWidenFactor widens the internal candidate pool before reranking:
	// TopK × RerankWidenFactor candidates are recalled, then narrowed to TopK.
	RerankWidenFactor = 4
	// MinRerankCandidates is the minimum pool size below which reranking is
	// skipped (a stable no-op) to avoid paying latency for tiny pools.
	MinRerankCandidates = 3
	// RerankDefaultTopN is the number of results requested from a reranker
	// when the caller does not specify RerankTopK.
	RerankDefaultTopN = 5
	// DefaultRAGTopK is the retrieval result count used when a query does
	// not specify TopK.
	DefaultRAGTopK = 5
	// MaxRerankTopK 是重排后最终条数硬上限（workspace rerank_top_k，0=跟随
	// TopK 合法）。外部 reranker 候选上限 RerankMaxCandidates(50) 不受此限。
	// 检索条数硬上限复用 agent.go 的 MaxRAGTopK（与 proto QueryRequest.topK
	// binding max=20 保持同步，修改须三处一致）。
	MaxRerankTopK = 20
	// MaxConcurrentWorkspaceSearch caps the number of workspaces searched
	// concurrently by the RAG fan-out, bounding embed/DB load per query.
	MaxConcurrentWorkspaceSearch = 3
	// MaxSourceSnippetRunes bounds the preview snippet attached to retrieval
	// sources for citation display. Full chunk content stays in the LLM
	// context; the snippet is display metadata only.
	MaxSourceSnippetRunes = 200
	// KnowledgeJudgeTimeout bounds a single sufficiency/faithfulness judge
	// call; 超时按 fail-closed 降级为"未判定"，不阻塞检索/评估主链路。
	KnowledgeJudgeTimeout = 15 * time.Second
	// KnowledgeJudgeMaxTokens caps judge 输出（固定 JSON 结构，1024 充足）。
	KnowledgeJudgeMaxTokens = 1024
	// KnowledgeJudgeMaxEvidenceRunes 截断喂给 judge 的聚合证据，防止
	// 多 workspace 拼接把上下文打爆（judge 只判断充分性，尾部内容
	// 截断对结论影响有限；与 factcheck 不截断的差异是本方案的成本控制）。
	KnowledgeJudgeMaxEvidenceRunes = 4000
	// RerankLLMTopN 是 builtin-score-v1 LLM 语义重排的精排候选上限（默认）：
	// 先按召回分数取前 N 条再 listwise 打分，池更小则全量精排。
	// 须 ≥ 常用 TopK（默认 5）；配置值 < 工作区 TopK 时最终条数受此硬上限约束。
	RerankLLMTopN = 10
	// RerankLLMMaxTokens caps 语义重排输出（固定 JSON 结构，1024 充足）。
	RerankLLMMaxTokens = 1024
	// RerankLLMTimeout bounds 单次 LLM 重排调用；超时按 fail-open 降级为分数排序。
	RerankLLMTimeout = 5 * time.Second
	// RerankLLMMaxDocRunes 截断喂给重排模型的候选正文（token 成本控制；只影响
	// 相关性打分上下文，不影响检索正文）。
	RerankLLMMaxDocRunes = 500
	// MaxRerankScoringInstructionsRunes / MaxJudgeScoringInstructionsRunes 是
	// workspace 评分指令附加段（rerank/judge scoring instructions）的 rune 上限。
	// 指令是可选的用户偏好段（空 = 用内置评分 prompt），上限防止用户文本打爆
	// LLM 上下文；前端 TextArea 同步展示该值
	// （web/src/constants KNOWLEDGE_MAX_SCORING_INSTRUCTIONS_RUNES，后端为权威）。
	MaxRerankScoringInstructionsRunes = 2000
	MaxJudgeScoringInstructionsRunes  = 2000
)

var milvusUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// SanitizeMilvusName 把任意字符串清洗为 Milvus 安全的 collection 名片段
// （仅字母数字下划线）。memory 与 knowledge 命名统一走此函数。
func SanitizeMilvusName(s string) string { return milvusUnsafe.ReplaceAllString(s, "_") }

// CollectionName generates the Milvus collection name for a knowledge workspace.
// workspaceID must be the stable workspace ID, not the mutable name.
// workspaceID (UUID v7) is globally unique, so tenantID is ignored; embedModel
// is encoded as a sanitized suffix so switching models isolates vector data.
// 空 model 时产出 kb_<workspaceID>_ 尾下划线形态（既非 legacy 名也非模型名）：
// 该形态被 cleaner 的 kb_<ws>_ 前缀匹配覆盖，RAG 查询回退仍可达 legacy 数据，
// 故无害；此行为已由 pkg/constants/agent_test.go pin。
func CollectionName(_, workspaceID, embedModel string) string {
	return fmt.Sprintf("%s_%s_%s", CollectionPrefix, SanitizeMilvusName(workspaceID), SanitizeMilvusName(embedModel))
}

// CollectionLegacyName 是无模型后缀的存量 collection 名（升级前数据）。
// 删除/清理路径与 legacy 回退读取统一经此拼写，避免两处命名漂移。
func CollectionLegacyName(_, workspaceID string) string {
	return fmt.Sprintf("%s_%s", CollectionPrefix, SanitizeMilvusName(workspaceID))
}
