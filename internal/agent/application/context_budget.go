package application

import (
	"context"
	"errors"
	"fmt"
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
// safetyRatio 锁定平台默认（产品规格：不暴露用户配置），传 0 →
// ComputeBudget 回退 ContextSafetyReserveRatio（0.2）：一次执行一个 usable（I1）。
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
// 跨轮复用（游标折叠、增量压缩、回写）见 BuildContextMessagesWithCompactionReuse；
// 本函数等价于以 nil reuse 调用它，行为逐字节一致。
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
	msgs, _ := buildContextMessagesCore(ctx, systemPromptBase, globalSuffix, memoryCtx, history,
		currentInput, maxTokens, historyWindow, outputReserve, safetyRatio, compactor, nil)
	return msgs
}

// BuildContextMessagesWithCompactionReuse 在 WithCompaction 的基础上启用跨轮复用
// （D6）：入口读共享压缩摘要存储的覆盖游标，covered_until 之前的消息段折叠为
// 摘要、不进入窗口判断（D2/D3）；窗口溢出按「未压缩轮数」整轮截断（D3/D5）；
// 溢出轮压缩后回写推进游标，covered_until 单调推进（模型 A 累计覆盖）。
//
// 返回 (msgs, err)：err 非 nil 表示覆盖读取或回写失败。消息已 fail-closed
// 降级组装（覆盖读取失败 → 按无覆盖全量路径继续；回写失败 → 本次降级无新摘要、
// 走纯截断），绝不静默丢上下文；调用方必须记录并暴露该错误（§3.5）。
// recentRounds（CompactionReuse.RecentRounds）溢出后保留的最近工具轮原文数，
// 0 = 溢出轮全压（对齐循环侧 recentGroups==0 语义）。
func BuildContextMessagesWithCompactionReuse(
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
	reuse *CompactionReuse,
) ([]port.LLMMessage, error) {
	return buildContextMessagesCore(ctx, systemPromptBase, globalSuffix, memoryCtx, history,
		currentInput, maxTokens, historyWindow, outputReserve, safetyRatio, compactor, reuse)
}

func buildContextMessagesCore(
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
	reuse *CompactionReuse,
) ([]port.LLMMessage, error) {
	if historyWindow <= 0 {
		historyWindow = constants.DefaultContextHistoryWindow
	}

	// 跨轮复用（D2/D3）：读覆盖游标，covered 段折叠为摘要、不进入窗口判断。
	// 读取失败 fail closed：降级为无覆盖 + 禁回写，宁可多压不可丢上下文。
	coveredSummary, uncovered, coverageErr := loadCompactionCoverage(ctx, reuse, history)
	if coverageErr != nil {
		reuse = nil
	}

	// 窗口溢出按「未压缩轮数」计（D3），整轮为单位（D5），不逐条切断工具对。
	compactedRounds, keptRounds := selectOverflowRounds(splitRounds(uncovered),
		historyWindow, recentRoundsOf(reuse))

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
			systemPromptBase, globalSuffix, memoryCtx, currentInput), coverageErr
	}

	// 2/3. System prompt 与 memory context — FixedHeadCap 配额内截断
	systemPromptBase, memoryCtx = fitSystemAndMemory(b.FixedHeadCap, systemPromptBase, memoryCtx)

	// 4. 保留轮 → 历史消息；预算截断丢最老。被预算丢弃的消息不进压缩集、
	// 不回写——它们未被覆盖，下次加载重新评估，不静默丢信息。
	histMsgs := flattenRounds(keptRounds)
	windowHistMsgs := append([]port.LLMMessage(nil), histMsgs...)
	dropped := flattenRounds(compactedRounds)

	// 为摘要预留额度（仅当有压缩器时），从 HistoryCap 按 5% 联动（下限 200t），
	// 封顶避免吃满可压缩区。
	summaryReserve := 0
	if compactor != nil {
		summaryReserve = min(constants.DynamicSummaryReserve(b.HistoryCap), b.HistoryCap)
	}
	histBudget := max(b.HistoryCap-summaryReserve, 0)
	histMsgs = trimHistoryToBudget(histMsgs, histBudget)

	// 4b. 压缩溢出轮（增量：coveredSummary 前置 + 溢出轮）→ 累计摘要；
	// 成功后回写推进游标。回写失败 → 本次降级无新摘要 + 暴露错误（§3.5，
	// 不得伪成功），下次全量重压，行为确定。
	summary, persistErr := compactAndPersist(ctx, compactor, coveredSummary, dropped,
		summaryReserve, reuse, compactedRounds)
	if compactor != nil && summary == "" {
		// 压缩失败/空输出 → 预留额度未用，用完整 HistoryCap 重建，回退
		// 纯截断（与无压缩器逐字节一致）。
		histMsgs = trimHistoryToBudget(windowHistMsgs, b.HistoryCap)
	}
	if persistErr != nil {
		coverageErr = errors.Join(coverageErr, persistErr)
	}

	// 5. 组合 system：base + globalSuffix + [摘要] + memory。摘要（新压缩或
	// 持久化覆盖摘要）放 system 而非 history 尾部（D2）。globalSuffix 豁免
	// head 配额，在截断后的 base 之后完整追加——平台级治理指令先于
	// untrusted 内容注入，保持可信指令在前。
	return assembleContextMessages(systemPromptBase, globalSuffix, coveredSummary, summary,
		memoryCtx, histMsgs, currentInput), coverageErr
}

