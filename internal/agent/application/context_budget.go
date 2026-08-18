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
// safetyRatio 与 ReAct 循环侧同一来源（execution 级 registry 参数
// agent.compaction_safety_ratio）：一次执行一个 usable（I1）。
func resolveLedgerBudget(maxTokens, outputReserve int, safetyRatio float64) agentgraph.Budget {
	if outputReserve <= 0 {
		outputReserve = constants.DefaultOutputReserveTokens
	}
	return agentgraph.ComputeBudget(maxTokens, outputReserve, safetyRatio)
}

// fitSystemAndMemory 在 headCap 配额内截断 system prompt（保底
// MinSystemPromptTokens）与 memory context（剩余头部份额的 30%）。
func fitSystemAndMemory(headCap int, systemPromptBase, memoryCtx string) (string, string) {
	sysTokens := tokenutil.EstimateText(systemPromptBase)
	sysReserve := max(sysTokens, constants.MinSystemPromptTokens)
	sysReserve = min(sysReserve, headCap)
	if sysTokens > sysReserve {
		systemPromptBase = truncateToTokenBudget(systemPromptBase, sysReserve)
	}
	if memoryCtx != "" {
		memBudget := int(float64(headCap-sysReserve) * constants.MemoryBudgetRatio)
		if tokenutil.EstimateText(memoryCtx) > memBudget {
			memoryCtx = truncateToTokenBudget(memoryCtx, memBudget)
		}
	}
	return systemPromptBase, memoryCtx
}

// minimalHeadMessages 在预算耗尽（usable − task ≤ 0）时组装最小 head：
// system prompt + 当前输入，memory 能装下才带——绝不丢弃 system prompt。
// globalSuffix（全局系统提示词）豁免 head 配额、紧随 base 完整追加：
// 它承载平台级安全/事实约束（如"事实与引用"四条款），截断会静默
// 丢失治理指令，故不受 FixedHeadCap 约束。
// 超窗发送交由规格收敛机制（400 context_length_exceeded → TokenCorrection
// 下调阈值）处理，self-correction 才有机会启动（C1 回归防护）。
func minimalHeadMessages(headCap int, systemPromptBase, globalSuffix, memoryCtx, currentInput string) []port.LLMMessage {
	systemPromptBase, memoryCtx = fitSystemAndMemory(headCap, systemPromptBase, memoryCtx)
	systemFull := systemPromptBase
	if globalSuffix != "" {
		systemFull += "\n\n" + globalSuffix
	}
	if memoryCtx != "" {
		systemFull += "\n\n" + agentgraph.WrapUntrustedSection("memory", memoryCtx)
	}
	return []port.LLMMessage{
		{Role: "system", Content: systemFull},
		{Role: "user", Content: currentInput},
	}
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
	// outputReserve 传 0 走自动链（显式 > vendor > 常量），safetyRatio 传 0
	// 走 constants 默认——此处无法感知执行参数。globalSuffix 传 ""：
	// 旧签名调用方（无全局提示词概念）保持原行为。
	return BuildContextMessagesWithCompaction(
		context.Background(),
		systemPromptBase, "", memoryCtx, history, currentInput,
		maxTokens, historyWindow, 0, 0, nil,
	)
}

// BuildContextMessagesWithCompaction 在 BuildContextMessages 的优先级预算基础上，
// 用 compactor 把“将被丢弃的最老历史”压成一条摘要注入 system，而非直接扔掉。
//
// 预算优先级不变：task(currentInput) > systemPrompt(保底) > memoryCtx(≤30%) > history。
// 差异只在预算来源：window → usable → 四配额（Spec 第 2 节账本），
// system+memory 占用 FixedHeadCap 配额，history 占用 HistoryCap 配额，
// task 永不压缩。globalSuffix（平台级全局系统提示词）豁免 FixedHeadCap：
// 它在 systemPromptBase 截断之后、summary/memory 之前完整追加，保证
// 治理指令（事实约束、隐私边界等）在预算紧张时不丢失。history 溢出处理：
//   - compactor == nil：溢出的最老消息被丢弃（与旧行为逐字节一致）。
//   - compactor != nil：溢出消息先压缩成摘要，占用预留额度后注入 system。
//
// outputReserve ≤ 0 时走自动链兜底常量（显式 max_tokens > vendor maxOut 的
// 解析在 AgentService，此处仅接收结果）；safetyRatio ≤ 0 走 constants 默认，
// 必须与 ReAct 循环侧同一来源（I1），保证一次执行一个 usable。
// 降级保证：compactor 返回 error 或空摘要时，静默退回纯截断，绝不阻断主流程。
func BuildContextMessagesWithCompaction(
	ctx context.Context,
	systemPromptBase string,
	globalSuffix string,
	memoryCtx string,
	history []*ChatMessage,
	currentInput string,
	maxTokens int,
	historyWindow int,
	outputReserve int,
	safetyRatio float64,
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
	// 传入时兜底常量。任务 token 从 history 配额扣减（Spec 第 2 节
	// history = usable − fixedHead − tools − task）。
	b := resolveLedgerBudget(maxTokens, outputReserve, safetyRatio)
	taskTokens := tokenutil.EstimateText(currentInput)
	b = b.WithTask(taskTokens)

	// 1. task（currentInput）最高优先级且永不压缩：usable 扣减 task 后
	// 仍为正，才有 head + history 的可组装空间。
	if b.Usable-taskTokens <= 0 {
		// 降级：预算耗尽仍保留最小 head（system prompt + 当前输入，
		// memory 能装下才带），不丢弃 system prompt；超窗发送交由规格
		// 收敛机制（400 context_length_exceeded → TokenCorrection 下调
		// 阈值）处理。fallback 来源的 WARN 在 resolveExecutionWindow。
		return minimalHeadMessages(max(b.FixedHeadCap, constants.MinSystemPromptTokens),
			systemPromptBase, globalSuffix, memoryCtx, currentInput)
	}

	// 2/3. System prompt 与 memory context — FixedHeadCap 配额内截断
	systemPromptBase, memoryCtx = fitSystemAndMemory(b.FixedHeadCap, systemPromptBase, memoryCtx)

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

	// 5. Compose final system prompt: base + globalSuffix + [summary] + memory.
	// globalSuffix 豁免 head 配额，在截断后的 base 之后完整追加——它是平台级
	// 治理指令（事实/隐私约束），且先于 untrusted 内容注入，保持可信指令在前。
	systemFull := systemPromptBase
	if globalSuffix != "" {
		systemFull += "\n\n" + globalSuffix
	}
	if summary != "" {
		systemFull += "\n\n[早期对话摘要]\n" + agentgraph.WrapUntrustedSection("history", summary)
	}
	if memoryCtx != "" {
		systemFull += "\n\n" + agentgraph.WrapUntrustedSection("memory", memoryCtx)
	}

	msgs := make([]port.LLMMessage, 0, len(histMsgs)+2)
	if systemFull != "" {
		msgs = append(msgs, port.LLMMessage{Role: "system", Content: systemFull})
	}
	msgs = append(msgs, histMsgs...)
	msgs = append(msgs, port.LLMMessage{Role: "user", Content: currentInput})
	return msgs
}
