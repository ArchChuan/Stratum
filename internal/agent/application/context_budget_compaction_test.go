package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
				"你是助手", "", "", makeHistory(tc.historyLen), "当前问题",
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

// TestBuildContextMessages_UntrustedWrappers 验证 memory context 与压缩摘要
// 在注入 system 消息时被 <untrusted_memory> / <untrusted_history> 结构标签
// 包裹——这是防「指令洗白」的结构性标记：untrusted 内容升格为 system 级
// 指令前必须有明确边界。
func TestBuildContextMessages_UntrustedWrappers(t *testing.T) {
	fc := &fakeCompactor{summary: "被逐出的历史摘要"}
	// 窗口取大：小预算下 memory 会被 fitSystemAndMemory 的 head 配额截空，
	// 无法验证 <untrusted_memory> 打标；这里保证 memory 与摘要都能注入。
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", "记忆内容", makeHistory(30), "当前问题",
		40000, 5, 0, 0, fc,
	)
	sys, hasSys := systemContent(msgs)
	require.True(t, hasSys, "system message expected")

	// 摘要位于 <untrusted_history> 内。
	require.Contains(t, sys, "<untrusted_history>")
	require.Contains(t, sys, "被逐出的历史摘要")
	require.Contains(t, sys, "</untrusted_history>")

	// memory 位于 <untrusted_memory> 内。
	require.Contains(t, sys, "<untrusted_memory>")
	require.Contains(t, sys, "记忆内容")
	require.Contains(t, sys, "</untrusted_memory>")

	// 标签有序且不重叠：history 先于 memory，各自闭合。
	idxHistory := indexOf(sys, "<untrusted_history>")
	idxHistoryEnd := indexOf(sys, "</untrusted_history>")
	idxMemory := indexOf(sys, "<untrusted_memory>")
	idxMemoryEnd := indexOf(sys, "</untrusted_memory>")
	require.True(t, idxHistory < idxHistoryEnd && idxHistoryEnd <= idxMemory && idxMemory < idxMemoryEnd,
		"wrapper 顺序或闭合异常: %s", sys)
}

func indexOf(s, sub string) int {
	idx := strings.Index(s, sub)
	if idx < 0 {
		return -1
	}
	return idx
}

