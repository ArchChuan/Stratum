package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// fakeCompactor records invocations and returns a canned summary/error.
type fakeCompactor struct {
	summary   string
	err       error
	callCount int
	gotMsgs   []port.LLMMessage
}

func (f *fakeCompactor) CompactHistory(_ context.Context, msgs []port.LLMMessage) (string, error) {
	f.callCount++
	f.gotMsgs = msgs
	return f.summary, f.err
}

// assertNoOrphans is the central safety invariant: every tool_call id emitted by
// an assistant message must be answered by a tool message in the same output,
// and every tool message's ToolCallID must trace back to a preceding assistant
// tool_call. A violation is exactly what makes OpenAI/Qwen return HTTP 400.
func assertNoOrphans(t *testing.T, msgs []port.LLMMessage) {
	t.Helper()
	open := map[string]bool{}
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				open[tc.ID] = true
			}
		}
		if m.Role == "tool" {
			if !open[m.ToolCallID] {
				t.Fatalf("orphan tool_result: ToolCallID %q has no preceding assistant tool_call", m.ToolCallID)
			}
			delete(open, m.ToolCallID)
		}
	}
	if len(open) > 0 {
		t.Fatalf("orphan tool_call(s) with no tool_result: %v", open)
	}
}

func sys(c string) port.LLMMessage  { return port.LLMMessage{Role: "system", Content: c} }
func usr(c string) port.LLMMessage  { return port.LLMMessage{Role: "user", Content: c} }
func asst(c string) port.LLMMessage { return port.LLMMessage{Role: "assistant", Content: c} }

// toolTurn builds one atomic group: an assistant message issuing a tool_call
// plus the tool message answering it. filler pads Content to force token growth.
func toolTurn(id, filler string) []port.LLMMessage {
	return []port.LLMMessage{
		{Role: "assistant", ToolCalls: []port.ToolCall{{ID: id, Name: "search"}}},
		{Role: "tool", ToolCallID: id, Content: filler},
	}
}

// bigHistory produces a system + user anchor followed by n tool turns whose
// combined size comfortably exceeds any small budget.
func bigHistory(n int) []port.LLMMessage {
	msgs := []port.LLMMessage{sys("you are an agent"), usr("do the task")}
	pad := strings.Repeat("x", 800)
	for i := 0; i < n; i++ {
		msgs = append(msgs, toolTurn(string(rune('a'+i)), pad)...)
	}
	return msgs
}

func TestCompactLoopMessages_LazyNoOp(t *testing.T) {
	// Small history well under budget: returned unchanged, compactor untouched.
	in := []port.LLMMessage{sys("s"), usr("u"), asst("a")}
	fc := &fakeCompactor{summary: "SUMMARY"}
	out := compactLoopMessages(context.Background(), in, 100000, 3, fc)
	if len(out) != len(in) {
		t.Fatalf("expected no-op, got %d msgs (want %d)", len(out), len(in))
	}
	if fc.callCount != 0 {
		t.Fatalf("compactor called %d times on a fitting history; want 0", fc.callCount)
	}
}

func TestCompactLoopMessages_ZeroBudgetDisabled(t *testing.T) {
	in := bigHistory(10)
	out := compactLoopMessages(context.Background(), in, 0, 3, nil)
	if len(out) != len(in) {
		t.Fatalf("zero budget must disable compaction; got %d want %d", len(out), len(in))
	}
}

func TestCompactLoopMessages_NoOrphansAfterEviction(t *testing.T) {
	// Force heavy compaction with a tight budget; the pairing invariant must hold
	// regardless of where the eviction boundary lands.
	for _, n := range []int{4, 7, 12, 20} {
		in := bigHistory(n)
		out := compactLoopMessages(context.Background(), in, 800, 3, nil)
		assertNoOrphans(t, out)
		if len(out) >= len(in) {
			t.Fatalf("n=%d: expected shrink, got %d >= %d", n, len(out), len(in))
		}
	}
}