// trimHistoryToBudget 从最老消息起逐条丢弃，直到消息估计 token 落在预算内。
func trimHistoryToBudget(msgs []port.LLMMessage, budget int) []port.LLMMessage {
	for len(msgs) > 0 && estimateMessagesTokens(msgs) > budget {
		msgs = msgs[1:]
	}
	return msgs
}

// compactAndPersist 压缩溢出轮并回写覆盖游标。无压缩器/无溢出 → 原样返回。
// 回写失败 → 返回空摘要 + 错误（调用方降级为纯截断并暴露，§3.5）。
func compactAndPersist(
	ctx context.Context,
	compactor port.HistoryCompactor,
	coveredSummary string,
	dropped []port.LLMMessage,
	summaryReserve int,
	reuse *CompactionReuse,
	compactedRounds []chatRound,
) (string, error) {
	if compactor == nil || summaryReserve <= 0 || len(dropped) == 0 {
		return "", nil
	}
	summary := ""
	if s, err := compactor.CompactHistory(ctx, buildCompactionInput(coveredSummary, dropped)); err == nil && s != "" {
		summary = truncateToTokenBudget(s, summaryReserve)
	}
	if summary == "" || reuse == nil || len(compactedRounds) == 0 {
		return summary, nil
	}
	if err := persistCompaction(ctx, reuse, compactedRounds, summary); err != nil {
		return "", fmt.Errorf("compaction writeback: %w", err)
	}
	return summary, nil
}

// assembleContextMessages 组装最终消息切片。摘要优先新压缩摘要（含覆盖段信息），
// 否则回退持久化覆盖摘要（未溢出或降级时复用缓存，D6）；两者都为空则无摘要。
func assembleContextMessages(
	systemPromptBase string,
	globalSuffix string,
	coveredSummary string,
	summary string,
	memoryCtx string,
	histMsgs []port.LLMMessage,
	currentInput string,
) []port.LLMMessage {
	systemFull := systemPromptBase
	if globalSuffix != "" {
		systemFull += "\n\n" + globalSuffix
	}
	if summary != "" {
		systemFull += "\n\n[早期对话摘要]\n" + agentgraph.WrapUntrustedSection("history", summary)
	} else if coveredSummary != "" {
		systemFull += "\n\n[早期对话摘要]\n" + agentgraph.WrapUntrustedSection("history", coveredSummary)
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