// TestCompaction_BackwardCompatible 保证：nil compactor 时与压缩失败降级
// 路径逐条一致。窗口取 40000，避免小窗口被自动链输出预留（4096）吞成 0 usable。
func TestCompaction_BackwardCompatible(t *testing.T) {
	hist := makeHistory(30)
	legacy := application.BuildContextMessages("sys", "mem", hist, "q", 40000, 5)
	viaCompaction := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", "mem", hist, "q", 40000, 5, 0, 0, nil,
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

// 提取注入的摘要正文（marker 之后、system 结尾之前）。摘要被
// <untrusted_history> 结构标签包裹，这里剥掉尾标签，使字节断言与
// "摘要体截断到预留额度"的语义一致。
func summaryBody(sys string) string {
	idx := strings.Index(sys, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSuffix(sys[idx+len(marker):], "\n</untrusted_history>")
}

// TestCompaction_SummaryReserveScalesWithBudget 验证摘要预留额度从固定
// min(budget/4, 400) 改为 HistoryCap 的 5% 联动（300000 窗口，默认 safety
// 0.2 → usable 235904 → HistoryCap 141540 − 任务 4 = 141536 → 7077 tokens
// 预留）：超大摘要被精确截断到预留额度，且明显大于旧逻辑的 400-token 上限。
// outputReserve 传 0 走自动链常量 4096。
func TestCompaction_SummaryReserveScalesWithBudget(t *testing.T) {
	const maxTokens = 300000
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", "", makeHistory(30), "当前问题",
		maxTokens, 5, 0, 0, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// usable = 300000 − 60000 − 4096 = 235904; fixedHead/tools 各 47180;
	// HistoryCap = 235904 − 47180 − 47180 − task(4) = 141540;
	// reserve = 5% = 7077 tokens. The whole summary (marker included) is
	// truncated to the reserve, so the marker-stripped body must be
	// 7077*3 − len(marker) bytes.
	const wantBytes = 7077 * 3
	if got := len(summaryBody(sys)) + len(marker); got != wantBytes {
		t.Fatalf("summary reserve = %d bytes, want %d (5%% of HistoryCap)", got, wantBytes)
	}
}

// TestCompaction_SummaryReserveCappedAtBudget 验证小窗口下预留额度被 cap 于
// history 配额而非固定 200-token floor：maxTokens=400 + 预留 50 时
// HistoryCap 只剩 162，扣任务 4 后为 158，预留被 cap 到全部 158。
func TestCompaction_SummaryReserveCappedAtBudget(t *testing.T) {
	const maxTokens = 400
	const outputReserve = 50
	fc := &fakeCompactor{summary: marker + strings.Repeat("摘", 20000)}
	msgs := application.BuildContextMessagesWithCompaction(
		context.Background(), "你是助手", "", "", makeHistory(30), "当前问题",
		maxTokens, 5, outputReserve, 0, fc,
	)
	if fc.callCount == 0 {
		t.Fatal("expected compactor to be invoked on overflow history")
	}
	sys, hasSys := systemContent(msgs)
	if !hasSys || !strings.Contains(sys, marker) {
		t.Fatalf("summary not injected: %q", sys)
	}
	// usable = 400 − 80 − 50 = 270; HistoryCap = 270 − 54 − 54 − task(4) = 158;
	// reserve = min(5% floor 200, 158) = 158 tokens, capped at the history quota.
	const wantBytes = 158 * 3
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
		context.Background(), "sys", "", "mem", hist, "q", 500, 50, 50, 0, nil,
	)
	got := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", "mem", hist, "q", 500, 50, 50, 0,
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
// usable 耗尽时（显式小窗口 2000、显式 aggressive ratio 0.8、大任务超窗），
// 初始组装仍发送最小 head（system prompt + 当前输入，memory 能装下才带），
// 绝不丢弃 system prompt；超窗发送交由规格收敛机制（400
// context_length_exceeded → TokenCorrection 下调阈值）处理。
// 默认 safety ratio 0.2 下 fallback 8000 窗口 usable 仍为正（2304），
// 走正常组装路径且 memory 注入；断言"只发输入"就是在断言回归本身。
func TestBuildContextMessages_DegradedUsableKeepsMinimalHead(t *testing.T) {
	cases := []struct {
		name        string
		maxTokens   int
		ratio       float64
		input       string
		wantMinimal bool // 期望最小 head（恰 2 条）
		wantMem     bool
	}{
		// 默认 ratio 0.2：usable = 8000 − 1600 − 4096 = 2304 > 0，headCap 460
		// 内 system + memory 都能装下（修复前默认 0.8 时 usable 钳 0 属退化）。
		{name: "fallback 8000 默认 ratio 正常组装", maxTokens: 8000, input: "当前问题", wantMinimal: false, wantMem: true},
		// 显式 aggressive ratio 0.8（registry 配置被尊重，不隐式纠正）：
		// usable 钳 0 → 最小 head，memory 装不下丢弃。
		{name: "8000 + 显式 0.8 退化保 head", maxTokens: 8000, ratio: 0.8, input: "当前问题", wantMinimal: true, wantMem: false},
		{name: "显式 2000 窗口", maxTokens: 2000, input: "当前问题", wantMinimal: true, wantMem: false},
		// 大任务超窗（显式 0.8：usable 3904 < task 5000）但头部配额充足：
		// memory 能装下则保留。
		{name: "大任务超窗且 memory 能装下", maxTokens: 40000, ratio: 0.8,
			input: strings.Repeat("x", 15000), wantMinimal: true, wantMem: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := application.BuildContextMessagesWithCompaction(
				context.Background(),
				"你是助手", "", "记忆内容", makeHistory(5), tc.input,
				tc.maxTokens, 5, 0, tc.ratio, nil,
			)
			if tc.wantMinimal {
				if len(msgs) != 2 {
					t.Fatalf("最小 head 应恰为 system + user 两条，实际 %d 条: %+v", len(msgs), msgs)
				}
				if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "你是助手") {
					t.Fatalf("system prompt 被丢弃：msgs[0] = %+v", msgs[0])
				}
				if msgs[1].Role != "user" || msgs[1].Content != tc.input {
					t.Fatalf("末条应为当前输入 user，实际 %+v", msgs[1])
				}
			} else if len(msgs) <= 2 {
				t.Fatalf("正常组装应含历史，实际 %d 条: %+v", len(msgs), msgs)
			}
			sys := msgs[0].Content
			if got := strings.Contains(sys, "记忆内容"); got != tc.wantMem {
				t.Errorf("memory 保留 = %v，期望 %v（system=%q）", got, tc.wantMem, sys)
			}
		})
	}
}