func TestCompactLoopMessages_AnchorPreserved(t *testing.T) {
	in := bigHistory(15)
	out := compactLoopMessages(context.Background(), in, 800, 3, nil)
	if out[0].Role != "system" || out[0].Content != "you are an agent" {
		t.Fatalf("system anchor lost: %+v", out[0])
	}
	foundUser := false
	for _, message := range out[1:] {
		if message.Role == "user" && message.Content == "do the task" {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Fatalf("current user request lost: %+v", out)
	}
}

func TestCompactLoopMessages_PreservesLatestUserRequestWithHistory(t *testing.T) {
	msgs := []port.LLMMessage{
		sys("system"),
		usr("old historical request"),
		asst(strings.Repeat("old answer", 100)),
		usr("CURRENT TASK MUST SURVIVE"),
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, toolTurn(string(rune('a'+i)), strings.Repeat("result", 120))...)
	}

	out := compactLoopMessages(context.Background(), msgs, 800, 3, nil)
	joined := ""
	currentIdx := -1
	for _, message := range out {
		joined += message.Content
	}
	for i, message := range out {
		if message.Content == "CURRENT TASK MUST SURVIVE" {
			currentIdx = i
		}
		if message.Role == "assistant" && len(message.ToolCalls) > 0 && currentIdx < 0 {
			t.Fatalf("tool turn moved before current request: %+v", out)
		}
	}
	if !strings.Contains(joined, "CURRENT TASK MUST SURVIVE") {
		t.Fatalf("latest user request was evicted: %+v", out)
	}
	assertNoOrphans(t, out)
}

func TestCompactLoopMessages_CompactorSummaryInjected(t *testing.T) {
	fc := &fakeCompactor{summary: "早期做了搜索"}
	out := compactLoopMessages(context.Background(), bigHistory(15), 800, 3, fc)
	if fc.callCount == 0 {
		t.Fatal("compactor never called despite overflow")
	}
	found := false
	for _, m := range out {
		if m.Role == "system" && strings.Contains(m.Content, "早期做了搜索") {
			found = true
		}
	}
	if !found {
		t.Fatal("compactor summary not injected as breadcrumb")
	}
	assertNoOrphans(t, out)
}

func TestCompactLoopMessages_CompactorErrorDegrades(t *testing.T) {
	fc := &fakeCompactor{err: context.DeadlineExceeded}
	out := compactLoopMessages(context.Background(), bigHistory(15), 800, 3, fc)
	// On error, breadcrumb marker replaces summary; loop never blocked.
	found := false
	for _, m := range out {
		if m.Role == "system" && strings.Contains(m.Content, "已省略") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected count-only breadcrumb after compactor error")
	}
	assertNoOrphans(t, out)
}

func TestToEstimate_CountsToolCallPayload(t *testing.T) {
	// An assistant tool-call turn has empty Content; its weight lives entirely in
	// ToolCalls.Arguments. Estimation must not treat it as ~0 tokens.
	bigArgs := map[string]any{"query": strings.Repeat("y", 2000)}
	msgs := []port.LLMMessage{
		{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "x", Name: "search", Arguments: bigArgs}}},
	}
	est := toEstimate(msgs)
	if len(est[0].Content) < 1000 {
		t.Fatalf("tool-call payload dropped from estimate: content len %d", len(est[0].Content))
	}
}

func TestCompactLoopMessages_NilCompactorBreadcrumb(t *testing.T) {
	out := compactLoopMessages(context.Background(), bigHistory(15), 800, 3, nil)
	found := false
	for _, m := range out {
		if m.Role == "system" && strings.Contains(m.Content, "已省略") {
			found = true
		}
	}
	if !found {
		t.Fatal("nil compactor must still leave a breadcrumb marker")
	}
	assertNoOrphans(t, out)
}

func TestCompactLoopMessages_EnforcesSafetyThreshold(t *testing.T) {
	const budget = 800
	msgs := []port.LLMMessage{sys("s"), usr("u")}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, asst(strings.Repeat("x", 360)))
	}
	out := compactLoopMessages(context.Background(), msgs, budget, 5, nil)
	wantMax := int(float64(budget) * constants.LoopCompactionSafetyRatio)
	if got := tokenutil.EstimateMessages(toEstimate(out)); got > wantMax {
		t.Fatalf("compacted estimate = %d, want <= safety threshold %d", got, wantMax)
	}
	assertNoOrphans(t, out)
}

func TestCompactLoopMessages_EvictsTailWhenNoMiddleExists(t *testing.T) {
	msgs := []port.LLMMessage{sys("s"), usr("u")}
	for i := 0; i < constants.LoopCompactionRecentGroups; i++ {
		msgs = append(msgs, toolTurn(string(rune('a'+i)), strings.Repeat("x", 800))...)
	}

	out := compactLoopMessages(context.Background(), msgs, 800, constants.LoopCompactionRecentGroups, nil)
	if len(out) >= len(msgs) {
		t.Fatalf("expected oversized recent tail to shrink, got %d messages from %d", len(out), len(msgs))
	}
	assertNoOrphans(t, out)
}

