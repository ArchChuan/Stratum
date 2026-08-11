package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// providerFunc 把纯函数适配为 port.ModelContextProvider 桩。
type providerFunc func(context.Context, string, string) (int, error)

func (p providerFunc) GetChatModelContextWindow(ctx context.Context, tenantID, model string) (int, error) {
	return p(ctx, tenantID, model)
}

// okWindow 返回 registry 命中固定窗口的 provider 桩。
func okWindow(cw int) providerFunc {
	return func(context.Context, string, string) (int, error) { return cw, nil }
}

// errWindow 返回 provider 报错的桩。
func errWindow() providerFunc {
	return func(context.Context, string, string) (int, error) { return 0, errors.New("catalog down") }
}

// vendorWindow 返回 vendor 静态表命中固定窗口的桩。
func vendorWindow(cw int) func(string) (int, int) {
	return func(string) (int, int) { return cw, 8192 }
}

// windowRatio 按 DefaultContextWindowRatio 运行时缩放窗口（want 值计算，
// 常量浮点→int 转换在编译期被拒绝，故经运行时变量走截断）。
func windowRatio(win int) int {
	return int(float64(win) * constants.DefaultContextWindowRatio)
}

func TestResolveModelWindow(t *testing.T) {
	cases := []struct {
		name            string
		provider        port.ModelContextProvider
		vendor          func(string) (int, int)
		want            int
		wantSrc         WindowSource
		wantVendorCalls int
	}{
		// registry 命中优先，vendor 不被调用。
		{name: "registry wins", provider: okWindow(200000), vendor: nil,
			want: 200000, wantSrc: WindowRegistry, wantVendorCalls: 0},
		// registry 未知（cw=0）→ 降级 vendor 静态表。
		{name: "vendor table fallback", provider: okWindow(0), vendor: vendorWindow(131072),
			want: 131072, wantSrc: WindowVendorTable, wantVendorCalls: 1},
		// registry 报错 → 与未知等价，降级 vendor。
		{name: "provider error degrades to vendor", provider: errWindow(), vendor: vendorWindow(131072),
			want: 131072, wantSrc: WindowVendorTable, wantVendorCalls: 1},
		// 两侧都未知 → 0 = UNKNOWN，来源 fallback。
		{name: "both unknown", provider: okWindow(0), vendor: vendorWindow(0),
			want: 0, wantSrc: WindowFallback, wantVendorCalls: 1},
		// provider 缺失（nil）时回退链仍尝试 vendor。
		{name: "nil provider", provider: nil, vendor: vendorWindow(0),
			want: 0, wantSrc: WindowFallback, wantVendorCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vendorCalls := 0
			var vendor func(string) (int, int)
			if tc.vendor != nil {
				vendor = func(model string) (int, int) {
					vendorCalls++
					return tc.vendor(model)
				}
			}
			got, src := ResolveModelWindow(context.Background(), "tenant-1", "qwen-max", tc.provider, vendor)
			if got != tc.want {
				t.Fatalf("ResolveModelWindow() window = %d, want %d", got, tc.want)
			}
			if src != tc.wantSrc {
				t.Fatalf("ResolveModelWindow() source = %q, want %q", src, tc.wantSrc)
			}
			if vendorCalls != tc.wantVendorCalls {
				t.Fatalf("vendor called %d times, want %d", vendorCalls, tc.wantVendorCalls)
			}
		})
	}
}

func TestResolveAgentWindow(t *testing.T) {
	cases := []struct {
		name     string
		modelWin int // 0 = UNKNOWN
		explicit int // 0 = 未配置
		want     int
		wantSrc  WindowSource
	}{
		// 显式 + 已知窗口 → clamp 到 [MinContextWindowTokens, w×0.85]。
		{name: "explicit within clamp", modelWin: 200000, explicit: 30000,
			want: 30000, wantSrc: WindowExplicit},
		{name: "explicit above ratio cap clamps", modelWin: 200000, explicit: 200000,
			want: windowRatio(200000), wantSrc: WindowExplicit},
		{name: "explicit below min clamps", modelWin: 131072, explicit: 500,
			want: constants.MinContextWindowTokens, wantSrc: WindowExplicit},
		// 显式 + UNKNOWN 窗口 → 显式原值生效（D7：未知假设无权压制显式配置）。
		{name: "explicit unknown window not clamped", modelWin: 0, explicit: 40000,
			want: 40000, wantSrc: WindowExplicit},
		// 未配置 + 已知窗口 → w×0.85。
		{name: "derived from known window", modelWin: 131072, explicit: 0,
			want: windowRatio(131072), wantSrc: WindowRegistry},
		// 全空 → 保守默认。
		{name: "fallback default", modelWin: 0, explicit: 0,
			want: constants.DefaultAgentContextTokens, wantSrc: WindowFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, src := ResolveAgentWindow(tc.modelWin, tc.explicit)
			if got != tc.want {
				t.Fatalf("ResolveAgentWindow() window = %d, want %d", got, tc.want)
			}
			if src != tc.wantSrc {
				t.Fatalf("ResolveAgentWindow() source = %q, want %q", src, tc.wantSrc)
			}
		})
	}
}
