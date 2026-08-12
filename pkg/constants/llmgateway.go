package constants

// LLM 网关模型 fallback 链预算。
const (
	// MaxModelFallbackCandidates 是 fallback 候选模型数量上限（不含主模型）。
	// 链总长上限 = 1（主模型）+ 该值；主模型失败时允许 1 次立即重试。
	MaxModelFallbackCandidates = 3
)

// ReasoningEffort 思考强度档位 → Anthropic extended_thinking budget_tokens。
// OpenAI 兼容协议直传 reasoning_effort 字符串本身，无需预算映射。
const (
	// ReasoningEffortLow/Medium/High 是思考强度的合法枚举值。空串表示 unset
	// （与 Temperature 0=unset 同构）。非法值沿链路透传到严格端点会 400
	// （永久错误中止整条 fallback 链），所有入口必须按此枚举校验。
	ReasoningEffortLow    = "low"
	ReasoningEffortMedium = "medium"
	ReasoningEffortHigh   = "high"

	ReasoningEffortBudgetLow    = 2_000
	ReasoningEffortBudgetMedium = 8_000
	ReasoningEffortBudgetHigh   = 20_000
	// ReasoningEffortMaxTokensReserve 是启用 thinking 时为推理内容预留的输出
	// token：max_tokens 不足 budget+reserve 时抬升，否则严格端点 400。
	ReasoningEffortMaxTokensReserve = 4_096
)

// IsValidReasoningEffort 判断档位是否在 low/medium/high 枚举内。空串（unset）
// 视为合法。domain 与 evaluation 优化器共用，避免枚举值分散。
func IsValidReasoningEffort(effort string) bool {
	switch effort {
	case "", ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh:
		return true
	}
	return false
}