func TestCompactLoopMessages_BoundsOversizedProtectedAnchors(t *testing.T) {
	const budget = 800
	msgs := []port.LLMMessage{
		sys(strings.Repeat("system", 1000)),
		usr(strings.Repeat("user", 1000)),
		asst(strings.Repeat("history", 500)),
	}

	out := compactLoopMessages(context.Background(), msgs, budget, 3, nil)
	wantMax := int(float64(budget) * constants.LoopCompactionSafetyRatio)
	if got := tokenutil.EstimateMessages(toEstimate(out)); got > wantMax {
		t.Fatalf("protected estimate = %d, want <= safety threshold %d", got, wantMax)
	}
	if len(out) < 2 || out[0].Role != "system" || out[1].Role != "user" {
		t.Fatalf("protected anchor roles were lost: %+v", out)
	}
}

func TestCompactLoopMessages_TruncatesSystemBeforeCurrentRequest(t *testing.T) {
	current := strings.Repeat("task", 120)
	out := compactLoopMessages(context.Background(), []port.LLMMessage{
		sys(strings.Repeat("system", 400)),
		usr(current),
	}, 300, 0, nil)

	foundCurrent := false
	for _, message := range out {
		if message.Role == "user" && message.Content == current {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("current request was truncated before system prompt: %+v", out)
	}
}

// TestCompactLoop_CooldownSuppressesRepeat 覆盖 Spec 第 4 节压缩冷却：首次超限
// 触发同步摘要并回写 LastCompactionAt；冷却窗口内再次超限只走截断兜底，
// 不得重复调用 LLM compactor（一次执行压缩最多一次）。
func TestCompactLoop_CooldownSuppressesRepeat(t *testing.T) {
	in := bigHistory(15)
	fc := &fakeCompactor{summary: "SUMMARY"}
	state := ReActState{
		CompactionCooldownSec: int(constants.DefaultCompactionCooldown.Seconds()),
	}
	budget := Budget{HistoryCap: 800}
	ctx := context.Background()

	out1 := compactLoopMessagesWithPolicy(ctx, in, budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	if fc.callCount != 1 {
		t.Fatalf("first over-budget step must compact; compactor called %d times, want 1", fc.callCount)
	}
	if state.LastCompactionAt.IsZero() {
		t.Fatal("actual compaction must stamp LastCompactionAt")
	}
	assertNoOrphans(t, out1)

	// 冷却窗口内再次超限：不得再次调用 LLM compactor，走截断兜底。
	out2 := compactLoopMessagesWithPolicy(ctx, bigHistory(15), budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	if fc.callCount != 1 {
		t.Fatalf("cooldown must suppress repeat compaction; compactor called %d times, want 1", fc.callCount)
	}
	assertNoOrphans(t, out2)
	if len(out2) >= len(bigHistory(15)) {
		t.Fatalf("cooldown path must still bound messages (truncation fallback), got %d", len(out2))
	}
}

// TestCompactLoop_CooldownExpiredAllowsAgain 覆盖冷却窗口过期后的重新触发：
// 距上次压缩超过窗口后，超限步骤允许再次调用 LLM compactor。
func TestCompactLoop_CooldownExpiredAllowsAgain(t *testing.T) {
	fc := &fakeCompactor{summary: "SUMMARY"}
	state := ReActState{
		CompactionCooldownSec: int(constants.DefaultCompactionCooldown.Seconds()),
	}
	budget := Budget{HistoryCap: 800}
	ctx := context.Background()

	compactLoopMessagesWithPolicy(ctx, bigHistory(15), budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	if fc.callCount != 1 {
		t.Fatalf("first compaction must trigger; compactor called %d times, want 1", fc.callCount)
	}

	// 冷却窗口已过期：再次超限允许重新触发同步摘要。
	state.LastCompactionAt = time.Now().Add(-constants.DefaultCompactionCooldown - time.Second)
	compactLoopMessagesWithPolicy(ctx, bigHistory(15), budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	if fc.callCount != 2 {
		t.Fatalf("expired cooldown must allow compaction; compactor called %d times, want 2", fc.callCount)
	}
}

// TestCompactLoop_CooldownZeroDisabled 固定 0 = 冷却禁用的契约：与旧行为一致，
// 每次超限都调用 compactor（截断/摘要路径不受冷却约束）。
func TestCompactLoop_CooldownZeroDisabled(t *testing.T) {
	fc := &fakeCompactor{summary: "SUMMARY"}
	state := ReActState{}
	budget := Budget{HistoryCap: 800}
	ctx := context.Background()

	compactLoopMessagesWithPolicy(ctx, bigHistory(15), budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	compactLoopMessagesWithPolicy(ctx, bigHistory(15), budget, 0, 3, 1, 1.0, constants.LoopCompactionSafetyRatio, fc, &state)
	if fc.callCount != 2 {
		t.Fatalf("zero cooldown must keep legacy behavior; compactor called %d times, want 2", fc.callCount)
	}
}
