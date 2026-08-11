package application

import (
	"context"
	"unicode/utf8"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
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

// resolveLedgerBudget 解析 outputReserve 自动链并计算执行级预算账本。
// 显式 max_tokens > vendor maxOut 的解析在 AgentService，此处仅兜底常量。
func resolveLedgerBudget(maxTokens, outputReserve int) agentgraph.Budget {
	if outputReserve <= 0 {
		outputReserve = constants.DefaultOutputReserveTokens
	}
	return agentgraph.ComputeBudget(maxTokens, outputReserve, 0)
}

// fitSystemAndMemory 在 FixedHeadCap 配额内截断 system prompt（保底
// MinSystemPromptTokens）与 memory context（剩余头部份额的 30%）。
func fitSystemAndMemory(b agentgraph.Budget, systemPromptBase, memoryCtx string) (string, string) {
	sysTokens := tokenutil.EstimateText(systemPromptBase)
	sysReserve := max(sysTokens, constants.MinSystemPromptTokens)
	sysReserve = min(sysReserve, b.FixedHeadCap)
	if sysTokens > sysReserve {
		systemPromptBase = truncateToTokenBudget(systemPromptBase, sysReserve)
	}
	if memoryCtx != "" {
		memBudget := int(float64(b.FixedHeadCap-sysReserve) * constants.MemoryBudgetRatio)
		if tokenutil.EstimateText(memoryCtx) > memBudget {
			memoryCtx = truncateToTokenBudget(memoryCtx, memBudget)
		}
	}
	return systemPromptBase, memoryCtx
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
	// outputReserve 传 0 走自动链（显式 > vendor > 常量），此处无法感知执行参数。
	return BuildContextMessagesWithCompaction(
		context.Background(),
		systemPromptBase, memoryCtx, history, currentInput,
		maxTokens, historyWindow, 0, nil,
	)
}

// BuildContextMessagesWithCompaction 在 BuildContextMessages 的优先级预算基础上，
// 用 compactor 把“将被丢弃的最老历史”压成一条摘要注入 system，而非直接扔掉。
//
// 预算优先级不变：task(currentInput) > systemPrompt(保底) > memoryCtx(≤30%) > history。
// 差异只在预算来源：window → usable → 四配额（Spec 第 2 节账本），
// system+memory 占用 FixedHeadCap 配额，history 占用 HistoryCap 配额，
// task 永不压缩。history 溢出处理：
//   - compactor == nil：溢出的最老消息被丢弃（与旧行为逐字节一致）。
//   - compactor != nil：溢出消息先压缩成摘要，占用预留额度后注入 system。
//
// outputReserve ≤ 0 时走自动链兜底常量（显式 max_tokens > vendor maxOut 的
// 解析在 AgentService，此处仅接收结果）。
// 降级保证：compactor 返回 error 或空摘要时，静默退回纯截断，绝不阻断主流程。
func BuildContextMessagesWithCompaction(
	ctx context.Context,
	systemPromptBase string,
	memoryCtx string,
	history []*ChatMessage,
	currentInput string,
	maxTokens int,
	historyWindow int,
	outputReserve int,
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

	// 预算账本：maxTokens 即执行窗口（调用侧已解析），outputReserve 未显式
	// 传入时兜底常量。safetyRatio 走默认（0 = 用 constants 默认）。
	b := resolveLedgerBudget(maxTokens, outputReserve)

	// 1. task（currentInput）最高优先级且永不压缩：usable 扣减 task 后
	// 仍为正，才有 head + history 的可组装空间。
	if b.Usable-tokenutil.EstimateText(currentInput) <= 0 {
		return []port.LLMMessage{{Role: "user", Content: currentInput}}
	}

	// 2/3. System prompt 与 memory context — FixedHeadCap 配额内截断
	systemPromptBase, memoryCtx = fitSystemAndMemory(b, systemPromptBase, memoryCtx)

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

	// 为摘要预留额度（仅当有压缩器时），从 HistoryCap 按 5% 联动（下限 200t），
	// 封顶避免吃满可压缩区。
	summaryReserve := 0
	if compactor != nil {
		summaryReserve = min(constants.DynamicSummaryReserve(b.HistoryCap), b.HistoryCap)
	}
	histBudget := max(b.HistoryCap-summaryReserve, 0)
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
		// history with the full history quota so fallback matches plain truncation.
		histMsgs = windowHistMsgs
		for len(histMsgs) > 0 && estimateMessagesTokens(histMsgs) > b.HistoryCap {
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
