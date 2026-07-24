package constants

const (
	DefaultAgentContextTokens                    = 8000
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
	// LoopCompactionSafetyRatio triggers compaction before the hard token ceiling,
	// leaving margin for the EstimateText heuristic error (<20%).
	LoopCompactionSafetyRatio = 0.8
)
