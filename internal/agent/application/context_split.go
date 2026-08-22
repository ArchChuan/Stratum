package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// CompactionReuse 是组装侧跨轮压缩复用的配置；nil 表示不启用（保持原行为）。
// Store 非 nil 时启用（D6）：入口读共享压缩摘要存储的覆盖游标，covered_until
// 之前的消息段折叠为摘要、不进入窗口判断（D3）；压缩成功后回写推进游标。
// 模型 A（累计覆盖）：新摘要 = 旧覆盖摘要（作为增量输入）+ 新溢出轮，system
// 中始终只有一条累计摘要，covered_until 单调推进。
type CompactionReuse struct {
	Store          port.CompactionStore
	TenantID       string
	ConversationID string
	// RecentRounds 溢出后保留的最近工具轮原文数（D4）。0 = 溢出轮全压，
	// 对齐循环侧 recentGroups==0 语义；>0 时仅更老的溢出轮进压缩。
	RecentRounds int
}

// recentRoundsOf 取复用的最近工具轮保留数；reuse 未启用时为 0（溢出轮全压）。
func recentRoundsOf(reuse *CompactionReuse) int {
	if reuse == nil {
		return 0
	}
	return reuse.RecentRounds
}

// chatRound 是组装侧窗口/压缩的原子单元：一条 user 消息及其后的连续
// assistant 消息（含 internal 工具摘要）构成一轮。轮内永不拆分（D5）：
// 工具对随整轮进入压缩或保留，不产生孤儿 tool 消息。
type chatRound struct {
	msgs    []*ChatMessage
	hasTool bool // 轮内含 internal 工具摘要消息
}

// splitRounds 把消息按轮划分。组装侧输入 role ∈ {user, assistant}；
// internal 工具摘要消息（role=assistant, Visibility=Internal）归入其所属轮
// （D5 配对原子：同一轮整体进入压缩或保留）。
func splitRounds(msgs []*ChatMessage) []chatRound {
	rounds := make([]chatRound, 0, len(msgs))
	for _, m := range msgs {
		last := len(rounds) - 1
		if m.Role == "user" || last < 0 {
			rounds = append(rounds, chatRound{msgs: []*ChatMessage{m}, hasTool: isInternalSummary(m)})
			continue
		}
		rounds[last].msgs = append(rounds[last].msgs, m)
		rounds[last].hasTool = rounds[last].hasTool || isInternalSummary(m)
	}
	return rounds
}

// isInternalSummary 判断消息是否为 internal 工具摘要（组装侧工具数据形态，
// buildToolObservationSummary 落库，Role=assistant, Visibility=Internal）。
func isInternalSummary(m *ChatMessage) bool {
	return m != nil && m.Visibility == domain.ChatMessageVisibilityInternal
}

// filterUncovered 丢弃 covered_until（含）之前的所有消息，返回未覆盖段。
// 未覆盖段才参与窗口判断（D3：窗口 = 游标后未压缩轮数）。chat_messages.id
// 是 UUID v7（时间有序），字符串比较即时间比较。
func filterUncovered(msgs []*ChatMessage, coveredUntil string) []*ChatMessage {
	if coveredUntil == "" {
		return msgs
	}
	for i, m := range msgs {
		if m.ID > coveredUntil {
			return msgs[i:]
		}
	}
	return nil
}

// selectOverflowRounds 按「未压缩轮数」判断窗口溢出，整轮为单位截断（D3/D5）。
// compacted：被压掉的最老轮（溢出且不在最近 N 对保护内）；kept：最近 N 对
// 保护轮 + 窗口内轮（原文保留）。recentRounds <= 0 → 溢出轮全压。
func selectOverflowRounds(rounds []chatRound, historyWindow, recentRounds int) (compacted, kept []chatRound) {
	if len(rounds) <= historyWindow {
		return nil, rounds
	}
	overflow := len(rounds) - historyWindow
	compactCount := max(overflow-recentRounds, 0)
	return rounds[:compactCount], rounds[compactCount:]
}

// flattenRounds 把轮内消息转成组装/压缩输入（只取 Role/Content，与现有
// 转换一致；StepsJSON 不重建——R5：steps_json 恒空）。
func flattenRounds(rounds []chatRound) []port.LLMMessage {
	out := make([]port.LLMMessage, 0, len(rounds)*2)
	for _, r := range rounds {
		for _, m := range r.msgs {
			out = append(out, port.LLMMessage{Role: m.Role, Content: m.Content})
		}
	}
	return out
}

// loadCompactionCoverage 读取覆盖游标并折叠 covered 段（D2/D3）。
// 返回覆盖摘要与折叠后的未覆盖段。reuse 为 nil 或无 Store → 无覆盖。
// 读取失败 fail closed：降级为无覆盖（摘要空、history 原样）+ 返回错误，
// 调用方禁回写——宁可多压不可丢上下文（§3.2.7）。
func loadCompactionCoverage(ctx context.Context, reuse *CompactionReuse, history []*ChatMessage) (coveredSummary string, uncovered []*ChatMessage, err error) {
	if reuse == nil || reuse.Store == nil {
		return "", history, nil
	}
	cov, covErr := reuse.Store.GetCoverage(ctx, reuse.TenantID, reuse.ConversationID)
	if covErr != nil {
		return "", history, fmt.Errorf("compaction coverage: %w", covErr)
	}
	if cov == nil {
		return "", history, nil
	}
	return cov.Summary, filterUncovered(history, cov.CoveredUntil), nil
}

// buildCompactionInput 组装增量压缩输入：已覆盖段摘要前置、新溢出轮随后
// （LangGraph 增量摘要模式：running summary + 新段 → 新 summary）。无覆盖
// 摘要时直接返回溢出轮（首次压缩）。
func buildCompactionInput(coveredSummary string, dropped []port.LLMMessage) []port.LLMMessage {
	if coveredSummary == "" {
		return dropped
	}
	out := make([]port.LLMMessage, 0, len(dropped)+1)
	out = append(out, port.LLMMessage{Role: "system", Content: coveredSummary})
	return append(out, dropped...)
}

// persistCompaction 回写压缩段：covered_until 推进到最后一条被压掉的消息 id。
// source_start 传本次段首条（Upsert 对已有行保留原始起点——模型 A 累计覆盖）。
func persistCompaction(ctx context.Context, reuse *CompactionReuse, compacted []chatRound, summary string) error {
	first := compacted[0].msgs[0]
	lastMsgs := compacted[len(compacted)-1].msgs
	last := lastMsgs[len(lastMsgs)-1]
	seg := &domain.CompactionSegment{
		ConversationID: reuse.ConversationID,
		CoveredUntil:   last.ID,
		Summary:        summary,
		SourceStart:    first.ID,
		SourceEnd:      last.ID,
		TokenCount:     tokenutil.EstimateText(summary),
	}
	return reuse.Store.Upsert(ctx, reuse.TenantID, seg)
}
