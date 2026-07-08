package application

import (
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

func estimateMessagesTokens(msgs []port.LLMMessage) int {
	total := 0
	for _, m := range msgs {
		total += tokenutil.EstimateText(m.Role) + tokenutil.EstimateText(m.Content) + 4
	}
	return total
}

// truncateToTokenBudget 截断字符串使其估算 token 不超过 budget。
// 使用 UTF-8 字节边界截断，避免切断多字节字符。
func truncateToTokenBudget(s string, budget int) string {
	maxBytes := budget * 3
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// BuildContextMessages assembles the message slice for an LLM call with token-aware trimming.
// Priority (high→low): currentInput > systemPromptBase (min 200t) > memoryCtx (≤30% remaining) > history (oldest dropped first).
func BuildContextMessages(
	systemPromptBase string,
	memoryCtx string,
	history []*ChatMessage,
	currentInput string,
	maxTokens int,
	historyWindow int,
) []port.LLMMessage {
	if historyWindow <= 0 {
		historyWindow = constants.DefaultContextHistoryWindow
	}
	if len(history) > historyWindow {
		history = history[len(history)-historyWindow:]
	}

	budget := maxTokens

	// 1. Reserve budget for current input (highest priority)
	budget -= tokenutil.EstimateText(currentInput)
	if budget <= 0 {
		return []port.LLMMessage{{Role: "user", Content: currentInput}}
	}

	// 2. System prompt — guarantee MinSystemPromptTokens, truncate if over budget
	sysTokens := tokenutil.EstimateText(systemPromptBase)
	sysReserve := max(sysTokens, constants.MinSystemPromptTokens)
	sysReserve = min(sysReserve, budget)
	if sysTokens > sysReserve {
		systemPromptBase = truncateToTokenBudget(systemPromptBase, sysReserve)
	}
	budget -= sysReserve

	// 3. Memory context — max 30% of remaining budget
	if memoryCtx != "" {
		memBudget := int(float64(budget) * constants.MemoryBudgetRatio)
		memTokens := tokenutil.EstimateText(memoryCtx)
		if memTokens > memBudget {
			memoryCtx = truncateToTokenBudget(memoryCtx, memBudget)
			memTokens = memBudget
		}
		budget -= memTokens
	}

	// 4. Convert history to LLM messages and trim oldest to fit remaining budget
	histMsgs := make([]port.LLMMessage, 0, len(history))
	for _, m := range history {
		histMsgs = append(histMsgs, port.LLMMessage{Role: m.Role, Content: m.Content})
	}
	for len(histMsgs) > 0 && estimateMessagesTokens(histMsgs) > budget {
		histMsgs = histMsgs[1:]
	}

	// 5. Compose final system prompt
	systemFull := systemPromptBase
	if memoryCtx != "" {
		systemFull = systemPromptBase + "\n\n" + memoryCtx
	}

	msgs := make([]port.LLMMessage, 0, len(histMsgs)+2)
	if systemFull != "" {
		msgs = append(msgs, port.LLMMessage{Role: "system", Content: systemFull})
	}
	msgs = append(msgs, histMsgs...)
	msgs = append(msgs, port.LLMMessage{Role: "user", Content: currentInput})
	return msgs
}
