package capgateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
)

type deadlineRecordingGateway struct {
	deadline time.Time
}

func (g *deadlineRecordingGateway) Route(ctx context.Context, _ port.CapabilityRequest) (port.CapabilityResponse, error) {
	g.deadline, _ = ctx.Deadline()
	return port.CapabilityResponse{Content: "summary"}, nil
}

func TestLLMHistoryCompactor_UsesIndependentShortDeadline(t *testing.T) {
	gw := &deadlineRecordingGateway{}
	compactor := NewLLMHistoryCompactor(gw, "qwen", nil, 0, "", 0)
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := compactor.CompactHistory(parent, []port.LLMMessage{{Role: "user", Content: "history"}})
	require.NoError(t, err)
	require.False(t, gw.deadline.IsZero())
	// 主尝试的 deadline = 首个时间片（总预算/尝试数），非完整预算。
	firstSlice := constants.CompactionBudgetTotal / (1 + constants.CompactionMaxCandidates)
	require.WithinDuration(t, time.Now().Add(firstSlice), gw.deadline, time.Second)
}

// recordedCall 记录一次 Route 调用的 ctx deadline 与请求字段。
type recordedCall struct {
	deadline time.Time
	req      port.CapabilityRequest
}

// scriptedCompactorGateway 是可编排的 CapabilityGateway 桩：results 逐次消费，
// 非 nil 元素作为该次调用错误返回，耗尽后返回成功。
type scriptedCompactorGateway struct {
	calls   []recordedCall
	results []error
}

func (g *scriptedCompactorGateway) Route(
	ctx context.Context, req port.CapabilityRequest,
) (port.CapabilityResponse, error) {
	deadline, _ := ctx.Deadline()
	g.calls = append(g.calls, recordedCall{deadline: deadline, req: req})
	if len(g.results) > 0 {
		err := g.results[0]
		g.results = g.results[1:]
		if err != nil {
			return port.CapabilityResponse{}, err
		}
	}
	return port.CapabilityResponse{Content: "summary"}, nil
}

// TestCompactHistory_BudgetSlices 验证 Spec 第 4 节时间片：
// 每次尝试 slice = remaining / remaining_attempts，各自独立 ctx；
// 请求携带 NoPrimaryRetry=true、MaxCandidates=2。
func TestCompactHistory_BudgetSlices(t *testing.T) {
	gw := &scriptedCompactorGateway{results: []error{errors.New("route: 503 unavailable")}}
	compactor := NewLLMHistoryCompactor(gw, "qwen", nil, 0, "", 0)

	_, err := compactor.CompactHistory(context.Background(), []port.LLMMessage{{Role: "user", Content: "history"}})
	require.NoError(t, err)
	require.Len(t, gw.calls, 2)

	llm := gw.calls[0].req.LLM
	require.NotNil(t, llm, "LLM 请求必须携带")
	require.True(t, llm.NoPrimaryRetry, "压缩路径禁止主模型立即重试")
	require.Equal(t, constants.CompactionMaxCandidates, llm.MaxCandidates, "候选上限取常量")

	// 第一次尝试：5s/3 ≈ 1.667s；第二次：剩余(≈5s)/2 ≈ 2.5s。
	firstSlice := constants.CompactionBudgetTotal / 3
	secondSlice := constants.CompactionBudgetTotal / 2
	require.WithinDuration(t, time.Now().Add(firstSlice), gw.calls[0].deadline, 500*time.Millisecond)
	require.WithinDuration(t, time.Now().Add(secondSlice), gw.calls[1].deadline, 500*time.Millisecond)
}

// TestCompactHistory_BudgetExhaustedFailsFast 验证链耗尽快速失败：
// 1 主 + 2 候选全部瞬态失败时，总耗时不超过预算（+容差），错误原样上抛。
func TestCompactHistory_BudgetExhaustedFailsFast(t *testing.T) {
	gw := &scriptedCompactorGateway{results: []error{
		errors.New("primary: 503 unavailable"),
		errors.New("cand-a: 500 internal"),
		errors.New("cand-b: 502 bad gateway"),
	}}
	compactor := NewLLMHistoryCompactor(gw, "qwen", nil, 0, "", 0)

	start := time.Now()
	_, err := compactor.CompactHistory(context.Background(), []port.LLMMessage{{Role: "user", Content: "history"}})
	require.Error(t, err)
	// 恰好尝试 1 主 + MaxCandidates 候选。
	require.Len(t, gw.calls, 1+constants.CompactionMaxCandidates)
	require.LessOrEqual(t, time.Since(start), constants.CompactionBudgetTotal+500*time.Millisecond)
	// 时间片逐次独立：链耗尽不是父 ctx 超时。
	require.NotErrorIs(t, err, context.DeadlineExceeded)
}

// permanentMarkerErr 模拟 llmgateway 的 permanent 错误（跨包鸭子类型探测协议）。
type permanentMarkerErr struct{ err error }

func (e *permanentMarkerErr) Error() string   { return e.err.Error() }
func (e *permanentMarkerErr) Unwrap() error   { return e.err }
func (e *permanentMarkerErr) Permanent() bool { return true }

// contextLengthMarkerErr 模拟 llmgateway 的 context_length_exceeded 标记。
type contextLengthMarkerErr struct{ err error }

