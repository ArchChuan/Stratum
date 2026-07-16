package application

import (
	"context"
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
	// 无压缩器时委托给完整实现，行为等同于历史版本（最老先丢）。
	return BuildContextMessagesWithCompaction(
		context.Background(),
		systemPromptBase, memoryCtx, history, currentInput,
		maxTokens, historyWindow, nil,
	)
}

// BuildContextMessagesWithCompaction 在 BuildContextMessages 的优先级预算基础上，
// 用 compactor 把“将被丢弃的最老历史”压成一条摘要注入 system，而非直接扔掉。
//
// 预算优先级不变：currentInput > systemPrompt(保底) > memoryCtx(≤30%) > history。
// 差异只在 history 溢出处理：
//   - compactor == nil：溢出的最老消息被丢弃（与旧行为逐字节一致）。
//   - compactor != nil：溢出消息先压缩成摘要，占用预留额度后注入 system。
//
// 降级保证：compactor 返回 error 或空摘要时，静默退回纯截断，绝不阻断主流程。
func BuildContextMessagesWithCompaction(
	ctx context.Context,
	systemPromptBase string,
	memoryCtx string,
	history []*ChatMessage,
	currentInput string,
	maxTokens int,
	historyWindow int,
	compactor port.HistoryCompactor,
) []port.LLMMessage {
	if historyWindow <= 0 {
		historyWindow = constants.DefaultContextHistoryWindow
	}
	// 窗口外的最老消息是压缩候选，而非立即丢弃。
	var overflow []*ChatMessage
	if len(history) > historyWindow {
		overflow = history[:len(history)-historyWindow]
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

	// 4. Convert in-window history and trim oldest to fit; collect dropped for compaction.
	histMsgs := make([]port.LLMMessage, 0, len(history))
	for _, m := range history {
		histMsgs = append(histMsgs, port.LLMMessage{Role: m.Role, Content: m.Content})
	}
	windowHistMsgs := append([]port.LLMMessage(nil), histMsgs...)

	dropped := make([]port.LLMMessage, 0, len(overflow))
	for _, m := range overflow {
		dropped = append(dropped, port.LLMMessage{Role: m.Role, Content: m.Content})
	}

	// 为摘要预留额度（仅当有压缩器时），封顶避免吃满预算。
	summaryReserve := 0
	if compactor != nil {
		summaryReserve = min(budget/4, constants.MinSystemPromptTokens*2)
	}
	histBudget := max(budget-summaryReserve, 0)
	for len(histMsgs) > 0 && estimateMessagesTokens(histMsgs) > histBudget {
		dropped = append(dropped, histMsgs[0])
		histMsgs = histMsgs[1:]
	}

	// 4b. Compact dropped history into a summary; degrade silently on any failure.
	summary := ""
	if compactor != nil && summaryReserve > 0 && len(dropped) > 0 {
		if s, err := compactor.CompactHistory(ctx, dropped); err == nil && s != "" {
			summary = truncateToTokenBudget(s, summaryReserve)
		}
	}
	if compactor != nil && summary == "" {
		// The reserved summary budget is unused on failure/empty output. Rebuild
		// history with the full remaining budget so fallback matches plain truncation.
		histMsgs = windowHistMsgs
		for len(histMsgs) > 0 && estimateMessagesTokens(histMsgs) > budget {
			histMsgs = histMsgs[1:]
		}
	}

	// 5. Compose final system prompt: base + [summary] + memory.
	systemFull := systemPromptBase
	if summary != "" {
		systemFull += "\n\n[早期对话摘要]\n" + summary
	}
	if memoryCtx != "" {
		systemFull += "\n\n" + memoryCtx
	}

	msgs := make([]port.LLMMessage, 0, len(histMsgs)+2)
	if systemFull != "" {
		msgs = append(msgs, port.LLMMessage{Role: "system", Content: systemFull})
	}
	msgs = append(msgs, histMsgs...)
	msgs = append(msgs, port.LLMMessage{Role: "user", Content: currentInput})
	return msgs
}
