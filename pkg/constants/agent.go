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

	DefaultPlanMaxNodes           = 10
	DefaultPlanMaxRevisions       = 20
	DefaultPlanMaxAttemptsPerNode = 3
	DefaultPlanMaxConcurrentNodes = 4

	// LoopCompactionRecentGroups is the number of most-recent message groups
	// (a group = one assistant turn plus its paired tool results) kept verbatim
	// during in-loop compaction. Older groups are summarized or dropped.
	LoopCompactionRecentGroups = 3
	// LoopCompactionSafetyRatio triggers compaction before the hard token ceiling,
	// leaving margin for the EstimateText heuristic error (<20%).
	LoopCompactionSafetyRatio = 0.8

	// DefaultAgentContextTokensCeiling caps auto-derived MaxContextTokens to avoid
	// 128K+ models exhausting memory with full-window context budgets.
	DefaultAgentContextTokensCeiling = 32768

	// DefaultContextWindowRatio is the fraction of a model's context window
	// used as the agent's MaxContextTokens when the user does not set one explicitly.
	DefaultContextWindowRatio = 0.85

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
