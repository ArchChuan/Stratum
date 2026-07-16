package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

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
	const marker = "【压缩摘要标记】"

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
