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
		outputReserve  int // 显式预留，避免小窗口被自动链常量 4096 吞掉
		compactor      *fakeCompactor
		wantSummary    bool // system 中应含摘要标记
		wantCompaction bool // compactor 应被调用
	}{
		{
			name:           "nil compactor 退回纯截断，无摘要",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			outputReserve:  50,
			compactor:      nil,
			wantSummary:    false,
			wantCompaction: false,
		},
		{
			name:           "溢出历史被压缩并注入 system",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			outputReserve:  50,
			compactor:      &fakeCompactor{summary: marker},
			wantSummary:    true,
			wantCompaction: true,
		},
		{
			name:           "compactor 出错则降级，不注入摘要",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			outputReserve:  50,
			compactor:      &fakeCompactor{err: errors.New("llm down")},
			wantSummary:    false,
			wantCompaction: true,
		},
		{
			name:           "空摘要降级，不注入",
			historyLen:     30,
			maxTokens:      500,
			window:         5,
			outputReserve:  50,
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
				tc.maxTokens, tc.window, tc.outputReserve, 0, c,
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

// TestCompaction_BackwardCompatible 保证：nil compactor 时与压缩失败降级
// 路径逐条一致。窗口取 40000，避免小窗口被自动链输出预留（4096）吞成 0 usable。
func TestCompaction_BackwardCompatible(t *testing.T) {
	hist := makeHistory(30)
	legacy := application.BuildContextMessages("sys", "mem", hist, "q", 40000, 5)
	viaCompaction := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 40000, 5, 0, 0, nil,
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
// min(budget/4, 400) 改为 HistoryCap 的 5% 联动（300000 窗口 → HistoryCap
// 33544 − 任务 4 = 33540 → 1677 tokens 预留）：超大摘要被精确截断到预留
// 额度，且明显大于旧逻辑的 400-token 上限。outputReserve 传 0 走自动链
// 常量 4096。
func TestCompaction_SummaryReserveScalesWithBudget(t *testing.T) {
	const maxTokens = 300000
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", makeHistory(30), "当前问题",
		maxTokens, 5, 0, 0, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// usable = 300000 − 240000 − 4096 = 55904; fixedHead/tools 各 11180;
	// HistoryCap = 55904 − 11180 − 11180 − task(4) = 33540;
	// reserve = 5% = 1677 tokens. The whole summary (marker included) is
	// truncated to the reserve, so the marker-stripped body must be
	// 1677*3 − len(marker) bytes.
	const wantBytes = 1677 * 3
	if got := len(summaryBody(sys)) + len(marker); got != wantBytes {
		t.Fatalf("summary reserve = %d bytes, want %d (5%% of HistoryCap)", got, wantBytes)
	}
}

// TestCompaction_SummaryReserveCappedAtBudget 验证小窗口下预留额度被 cap 于
// history 配额而非固定 200-token floor：maxTokens=400 + 预留 50 时
// HistoryCap 只剩 18，扣任务 4 后为 14，预留被 cap 到全部 14。
func TestCompaction_SummaryReserveCappedAtBudget(t *testing.T) {
	const maxTokens = 400
	const outputReserve = 50
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", makeHistory(30), "当前问题",
		maxTokens, 5, outputReserve, 0, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// usable = 400 − 320 − 50 = 30; HistoryCap = 30 − 6 − 6 − task(4) = 14;
	// reserve = min(5% floor 200, 14) = 14 tokens, capped at the history quota.
	const wantBytes = 14 * 3
	if got := len(summaryBody(sys)) + len(marker); got != wantBytes {
		t.Fatalf("summary reserve = %d bytes, want %d (capped at history quota)", got, wantBytes)
	}
}

func TestCompaction_FailureRestoresPlainTruncationBudget(t *testing.T) {
	hist := make([]*application.ChatMessage, 10)
	for i := range hist {
		hist[i] = &application.ChatMessage{Role: "user", Content: strings.Repeat("x", 360)}
	}
	want := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 500, 50, 50, 0, nil,
	)
	got := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "mem", hist, "q", 500, 50, 50, 0,
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

// TestBuildContextMessages_DegradedUsableKeepsMinimalHead 回归防护（C1）：
// fallback 窗口（8000，未知模型）与显式小窗口（2000）下 usable 耗尽时，
// 初始组装仍发送最小 head（system prompt + 当前输入，memory 能装下才带），
// 绝不丢弃 system prompt；超窗发送交由规格收敛机制（400
// context_length_exceeded → TokenCorrection 下调阈值）处理。断言"只发输入"
// 就是在断言回归本身。
func TestBuildContextMessages_DegradedUsableKeepsMinimalHead(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
		input     string
		wantMem   bool
	}{
		{name: "fallback 8000 窗口（未知模型）", maxTokens: 8000, input: "当前问题", wantMem: false},
		{name: "显式 2000 窗口", maxTokens: 2000, input: "当前问题", wantMem: false},
		// 大任务超窗（usable 3904 < task 5000）但头部配额充足：memory 能装下则保留。
		{name: "大任务超窗且 memory 能装下", maxTokens: 40000,
			input: strings.Repeat("x", 15000), wantMem: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := application.BuildContextMessagesWithCompaction(
				context.Background(),
				"你是助手", "记忆内容", makeHistory(5), tc.input,
				tc.maxTokens, 5, 0, 0, nil,
			)
			if len(msgs) != 2 {
				t.Fatalf("最小 head 应恰为 system + user 两条，实际 %d 条: %+v", len(msgs), msgs)
			}
			if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "你是助手") {
				t.Fatalf("system prompt 被丢弃：msgs[0] = %+v", msgs[0])
			}
			if msgs[1].Role != "user" || msgs[1].Content != tc.input {
				t.Fatalf("末条应为当前输入 user，实际 %+v", msgs[1])
			}
			if got := strings.Contains(msgs[0].Content, "记忆内容"); got != tc.wantMem {
				t.Errorf("memory 保留 = %v，期望 %v（system=%q）", got, tc.wantMem, msgs[0].Content)
			}
		})
	}
}

