package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// msgGroup is the atomic unit of compaction. A group is either a single
// standalone message (system / user / plain-assistant) or an assistant message
// carrying tool_calls together with every tool message that answers them.
// Groups are never split: dropping or keeping happens at group granularity so
// no tool_call is ever left without its tool_result (and vice versa), which the
// OpenAI/Qwen chat APIs reject with HTTP 400.
type msgGroup struct {
	msgs      []port.LLMMessage
	hasTool   bool // group contains an assistant→tool_calls pairing
	role0     string
	anchorHdr bool // leading system / initial-user anchor, never evicted
	priority  int  // higher values are truncated later
}

// groupMessages walks the flat message slice and folds it into atomic groups.
// Pairing rule: an assistant message with ToolCalls opens a group that absorbs
// all immediately-following tool messages (they carry ToolCallID answering it).
func groupMessages(msgs []port.LLMMessage) []msgGroup {
	groups := make([]msgGroup, 0, len(msgs))
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			grp := msgGroup{msgs: []port.LLMMessage{m}, hasTool: true, role0: m.Role}
			j := i + 1
			for j < len(msgs) && msgs[j].Role == "tool" {
				grp.msgs = append(grp.msgs, msgs[j])
				j++
			}
			groups = append(groups, grp)
			i = j
			continue
		}
		groups = append(groups, msgGroup{msgs: []port.LLMMessage{m}, role0: m.Role})
		i++
	}
	markAnchors(groups)
	return groups
}

// markAnchors flags leading system messages. The latest user request is pulled
// out separately during compaction because earlier user messages may be loaded
// conversation history rather than the task currently being executed.
func markAnchors(groups []msgGroup) {
	for i := range groups {
		if groups[i].role0 != "system" {
			return
		}
		groups[i].anchorHdr = true
	}
}

func flatten(groups []msgGroup) []port.LLMMessage {
	out := make([]port.LLMMessage, 0, len(groups)*2)
	for _, g := range groups {
		out = append(out, g.msgs...)
	}
	return out
}

// compactLoopMessages bounds a per-request copy of the conversation to budget
// tokens without ever orphaning a tool_call/tool_result pairing. It is lazy:
// when the estimate already fits (below the safety threshold) the input slice
// is returned unchanged and no compactor call is made.
//
// Layout after compaction:
//
//	[anchor head] [breadcrumb summary of evicted middle] [recent N groups]
//
// The middle is summarized via compactor when present, otherwise replaced by a
// short system breadcrumb noting how many turns were elided. compactor failure
// degrades to the breadcrumb path — the loop is never blocked on compaction.
func compactLoopMessages(
	ctx context.Context,
	msgs []port.LLMMessage,
	budget int,
	recentGroups int,
	compactor port.HistoryCompactor,
) []port.LLMMessage {
	return compactLoopMessagesWithReserve(ctx, msgs, budget, 0, recentGroups, compactor)
}

func compactLoopMessagesWithReserve(
	ctx context.Context,
	msgs []port.LLMMessage,
	budget int,
	reservedTokens int,
	recentGroups int,
	compactor port.HistoryCompactor,
) []port.LLMMessage {
	return compactLoopMessagesWithPolicy(ctx, msgs, Budget{HistoryCap: budget}, reservedTokens, recentGroups,
		1, 1.0, constants.LoopCompactionSafetyRatio, compactor)
}