// TestBuildContextMessages_TaskDeductedFromHistoryQuota（I3）：当前任务
// （输入）的 token 从 history 配额扣减（Spec 第 2 节
// history = usable − fixedHead − tools − task）——同窗口下任务越大，
// 可保留的历史越少。
func TestBuildContextMessages_TaskDeductedFromHistoryQuota(t *testing.T) {
	hist := makeHistory(30) // ≈4950 tokens，两种任务下都超预算
	small := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", "", hist, "q", 5000, 50, 500, 0, nil,
	)
	large := application.BuildContextMessagesWithCompaction(
		context.Background(), "sys", "", "", hist, strings.Repeat("x", 600), 5000, 50, 500, 0, nil,
	)
	// usable = 5000 − 4000 − 500 = 500；HistoryCap = 500−100−100−task。
	// "q" → task 1 → 299（保留 1 条 165t 历史）；600 字节 → task 200 → 99（全丢）。
	if len(small) <= len(large) {
		t.Fatalf("任务越大 history 配额越小：small=%d large=%d", len(small), len(large))
	}
}

// TestBuildContextMessages_GlobalSuffixExemptFromHeadCap 回归防护：全局系统
// 提示词（平台级治理指令，如"事实与引用"四条款）豁免 FixedHeadCap——
// 预算紧张时 systemPromptBase 被截断保头，但 globalSuffix 必须完整保留且
// 紧跟截断后的 base。修复前 globalSuffix 与 base 拼接后整体截断，
// 尾部治理指令被静默丢弃。
func TestBuildContextMessages_GlobalSuffixExemptFromHeadCap(t *testing.T) {
	const suffix = "【全局后缀】事实与引用条款完整内容"
	longBase := strings.Repeat("基础指令", 600) // ≈2400 字符，远超任意 headCap

	cases := []struct {
		name      string
		maxTokens int
		ratio     float64
	}{
		// usable 钳 0 → minimal head 路径（system + user 恰 2 条）。
		{name: "minimal head 路径", maxTokens: 2000},
		// usable > 0 但头部配额紧张（aggressive 0.8）→ 正常组装路径截断 base。
		{name: "正常组装预算紧张", maxTokens: 8000, ratio: 0.8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := application.BuildContextMessagesWithCompaction(
				context.Background(), longBase, suffix, "记忆", makeHistory(0), "当前问题",
				tc.maxTokens, 5, 0, tc.ratio, nil,
			)
			if len(msgs) != 2 {
				t.Fatalf("应恰为 system + user 两条，实际 %d 条", len(msgs))
			}
			sys := msgs[0].Content
			if !strings.Contains(sys, suffix) {
				t.Fatalf("globalSuffix 未完整保留：system=%q", sys)
			}
			// 完整后缀位于截断后的 base 之后（有前缀内容 + 分隔符），
			// 且 base 被截断而非整体丢弃。
			if idx := strings.Index(sys, suffix); idx <= 0 {
				t.Fatalf("globalSuffix 应紧跟截断后的 base，实际 idx=%d system=%q", idx, sys)
			}
		})
	}
}
