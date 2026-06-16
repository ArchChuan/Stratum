package agent

import (
	"github.com/byteBuilderX/stratum/internal/capgateway"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

func estimateTokens(s string) int {
	return len([]rune(s)) / 3
}

func estimateMessagesTokens(msgs []capgateway.LLMMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Content)
	}
	return total
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
) []capgateway.LLMMessage {
	if historyWindow <= 0 {
		historyWindow = constants.DefaultContextHistoryWindow
	}
	if len(history) > historyWindow {
		history = history[len(history)-historyWindow:]
	}

	budget := maxTokens

	// 1. Reserve budget for current input (highest priority)
	budget -= estimateTokens(currentInput)
	if budget <= 0 {
		return []capgateway.LLMMessage{{Role: "user", Content: currentInput}}
	}

	// 2. System prompt — guarantee MinSystemPromptTokens, truncate if over budget
	sysTokens := estimateTokens(systemPromptBase)
	sysReserve := max(sysTokens, constants.MinSystemPromptTokens)
	sysReserve = min(sysReserve, budget)
	if sysTokens > sysReserve {
		runes := []rune(systemPromptBase)
		maxRunes := sysReserve * 3
		if maxRunes < len(runes) {
			systemPromptBase = string(runes[:maxRunes])
		}
	}
	budget -= sysReserve

	// 3. Memory context — max 30% of remaining budget
	if memoryCtx != "" {
		memBudget := int(float64(budget) * constants.MemoryBudgetRatio)
		memTokens := estimateTokens(memoryCtx)
		if memTokens > memBudget {
			runes := []rune(memoryCtx)
			maxRunes := memBudget * 3
			if maxRunes < len(runes) {
				memoryCtx = string(runes[:maxRunes])
			}
			memTokens = memBudget
		}
		budget -= memTokens
	}

	// 4. Convert history to LLM messages and trim oldest to fit remaining budget
	histMsgs := make([]capgateway.LLMMessage, 0, len(history))
	for _, m := range history {
		role := m.Role
		if role == "agent" {
			role = "assistant"
		}
		histMsgs = append(histMsgs, capgateway.LLMMessage{Role: role, Content: m.Content})
	}
	for len(histMsgs) > 0 && estimateMessagesTokens(histMsgs) > budget {
		histMsgs = histMsgs[1:]
	}

	// 5. Compose final system prompt
	systemFull := systemPromptBase
	if memoryCtx != "" {
		systemFull = memoryCtx + "\n" + systemPromptBase
	}

	msgs := make([]capgateway.LLMMessage, 0, len(histMsgs)+2)
	if systemFull != "" {
		msgs = append(msgs, capgateway.LLMMessage{Role: "system", Content: systemFull})
	}
	msgs = append(msgs, histMsgs...)
	msgs = append(msgs, capgateway.LLMMessage{Role: "user", Content: currentInput})
	return msgs
}