// compactLoopMessagesWithPolicy is the full compaction core. The threshold is
// budget.HistoryCap·safety/correction: a correction > 1 (the token estimate
// under-counted the previous request) lowers the threshold and compacts
// earlier; < 1 later. correction ≤ 0 is treated as 1 (no correction).
// Budget 零值（未初始化）时 HistoryCap 为 0，压缩禁用，消息原样返回。
func compactLoopMessagesWithPolicy(
	ctx context.Context,
	msgs []port.LLMMessage,
	budget Budget,
	reservedTokens int,
	recentGroups int,
	protectedUsers int,
	correction float64,
	safetyRatio float64,
	compactor port.HistoryCompactor,
) []port.LLMMessage {
	if budget.HistoryCap <= 0 {
		return msgs
	}
	// 阈值基于预算账本的 history 配额：工具 token 走 ToolsCap 独立配额，
	// 不再压垮可压缩区（Spec 第 2 节根因修复）。
	threshold := compactionThreshold(budget.HistoryCap, reservedTokens, correction, safetyRatio)
	if tokenutil.EstimateMessages(toEstimate(msgs)) <= threshold {
		return msgs // lazy: still fits, do nothing
	}

	groups := groupMessages(msgs)
	headEnd := anchorCount(groups)
	if recentGroups < 0 {
		recentGroups = 0
	}
	rebuilt := rebuildProtectedSegments(ctx, groups, headEnd, recentGroups, protectedUsers, compactor)
	rebuilt = evictUntilFit(rebuilt, threshold)
	rebuilt = truncateProtectedUntilFit(rebuilt, threshold)
	return flatten(rebuilt)
}

// compactionThreshold normalizes the policy parameters and derives the budget
// threshold: correction > 1 (previous estimate under-counted) lowers it, so the
// next step compacts earlier; safetyRatio scales the usable fraction of budget.
func compactionThreshold(budget, reservedTokens int, correction, safetyRatio float64) int {
	if correction <= 0 {
		correction = 1
	}
	if safetyRatio <= 0 {
		safetyRatio = constants.LoopCompactionSafetyRatio
	}
	return max(int(float64(budget)*safetyRatio/correction)-reservedTokens, 0)
}

// rebuildProtectedSegments re-joins the anchor head, breadcrumb-compacted middle
// segments, and the protected user turns. The latest user message is the active
// task; preceding user messages belong to chat history and are protected in
// order of recency when protectedUsers > 0.
func rebuildProtectedSegments(ctx context.Context, groups []msgGroup, headEnd, recentGroups, protectedUsers int, compactor port.HistoryCompactor) []msgGroup {
	protectedUserIdx := make(map[int]struct{}, max(protectedUsers, 0))
	for i := len(groups) - 1; i >= headEnd && len(protectedUserIdx) < protectedUsers; i-- {
		if groups[i].role0 == "user" {
			protectedUserIdx[i] = struct{}{}
		}
	}
	latestProtected := latestProtectedUserIndex(protectedUserIdx)

	rebuilt := append([]msgGroup(nil), groups[:headEnd]...)
	segmentStart := headEnd
	seenProtectedUser := false
	for i := headEnd; i < len(groups); i++ {
		if _, keep := protectedUserIdx[i]; !keep {
			continue
		}
		keepRecent := 0
		if seenProtectedUser {
			keepRecent = recentGroups
		}
		rebuilt = appendCompactedSegment(ctx, rebuilt, groups[segmentStart:i], keepRecent, compactor)
		protectedUser := groups[i]
		protectedUser.anchorHdr = true
		protectedUser.priority = 2
		if protectedUsers > 1 && i == latestProtected {
			protectedUser.priority = 1
		}
		rebuilt = append(rebuilt, protectedUser)
		segmentStart = i + 1
		seenProtectedUser = true
	}
	return appendCompactedSegment(ctx, rebuilt, groups[segmentStart:], recentGroups, compactor)
}

func latestProtectedUserIndex(indices map[int]struct{}) int {
	latest := -1
	for i := range indices {
		latest = max(latest, i)
	}
	return latest
}

func appendCompactedSegment(ctx context.Context, dst, segment []msgGroup, keepRecent int, compactor port.HistoryCompactor) []msgGroup {
	tailStart := max(len(segment)-keepRecent, 0)
	if bc := summarizeMiddle(ctx, segment[:tailStart], compactor); bc != nil {
		bc.anchorHdr = true
		dst = append(dst, *bc)
	}
	return append(dst, segment[tailStart:]...)
}

func anchorCount(groups []msgGroup) int {
	n := 0
	for _, g := range groups {
		if !g.anchorHdr {
			break
		}
		n++
	}
	return n
}

