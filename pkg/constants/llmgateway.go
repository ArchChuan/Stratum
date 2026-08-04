package constants

// LLM 网关模型 fallback 链预算。
const (
	// MaxModelFallbackCandidates 是 fallback 候选模型数量上限（不含主模型）。
	// 链总长上限 = 1（主模型）+ 该值；主模型失败时允许 1 次立即重试。
	MaxModelFallbackCandidates = 3
)
