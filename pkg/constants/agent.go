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
	// MaxAgentResultSources caps the citation sources attached to an agent
	// result for chat display (deduplicated by chunk ID, newest wins).
	MaxAgentResultSources = 10

	// Lazy planning: K consecutive LLM rounds with no Output triggers Reflect→Plan.
	DefaultStuckThreshold = 3
	// MaxPlanSteps caps the number of steps a single Plan may contain.
	MaxPlanSteps = 10
	// DefaultStepMaxLLMSteps is the LLM budget for each sub-step ReAct execution.
	DefaultStepMaxLLMSteps = 3

	// AgentToolStopLossThreshold 是同一工具连续（同错指纹）失败触发止损的
	// 阈值：达阈值后该工具不再执行，模型收到观察后换路。
	AgentToolStopLossThreshold = 3
	// AgentDegradeReasonStopLossPrefix 是止损降级原因枚举的前缀。固定枚举
	// （"tool_stop_loss:<tool>"），禁止拼接 err.Error()——错误正文含
	// plan_id/revision 等内部标识，透出前端违反「错误不落下游错误正文」。
	AgentDegradeReasonStopLossPrefix = "tool_stop_loss:"
	// AgentToolStopLossObservation 是止损后返回给模型的观察文案（%s = 工具名）。
	AgentToolStopLossObservation = "Tool %s has been stopped after repeated validation failures. Use an alternative approach."
	// AgentFinalAnswerInstruction 是 ReAct 达步数上限强制收尾的用户指令。
	AgentFinalAnswerInstruction = "You have reached the maximum reasoning steps. Based on your analysis and tool results so far, provide your final answer now. Do not call any tools."
	// AgentDegradedFinalAnswerInstruction 是降级执行的强制收尾指令：只基于已
	// 确认事实回答，禁止声称完成了未验证的操作。
	AgentDegradedFinalAnswerInstruction = "You have reached the maximum reasoning steps. Based on confirmed facts only, provide your final answer now. Do not claim operations that were not verified successfully. Do not call any tools."

	// AgentFactCheckMaxClaims 是幻觉校验最多拆分的 claim 数（控成本，超出截断）。
	// 一次 judge 调用批量判定全部 claim，claim 过多会拉长单次生成 → 超时降级；
	// 4 条 + 30s 预算在 LLM-as-Judge 常见输出量下留足余量。
	AgentFactCheckMaxClaims = 4
	// AgentFactCheckTopK 是幻觉校验每个 claim 的 RAG 检索 topK。
	AgentFactCheckTopK = 4
	// AgentFactCheckTimeout 是单次幻觉校验的整体时间预算；judge/检索失败或超时
	// 降级为「不校验」（nil），不阻塞 agent 执行。
	AgentFactCheckTimeout = 30 * time.Second
	// AgentFactCheckJudgeModel 是幻觉校验 LLM-as-Judge 的默认模型（独立接线，
	// 不得静默回落 evaluation.judge.model）。
	AgentFactCheckJudgeModel = "qwen-turbo"
	// AgentFactCheckJudgeMaxTokens 是 judge 单次输出预算。1024 会被批量
	// claim 判定截断（finish_reason=length → JSON 半截 → 解析失败降级）；
	// 2048 覆盖 4 claims 的完整 verdict JSON 输出。
	AgentFactCheckJudgeMaxTokens = 2048

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
	// ContextSafetyReserveRatio 是执行级预算账本的安全余量默认比例（Spec 第 2 节
	// usable = window − safetyReserve − outputReserve）。独立于
	// LoopCompactionSafetyRatio（"80% 满即压缩"的触发语义）：扣减 80% 会让
	// 默认配置下 usable 归零（0.8×window + outputReserve > window），system 模板
	// 塞满 headCap、memory 注入与压缩全部失效。默认 20% 余量在窗口利用率与
	// 自修正兜底间取中。
	ContextSafetyReserveRatio = 0.2

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
	// MaxPendingApprovalsPerActor caps how many unexpired pending approvals a
	// single user may hold (D4 放宽后 member 可触发审批，须防存储 DoS）。
	MaxPendingApprovalsPerActor = 50
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