// TestBuildContextMessages_SafetyRatioDrivesAssemblyBudget（I1）：组装侧必须
// 使用与 ReAct 循环侧同一 compaction_safety_ratio 来源——非默认 ratio（0.5）
// 放大了 history 配额，同一历史在 0.5 下全部保留、默认 0.8 下被截断；
// 一次执行一个 usable。
func TestBuildContextMessages_SafetyRatioDrivesAssemblyBudget(t *testing.T) {
	hist := makeHistory(200) // ≈33000 tokens，介于 0.8 与 0.5 ratio 的 HistoryCap 之间
	loose := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", hist, "q", 200000, 200, 0, 0.5, nil,
	)
	strict := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", hist, "q", 200000, 200, 0, 0, nil,
	)
	if len(loose) <= len(strict) {
		t.Fatalf("ratio 0.5 应保留更多历史：loose=%d strict=%d", len(loose), len(strict))
	}
	// 0.5 下 200 条历史全部保留（≈33100 < HistoryCap 57543）。
	if len(loose) != len(hist)+2 {
		t.Fatalf("ratio 0.5 应保留全部历史：loose=%d, want %d", len(loose), len(hist)+2)
	}
}

// TestBuildContextMessages_TaskDeductedFromHistoryQuota（I3）：当前任务
// （输入）的 token 从 history 配额扣减（Spec 第 2 节
// history = usable − fixedHead − tools − task）——同窗口下任务越大，
// 可保留的历史越少。
func TestBuildContextMessages_TaskDeductedFromHistoryQuota(t *testing.T) {
	hist := makeHistory(30) // ≈4950 tokens，两种任务下都超预算
	small := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", hist, "q", 5000, 50, 500, 0, nil,
	)
	large := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", hist, strings.Repeat("x", 600), 5000, 50, 500, 0, nil,
	)
	// usable = 5000 − 4000 − 500 = 500；HistoryCap = 500−100−100−task。
	// "q" → task 1 → 299（保留 1 条 165t 历史）；600 字节 → task 200 → 99（全丢）。
	if len(small) <= len(large) {
		t.Fatalf("任务越大 history 配额越小：small=%d large=%d", len(small), len(large))
	}
}
