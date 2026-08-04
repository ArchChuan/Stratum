package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// marker 是压缩摘要注入测试共用的标记，需在包级可见（多个测试函数引用）。
const marker = "【压缩摘要标记】"

// fakeCompactor 是测试用的 HistoryCompactor：可注入固定摘要或错误，
// 并记录被传入的消息数，用于断言“哪些历史进了压缩”。
type fakeCompactor struct {
	summary   string
	err       error
	gotMsgs   int
	callCount int
}

func (f *fakeCompactor) CompactHistory(_ context.Context, msgs []port.LLMMessage) (string, error) {
	f.callCount++
	f.gotMsgs = len(msgs)
	return f.summary, f.err
}

func makeHistory(n int) []*application.ChatMessage {
	h := make([]*application.ChatMessage, 0, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// 每条塞较长内容，确保超预算触发丢弃/压缩。
		h = append(h, &application.ChatMessage{
			Role:    role,
			Content: strings.Repeat("对话内容", 40),
		})
	}
	return h
}

func systemContent(msgs []port.LLMMessage) (string, bool) {
	if len(msgs) > 0 && msgs[0].Role == "system" {
		return msgs[0].Content, true
	}
	return "", false
}

func TestBuildContextMessagesWithCompaction(t *testing.T) {
	tests := []struct {
		name           string
		historyLen     int
		maxTokens      int
		window         int
		compactor      *fakeCompactor
		wantSummary    bool // system 中应含摘要标记
		wantCompaction bool // compactor 应被调用
	}{
		{
			name:           "nil compactor 退回纯截断，无摘要",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			compactor:      nil,
			wantSummary:    false,
			wantCompaction: false,
		},
		{
			name:           "溢出历史被压缩并注入 system",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			compactor:      &fakeCompactor{summary: marker},
			wantSummary:    true,
			wantCompaction: true,
		},
		{
			name:           "compactor 出错则降级，不注入摘要",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			compactor:      &fakeCompactor{err: errors.New("llm down")},
			wantSummary:    false,
			wantCompaction: true,
		},
		{
			name:           "空摘要降级，不注入",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			compactor:      &fakeCompactor{summary: ""},
			wantSummary:    false,
			wantCompaction: true,
		},
		{
			name:           "历史很短无溢出，不调用 compactor",
			historyLen:     2,
			maxTokens:      100000,
			window:         50,
			compactor:      &fakeCompactor{summary: marker},
			wantSummary:    false,
			wantCompaction: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c port.HistoryCompactor
			if tc.compactor != nil {
				c = tc.compactor
			}
			msgs := application.BuildContextMessagesWithCompaction(
				context.Background(),
				"你是助手", "", makeHistory(tc.historyLen), "当前问题",
				tc.maxTokens, tc.window, c,
			)

			// 末条永远是当前输入。
			last := msgs[len(msgs)-1]
			if last.Role != "user" || last.Content != "当前问题" {
				t.Fatalf("末条应为当前输入 user，实际 role=%q content=%q", last.Role, last.Content)
			}

			sys, hasSys := systemContent(msgs)
			gotSummary := hasSys && strings.Contains(sys, marker)
			if gotSummary != tc.wantSummary {
				t.Errorf("摘要注入 = %v，期望 %v (system=%q)", gotSummary, tc.wantSummary, sys)
			}

			if tc.compactor != nil {
				called := tc.compactor.callCount > 0
				if called != tc.wantCompaction {
					t.Errorf("compactor 调用 = %v，期望 %v", called, tc.wantCompaction)
				}
			}
		})
	}
}

// TestCompaction_BackwardCompatible 保证：nil compactor 时新旧实现逐条一致。
func TestCompaction_BackwardCompatible(t *testing.T) {
	hist := makeHistory(30)
	legacy := application.BuildContextMessages("sys", "mem", hist, "q", 500, 5)
	viaCompaction := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 500, 5, nil,
	)
	if len(legacy) != len(viaCompaction) {
		t.Fatalf("长度不一致: legacy=%d compaction=%d", len(legacy), len(viaCompaction))
	}
	for i := range legacy {
		if legacy[i].Role != viaCompaction[i].Role || legacy[i].Content != viaCompaction[i].Content {
			t.Errorf("第 %d 条不一致: %+v vs %+v", i, legacy[i], viaCompaction[i])
		}
	}
}

// 提取注入的摘要正文（marker 之后、system 结尾之前）。
func summaryBody(sys string) string {
	idx := strings.Index(sys, marker)
	if idx < 0 {
		return ""
	}
	return sys[idx+len(marker):]
}

// TestCompaction_SummaryReserveScalesWithBudget 验证摘要预留额度从固定
// min(budget/4, 400) 改为 5% 联动（40000 tokens 预算 → 1989 tokens 预留）：
// 超大摘要被精确截断到预留额度，且明显大于旧逻辑的 400-token 上限。
func TestCompaction_SummaryReserveScalesWithBudget(t *testing.T) {
	const maxTokens = 40000
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", makeHistory(30), "当前问题",
		maxTokens, 5, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// budget = 40000 − input(4) − sysReserve(200) = 39796; reserve = 5% = 1989
	// tokens. The whole summary (marker included) is truncated to the reserve,
	// so the marker-stripped body must be 1989*3 − len(marker) bytes.
	const wantBytes = 1989 * 3
	if got := len(summaryBody(sys)) + len(marker); got != wantBytes {
		t.Fatalf("summary reserve = %d bytes, want %d (5%% of remaining budget)", got, wantBytes)
	}
}

// TestCompaction_SummaryReserveCappedAtBudget 验证小窗口下预留额度被 cap 于
// 剩余预算而非固定 200-token floor：maxTokens=400 时剩余 196，旧逻辑只预留
// 49 tokens（budget/4），新逻辑预留全部 196。
func TestCompaction_SummaryReserveCappedAtBudget(t *testing.T) {
	const maxTokens = 400
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", makeHistory(30), "当前问题",
		maxTokens, 5, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// budget = 400 − 4 − 200 = 196; reserve = min(5% floor 200, 196) = 196
	// tokens, capped at the remaining budget.
	const wantBytes = 196 * 3
	if got := len(summaryBody(sys)) + len(marker); got != wantBytes {
		t.Fatalf("summary reserve = %d bytes, want %d (capped at remaining budget)", got, wantBytes)
	}
}

func TestCompaction_FailureRestoresPlainTruncationBudget(t *testing.T) {
	hist := make([]*application.ChatMessage, 10)
	for i := range hist {
		hist[i] = &application.ChatMessage{Role: "user", Content: strings.Repeat("x", 360)}
	}
	want := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 500, 50, nil,
	)
	got := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 500, 50,
		&fakeCompactor{err: errors.New("unavailable")},
	)

	if len(got) != len(want) {
		t.Fatalf("fallback retained %d messages, plain truncation retained %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("fallback message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
