package constants

import "time"

const (
	DefaultAgentContextTokens                    = 8000
	DefaultSystemAssistantModel                  = "glm-5.2"
	MinSystemPromptTokens                        = 200
	DefaultInitHistoryWindow                     = 20  // BuildInitMessages fallback window
	DefaultContextHistoryWindow                  = 50  // BuildContextMessages fallback window
	MemoryBudgetRatio                            = 0.3 // fraction of remaining budget reserved for memory context
	MaxRAGTopK                                   = 20  // hard cap on RAG search top-k
	AgentToolTraceMaxRawJSONBytes                = 256 * 1024
	AgentToolTraceMaxRawTextBytes                = 64 * 1024
	SystemAssistantToolMaxJSONBytes              = 32 * 1024
	SystemAssistantQueryMaxRunes                 = 500
	SystemAssistantAreasMaxCount                 = 5
	SystemAssistantCitationMaxCount              = 5
	SystemAssistantDiagnosticFactsMaxCount       = 100
	SystemAssistantDiagnosticGapsMaxCount        = 20
	SystemAssistantDiagnosticAreaResultsMaxCount = 5
	SystemAssistantEvidenceFieldMaxRunes         = 500

	// Lazy planning: K consecutive LLM rounds with no Output triggers Reflect→Plan.
	DefaultStuckThreshold = 3
	// MaxPlanSteps caps the number of steps a single Plan may contain.
	MaxPlanSteps = 10
	// DefaultStepMaxLLMSteps is the LLM budget for each sub-step ReAct execution.
	DefaultStepMaxLLMSteps = 3

	DefaultPlanMaxNodes           = 10
	DefaultPlanMaxRevisions       = 20
	DefaultPlanMaxAttemptsPerNode = 3
	DefaultPlanMaxConcurrentNodes = 4

	// LoopCompactionRecentGroups is the number of most-recent message groups
	// (a group = one assistant turn plus its paired tool results) kept verbatim
	// during in-loop compaction. Older groups are summarized or dropped.
	LoopCompactionRecentGroups = 3
	// DefaultCompactionCooldown 是一次执行内压缩触发后的冷却窗口（Spec 第 4 节，
	// 建议默认 10s，实现时按压测验证）。registry 参数 agent.compaction_cooldown_sec
	// 覆盖它（0 = 本常量）。
	DefaultCompactionCooldown = 10 * time.Second
	// LoopCompactionSafetyRatio triggers compaction before the hard token ceiling,
	// leaving margin for the EstimateText heuristic error (<20%).
	LoopCompactionSafetyRatio = 0.8

	// CompactionBudgetTotal 是压缩路径一次执行的总体时间预算（Spec 第 4 节）：
	// 按 剩余/剩余尝试数 分摊为逐次独立的时间片，链内所有尝试合计不放大
	// 用户可感知时延。
	CompactionBudgetTotal = 5 * time.Second
	// CompactionMaxCandidates 是压缩路径 fallback 候选模型数量上限（不含主模型）。
	CompactionMaxCandidates = 2
	// CompactionMinSlice 是单次尝试时间片下限：剩余预算耗尽（≤0）时的兜底
	// slice，保证每次尝试仍有最小执行窗口。
	CompactionMinSlice = 1 * time.Millisecond

	// DefaultContextWindowRatio is the fraction of a model's context window
	// used as the agent's MaxContextTokens when the user does not set one explicitly.
	DefaultContextWindowRatio = 0.85

	// MaxContextWindowTokens is the hard ceiling of a resolved window (Spec 第 1 节),
	// replacing the model-independent DefaultAgentContextTokensCeiling(32768).
	MaxContextWindowTokens = 1_048_576
	// MinContextWindowTokens is the lower bound an explicit MaxContextTokens
	// is clamped to when the model window is known.
	MinContextWindowTokens = 2_000

	// DefaultFixedHeadRatio 是 system+memory 的预算配额比例（Spec 第 2 节）。
	DefaultFixedHeadRatio = 0.2
	// DefaultToolsBudgetRatio 是工具定义的预算配额比例（Spec 第 2 节）。
	DefaultToolsBudgetRatio = 0.2
	// DefaultOutputReserveTokens 是主模型输出预留的保守默认（无显式 max_tokens
	// 且 vendor 表未知时的兜底）。
	DefaultOutputReserveTokens = 4_096

	// ---- adaptive compaction thresholds (Context Phase 3) ----

	// CompactionRecentGroupsSmall is the number of recent message groups kept
	// verbatim when the context window is below 16K tokens.
	CompactionRecentGroupsSmall = 2
	// CompactionRecentGroupsLarge is the number of recent message groups kept
	// verbatim when the context window exceeds 64K tokens.
	CompactionRecentGroupsLarge = 5

	// CompactionRecentGroupsThresholdSmall is the window size below which the
	// small recent-groups count applies.
	CompactionRecentGroupsThresholdSmall = 16_000
	// CompactionRecentGroupsThresholdLarge is the window size above which the
	// large recent-groups count applies.
	CompactionRecentGroupsThresholdLarge = 64_000

	// CompactionSummaryReserveRatio is the fraction of the context budget
	// reserved for the history compaction summary (5%).
	CompactionSummaryReserveRatio = 0.05
	// CompactionSummaryReserveFloor is the minimum summary reserve in tokens.
	CompactionSummaryReserveFloor = 200

	// CompactionMaxTokensRatio is the fraction of MaxContextTokens used to cap
	// the compaction LLM's output (10%). See DynamicCompactionMaxTokens.
	CompactionMaxTokensRatio = 0.10
	// CompactionMaxTokensFloor is the minimum output budget for compaction.
	CompactionMaxTokensFloor = 400
	// CompactionMaxTokensCeiling caps the compaction output regardless of window.
	CompactionMaxTokensCeiling = 800

	// MaxFingerprintPayloadBytes caps the serialised ExecutionFingerprint
	// before it is truncated in span attributes (F1).
	MaxFingerprintPayloadBytes = 4096

	// OperationApprovalTTL bounds an approved operation proposal before its
	// single-use replay expires. Lives here (not in the agent application
	// package) because the tenant schema repository must parameterise the
	// Approve UPDATE's expires_at interval without importing application.
	OperationApprovalTTL = 24 * time.Hour
	// TokenCorrectionAlpha is the EMA smoothing factor for the compaction
	// token-correction loop: correction = α·ratio + (1−α)·correction.
	TokenCorrectionAlpha = 0.1
	// TokenCorrectionMin/Max clamp the correction factor. 0.5 halves the
	// effective budget (compacts earlier); 2.0 doubles it (compacts later).
	TokenCorrectionMin = 0.5
	TokenCorrectionMax = 2.0

	// MinimalRetryReserveBytes 是最终请求 context_length_exceeded 降级最小
	// 请求（Spec D4）的字节预算余量：len() 字节数是 token 的保守上界
	// （CJK 每字符 3 字节），从窗口扣除该余量保证最小请求必然小于原请求。
	MinimalRetryReserveBytes = 64
)

