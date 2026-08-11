package graph

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// TestComputeBudget 验证账本核心计算（Spec 第 2 节）：
// window → usable（= window − safetyReserve − outputReserve）→ 四配额；
// fixedHead/tools 各 ≤ 20% usable，history = usable − fixedHead − tools。
func TestComputeBudget(t *testing.T) {
	cases := []struct {
		name          string
		window        int
		reserve       int
		ratio         float64
		wantUsable    int
		wantFixedHead int
		wantTools     int
	}{
		{
			name: "qwen-max window",
			// int(131072×0.8) = 104857 → usable = 131072 − 104857 − 8192 = 18023
			window: 131072, reserve: 8192, ratio: 0.8,
			wantUsable:    18023,
			wantFixedHead: 3604,
			wantTools:     3604,
		},
		{
			name: "large window",
			// usable = 200000 − 160000 − 8192 = 31808；fixedHead = 20% = 6361
			window: 200000, reserve: 8192, ratio: 0.8,
			wantUsable:    31808,
			wantFixedHead: 6361,
			wantTools:     6361,
		},
		{
			name: "custom safety ratio",
			// ratio 0.9 → usable = 100000 − 90000 − 0 = 10000
			window: 100000, reserve: 0, ratio: 0.9,
			wantUsable:    10000,
			wantFixedHead: 2000,
			wantTools:     2000,
		},
		{
			name: "zero reserve",
			// reserve 0 时 usable = window − safety，不额外扣减
			window: 10000, reserve: 0, ratio: 0.8,
			wantUsable:    2000,
			wantFixedHead: 400,
			wantTools:     400,
		},
		{
			name: "tiny window degrades to zero usable",
			// 8000 fallback 窗口 + 默认 reserve 4096：safety 6400 + reserve 4096
			// 超过窗口 → usable 钳到 0，所有配额为 0（压缩/工具裁剪随之禁用）。
			window: 8000, reserve: 4096, ratio: 0.8,
			wantUsable: 0, wantFixedHead: 0, wantTools: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := ComputeBudget(tc.window, tc.reserve, tc.ratio)
			if b.Window != tc.window {
				t.Errorf("Window = %d, want %d", b.Window, tc.window)
			}
			if b.Usable != tc.wantUsable {
				t.Errorf("Usable = %d, want %d", b.Usable, tc.wantUsable)
			}
			if b.FixedHeadCap != tc.wantFixedHead {
				t.Errorf("FixedHeadCap = %d, want %d", b.FixedHeadCap, tc.wantFixedHead)
			}
			if b.ToolsCap != tc.wantTools {
				t.Errorf("ToolsCap = %d, want %d", b.ToolsCap, tc.wantTools)
			}
			// 不变量：history = usable − fixedHead − tools（taskHint 由
			// WithTask 单列扣减，ComputeBudget 不含任务输入）
			wantHistory := b.Usable - b.FixedHeadCap - b.ToolsCap
			if b.HistoryCap != wantHistory {
				t.Errorf("HistoryCap = %d, want %d (= usable − fixedHead − tools)", b.HistoryCap, wantHistory)
			}
			// 配额比例硬约束：fixedHead ≤ 20% usable、tools ≤ 20% usable
			ratioCap := int(float64(b.Usable) * constants.DefaultFixedHeadRatio)
			if b.FixedHeadCap > ratioCap || b.ToolsCap > ratioCap {
				t.Errorf("quota exceeded 20%% usable: FixedHeadCap=%d ToolsCap=%d cap=%d",
					b.FixedHeadCap, b.ToolsCap, ratioCap)
			}
		})
	}
}

// TestComputeBudget_ClampsAboveCeiling 验证 1M 硬 ceiling 在账本内 enforce：
// 超过 MaxContextWindowTokens 的窗口被钳到上限再算配额。
func TestComputeBudget_ClampsAboveCeiling(t *testing.T) {
	const over = constants.MaxContextWindowTokens * 2
	b := ComputeBudget(over, 0, 0.8)
	if b.Window != constants.MaxContextWindowTokens {
		t.Errorf("Window = %d, want clamped %d", b.Window, constants.MaxContextWindowTokens)
	}
	// int(1048576×0.8) = 838860 → usable = 1048576 − 838860 = 209716
	if b.Usable != 209716 {
		t.Errorf("Usable = %d, want %d (computed on clamped window)", b.Usable, 209716)
	}
}

