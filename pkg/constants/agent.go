package constants

const (
	DefaultAgentContextTokens   = 8000
	MinSystemPromptTokens       = 200
	DefaultInitHistoryWindow    = 20  // BuildInitMessages fallback window
	DefaultContextHistoryWindow = 50  // BuildContextMessages fallback window
	MemoryBudgetRatio           = 0.3 // fraction of remaining budget reserved for memory context
	MaxRAGTopK                  = 20  // hard cap on RAG search top-k
)