// toEstimate maps LLMMessage to tokenutil's estimation input. A tool-calling
// assistant message carries its payload in ToolCalls (Content is empty), and a
// tool result carries a ToolCallID; both must be folded into the estimated text
// or the tool-call turns would be counted as ~0 tokens, systematically
// under-estimating size and defeating the budget guard.
func toEstimate(msgs []port.LLMMessage) []tokenutil.Message {
	out := make([]tokenutil.Message, len(msgs))
	for i, m := range msgs {
		content := m.Content
		for _, tc := range m.ToolCalls {
			content += " " + tc.Name
			if args, err := json.Marshal(tc.Arguments); err == nil {
				content += " " + string(args)
			}
		}
		if m.ToolCallID != "" {
			content += " " + m.ToolCallID
		}
		out[i] = tokenutil.Message{Role: m.Role, Content: content}
	}
	return out
}

// summarizeMiddle collapses evicted groups into a single system breadcrumb.
// With a compactor it embeds a natural-language summary; without one (or on
// error) it emits a count-only marker so the model knows history was elided.
// Returns nil when there is nothing to summarize.
func summarizeMiddle(ctx context.Context, middle []msgGroup, compactor port.HistoryCompactor) *msgGroup {
	if len(middle) == 0 {
		return nil
	}
	flat := flatten(middle)
	marker := fmt.Sprintf("[已省略 %d 轮较早对话以控制上下文长度]", len(middle))
	if compactor != nil {
		if summary, err := compactor.CompactHistory(ctx, flat); err == nil && summary != "" {
			marker = "[早期对话摘要] " + summary
		}
	}
	return &msgGroup{
		msgs:  []port.LLMMessage{{Role: "system", Content: marker}},
		role0: "system",
	}
}

// evictTailUntilFit is the last-resort guard: if head+breadcrumb+tail still
// exceed budget, drop whole groups from the front of the tail region (oldest
// recent groups first) until it fits or only the protected prefix remains.
// Group-level dropping keeps every surviving tool pairing intact.
func evictUntilFit(groups []msgGroup, budget int) []msgGroup {
	for tokenutil.EstimateMessages(toEstimate(flatten(groups))) > budget {
		dropIdx := -1
		for i := range groups {
			if !groups[i].anchorHdr {
				dropIdx = i
				break
			}
		}
		if dropIdx < 0 {
			break
		}
		groups = append(groups[:dropIdx], groups[dropIdx+1:]...)
	}
	return groups
}

// truncateProtectedUntilFit bounds anchors and the optional breadcrumb after
// every evictable group has been removed. Roles and message ordering remain
// intact; only text content is shortened. This is the last-resort path for an
// oversized system prompt, active-skill instruction, user prompt, or summary.
func truncateProtectedUntilFit(groups []msgGroup, budget int) []msgGroup {
	for tokenutil.EstimateMessages(toEstimate(flatten(groups))) > budget {
		groupIdx, msgIdx, largest := -1, -1, 0
		selectedPriority := int(^uint(0) >> 1)
		for i := range groups {
			if !groups[i].anchorHdr {
				continue
			}
			for j := range groups[i].msgs {
				tokens := tokenutil.EstimateText(groups[i].msgs[j].Content)
				if tokens > 0 && (groups[i].priority < selectedPriority ||
					(groups[i].priority == selectedPriority && tokens > largest)) {
					selectedPriority = groups[i].priority
					groupIdx, msgIdx, largest = i, j, tokens
				}
			}
		}
		if largest == 0 {
			break
		}

		over := tokenutil.EstimateMessages(toEstimate(flatten(groups))) - budget
		keepTokens := max(largest-over, 0)
		groups[groupIdx].msgs[msgIdx].Content = truncateEstimatedText(
			groups[groupIdx].msgs[msgIdx].Content,
			keepTokens,
		)
	}
	return groups
}

func truncateEstimatedText(text string, tokens int) string {
	maxBytes := max(tokens, 0) * 3
	if len(text) <= maxBytes {
		return text
	}
	for maxBytes > 0 && maxBytes < len(text) && (text[maxBytes]&0xc0) == 0x80 {
		maxBytes--
	}
	return text[:maxBytes]
}