// TestComputeBudget_InvalidRatioFallsBackToDefault 验证 safetyRatio 越界
// （≤0 或 ≥1）时回退 constants 默认，与 compactionThreshold 的归一化同语义。
func TestComputeBudget_InvalidRatioFallsBackToDefault(t *testing.T) {
	const window = 100000
	// 默认 0.8 → usable = 100000 − 80000 = 20000
	const wantUsable = 20000
	for _, ratio := range []float64{0, -1, 1, 1.5} {
		b := ComputeBudget(window, 0, ratio)
		if b.Usable != wantUsable {
			t.Errorf("ratio %v: Usable = %d, want default-ratio %d", ratio, b.Usable, wantUsable)
		}
	}
}

// TestBudget_WithTaskDeductsHistoryQuota 验证任务扣减（I3）：WithTask 登记
// 当前任务的 token 估算，并按其从 history 配额扣减（Spec 第 2 节
// history = usable − fixedHead − tools − task），其余配额与窗口映射不变。
func TestBudget_WithTaskDeductsHistoryQuota(t *testing.T) {
	b := ComputeBudget(100000, 0, 0.8)
	// usable = 20000；fixedHead/tools 各 4000；HistoryCap = 12000。
	if b.HistoryCap != 12000 {
		t.Fatalf("HistoryCap = %d, want 12000", b.HistoryCap)
	}
	withTask := b.WithTask(500)
	if withTask.TaskHint != 500 || withTask.HistoryCap != 11500 {
		t.Fatalf("WithTask(500) = %+v, want TaskHint 500 / HistoryCap 11500", withTask)
	}
	if withTask.Usable != b.Usable || withTask.FixedHeadCap != b.FixedHeadCap || withTask.ToolsCap != b.ToolsCap {
		t.Fatalf("WithTask 不应改变窗口映射配额: %+v", withTask)
	}
	// 任务超出可压缩区时钳到 0，不产生负数配额。
	if zero := b.WithTask(50000); zero.HistoryCap != 0 {
		t.Fatalf("超量任务 HistoryCap = %d, want 0", zero.HistoryCap)
	}
	// 负值按 0 处理。
	if neg := b.WithTask(-10); neg.TaskHint != 0 || neg.HistoryCap != 12000 {
		t.Fatalf("负任务按 0 处理: %+v", neg)
	}
	// 值语义：WithTask 返回副本，原账本不被修改。
	if b.TaskHint != 0 || b.HistoryCap != 12000 {
		t.Fatalf("WithTask 必须返回副本: %+v", b)
	}
}

// TestComputeBudget_HistoryIsolation 验证 history 配额是账本预分配
// （= usable − fixedHead − tools 配额），独立于工具定义的实际 token 数：
// ComputeBudget 不含任何工具输入，工具再多也只占 ToolsCap 份额，
// 绝不压垮可压缩区（Spec 第 2 节根因修复）。
func TestComputeBudget_HistoryIsolation(t *testing.T) {
	const window = 100000
	base := ComputeBudget(window, 0, 0.8)
	// 不变量：history 配额 = usable − fixedHead − tools。
	if base.HistoryCap != base.Usable-base.FixedHeadCap-base.ToolsCap {
		t.Fatalf("HistoryCap = %d, want usable − fixedHead − tools = %d",
			base.HistoryCap, base.Usable-base.FixedHeadCap-base.ToolsCap)
	}
	// 工具配额是固定比例（20% usable），与工具列表内容无关。
	if base.ToolsCap != int(float64(base.Usable)*constants.DefaultToolsBudgetRatio) {
		t.Fatalf("ToolsCap = %d, want 20%% usable = %d",
			base.ToolsCap, int(float64(base.Usable)*constants.DefaultToolsBudgetRatio))
	}
	// 同参数重复计算结果恒定：history 配额不随执行内容变化。
	if again := ComputeBudget(window, 0, 0.8); again.HistoryCap != base.HistoryCap {
		t.Fatalf("HistoryCap 不稳定: %d vs %d", again.HistoryCap, base.HistoryCap)
	}
}