// DynamicRecentGroups returns the number of recent message groups to preserve
// during in-loop compaction, scaled by the agent's MaxContextTokens.
//
//	< 16K → 2 groups (tight budget)
//	16K–64K → 3 groups (default)
//	> 64K → 5 groups (ample budget)
func DynamicRecentGroups(maxContextTokens int) int {
	if maxContextTokens <= 0 {
		return LoopCompactionRecentGroups
	}
	switch {
	case maxContextTokens < CompactionRecentGroupsThresholdSmall:
		return CompactionRecentGroupsSmall
	case maxContextTokens > CompactionRecentGroupsThresholdLarge:
		return CompactionRecentGroupsLarge
	default:
		return LoopCompactionRecentGroups
	}
}

// DynamicSummaryReserve returns the token budget reserved for a conversation
// history compaction summary. It scales as 5% of budget with a 200-token floor,
// replacing the fixed budget/4 previously used.
func DynamicSummaryReserve(budget int) int {
	reserve := int(float64(budget) * CompactionSummaryReserveRatio)
	if reserve < CompactionSummaryReserveFloor {
		return CompactionSummaryReserveFloor
	}
	return reserve
}

// DynamicCompactionMaxTokens returns the max output tokens for a compaction
// LLM call. It scales as ~10% of the agent's MaxContextTokens, bounded to
// [CompactionMaxTokensFloor, CompactionMaxTokensCeiling].
//
//	4K → 400 (floor)
//	8K → 800 (ceiling)
//	32K → 800 (ceiling)
func DynamicCompactionMaxTokens(maxContextTokens int) int {
	derived := int(float64(maxContextTokens) * CompactionMaxTokensRatio)
	if derived < CompactionMaxTokensFloor {
		return CompactionMaxTokensFloor
	}
	if derived > CompactionMaxTokensCeiling {
		return CompactionMaxTokensCeiling
	}
	return derived
}
