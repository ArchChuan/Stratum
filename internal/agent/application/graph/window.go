package graph

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// WindowSource 标记一次执行窗口解析的最终来源（Spec 第 1 节）。
type WindowSource string

const (
	WindowExplicit    WindowSource = "explicit"     // 管理员显式配置
	WindowRegistry    WindowSource = "registry"     // 模型 registry context_window
	WindowVendorTable WindowSource = "vendor_table" // 内置厂商静态表
	WindowFallback    WindowSource = "fallback"     // 保守默认 8000
)

// ResolveModelWindow 解析模型真实窗口（阶段 A）：
// registry context_window > 0 → vendor 静态表 → 0 = UNKNOWN。
// vendor 通过注入函数访问（wiring 适配 llmgateway），graph 包不跨层依赖。
func ResolveModelWindow(
	ctx context.Context,
	model string,
	provider port.ModelContextProvider,
	vendor func(string) (int, int),
) (window int, source WindowSource) {
	if provider != nil {
		if cw, err := provider.GetChatModelContextWindow(ctx, model); err == nil && cw > 0 {
			return cw, WindowRegistry
		}
	}
	if vendor != nil {
		if cw, _ := vendor(model); cw > 0 {
			return cw, WindowVendorTable
		}
	}
	return 0, WindowFallback
}

// ResolveAgentWindow 解析 agent 执行窗口（阶段 B）。explicit=0 表示未配置。
// clamp 上限 w×0.85 只在模型窗口已知时适用；UNKNOWN 时显式值直接生效
// （D7：显式配置是最可信信息，未知假设无权压制它）。
func ResolveAgentWindow(modelWindow, explicit int) (window int, source WindowSource) {
	known := modelWindow > 0
	switch {
	case explicit > 0 && known:
		window = clampWindow(explicit, int(float64(modelWindow)*constants.DefaultContextWindowRatio))
		return window, WindowExplicit
	case explicit > 0:
		return explicit, WindowExplicit
	case known:
		return int(float64(modelWindow) * constants.DefaultContextWindowRatio), WindowRegistry
	default:
		return constants.DefaultAgentContextTokens, WindowFallback
	}
}

// clampWindow 将显式窗口约束到 [MinContextWindowTokens, ratioCap]。
func clampWindow(explicit, ratioCap int) int {
	if explicit < constants.MinContextWindowTokens {
		return constants.MinContextWindowTokens
	}
	if ratioCap > 0 && explicit > ratioCap {
		return ratioCap
	}
	return explicit
}