func (e *contextLengthMarkerErr) Error() string               { return e.err.Error() }
func (e *contextLengthMarkerErr) Unwrap() error               { return e.err }
func (e *contextLengthMarkerErr) ContextLengthExceeded() bool { return true }

// TestCompactHistory_PermanentOrContextLengthStopsChain 验证 duck-typing 标记
// 探测：永久/上下文超限错误立即停链，不消费候选时间片。
func TestCompactHistory_PermanentOrContextLengthStopsChain(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "permanent marker", err: &permanentMarkerErr{err: errors.New("chain exhausted")}},
		{name: "context length exceeded marker", err: &contextLengthMarkerErr{err: errors.New("context length exceeded")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := &scriptedCompactorGateway{results: []error{tc.err}}
			compactor := NewLLMHistoryCompactor(gw, "qwen", nil, 0, "", 0)

			_, err := compactor.CompactHistory(context.Background(), []port.LLMMessage{{Role: "user", Content: "history"}})
			require.ErrorIs(t, err, tc.err)
			require.Len(t, gw.calls, 1, "永久错误必须立即停链，不进入下一时间片")
		})
	}
}

// TestCompactionSlice 验证时间片计算的剩余 0/负边界（Spec 成功准则 6）。
func TestCompactionSlice(t *testing.T) {
	even := constants.CompactionBudgetTotal / 3
	cases := []struct {
		name         string
		remaining    time.Duration
		attemptsLeft int
		want         time.Duration
	}{
		{name: "even split", remaining: constants.CompactionBudgetTotal, attemptsLeft: 3, want: even},
		{name: "zero remaining", remaining: 0, attemptsLeft: 2, want: constants.CompactionMinSlice},
		{name: "negative", remaining: -time.Second, attemptsLeft: 1, want: constants.CompactionMinSlice},
		{name: "sub-division", remaining: time.Nanosecond, attemptsLeft: 2, want: constants.CompactionMinSlice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, compactionSlice(tc.remaining, tc.attemptsLeft))
		})
	}
}

// TestCompactHistory_UsesBuiltinCompactionPrompt 验证压缩指令走内置常量
// （mechanism 移除后为唯一权威）。
func TestCompactHistory_UsesBuiltinCompactionPrompt(t *testing.T) {
	gw := &scriptedCompactorGateway{}
	compactor := NewLLMHistoryCompactor(gw, "qwen", nil, 0, "", 0)
	msgs := []port.LLMMessage{{Role: "user", Content: "history"}}

	if _, err := compactor.CompactHistory(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	fallback := gw.calls[0].req.LLM.Messages[0].Content
	if !strings.Contains(fallback, "对话历史压缩器") {
		t.Fatalf("fallback compaction prompt missing: %q", fallback)
	}
}

// TestRenderConversation_ToolPairFormat 验证 D9 配对渲染：工具调用对
// （assistant + ToolCalls 与其 tool 结果）渲染为 [Tool] name(args) → result，
// 工具名与参数不再丢失；未配对的 tool 结果以 [Tool result] 行保留，普通
// 消息保持 Role: Content 格式。
func TestRenderConversation_ToolPairFormat(t *testing.T) {
	tests := []struct {
		name string
		in   []port.LLMMessage
		want string
	}{
		{
			name: "普通消息按角色渲染",
			in:   []port.LLMMessage{{Role: "user", Content: "你好"}},
			want: "User: 你好\n",
		},
		{
			name: "工具调用对渲染为配对格式",
			in: []port.LLMMessage{
				{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "1+1"}}}},
				{Role: "tool", Content: "2", ToolCallID: "c1"},
			},
			want: "[Tool] calc({\"expr\":\"1+1\"}) → 2\n",
		},
		{
			name: "未配对 tool 结果保留为孤儿行",
			in:   []port.LLMMessage{{Role: "tool", Content: "orphan result", ToolCallID: "c9"}},
			want: "[Tool result] orphan result\n",
		},
		{
			name: "空参数工具调用输出空括号",
			in: []port.LLMMessage{
				{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "c2", Name: "ping", Arguments: map[string]any{}}}},
				{Role: "tool", Content: "pong", ToolCallID: "c2"},
			},
			want: "[Tool] ping() → pong\n",
		},
		{
			name: "多条工具调用顺序输出",
			in: []port.LLMMessage{
				{Role: "assistant", ToolCalls: []port.ToolCall{
					{ID: "c1", Name: "a", Arguments: map[string]any{"x": "1"}},
					{ID: "c2", Name: "b", Arguments: map[string]any{"y": "2"}},
				}},
				{Role: "tool", Content: "A", ToolCallID: "c1"},
				{Role: "tool", Content: "B", ToolCallID: "c2"},
			},
			want: "[Tool] a({\"x\":\"1\"}) → A\n[Tool] b({\"y\":\"2\"}) → B\n",
		},
		{
			name: "调用与结果分离仍正确配对",
			in: []port.LLMMessage{
				{Role: "assistant", ToolCalls: []port.ToolCall{{ID: "c1", Name: "lookup", Arguments: map[string]any{"k": "v"}}}},
				{Role: "user", Content: "continue"},
				{Role: "tool", Content: "found", ToolCallID: "c1"},
			},
			want: "[Tool] lookup({\"k\":\"v\"}) → found\nUser: continue\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, renderConversation(tc.in))
		})
	}
}
