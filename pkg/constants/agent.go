package constants

const (
	DefaultAgentContextTokens     = 8000
	MinSystemPromptTokens         = 200
	DefaultInitHistoryWindow      = 20  // BuildInitMessages fallback window
	DefaultContextHistoryWindow   = 50  // BuildContextMessages fallback window
	MemoryBudgetRatio             = 0.3 // fraction of remaining budget reserved for memory context
	MaxRAGTopK                    = 20  // hard cap on RAG search top-k
	AgentToolTraceMaxRawJSONBytes = 256 * 1024
	AgentToolTraceMaxRawTextBytes = 64 * 1024

	// Lazy planning: K consecutive LLM rounds with no Output triggers Reflect→Plan.
	DefaultStuckThreshold = 3
	// MaxPlanSteps caps the number of steps a single Plan may contain.
	MaxPlanSteps = 10
	// DefaultStepMaxLLMSteps is the LLM budget for each sub-step ReAct execution.
	DefaultStepMaxLLMSteps = 3
)
