package graph

import "github.com/byteBuilderX/stratum/pkg/constants"

// Budget 是一次执行的上下文预算账本（Spec 第 2 节）：window → usable →
// 四配额。一次执行一个快照，初始组装与 ReAct 循环共享同一来源。
type Budget struct {
	Window       int // 阶段 B 解析结果（1M ceiling 已 clamp）
	Usable       int // window − safetyReserve − outputReserve
	FixedHeadCap int // system + memory 配额（20% usable）
	ToolsCap     int // 工具定义配额（20% usable）
	HistoryCap   int // 可压缩区（= usable − fixedHead − tools − task）
}

// ComputeBudget 计算执行预算。safetyRatio 是 registry 参数
// agent.compaction_safety_ratio（0 = 用 constants 默认）。
// outputReserve 是主模型输出预留（Task 3 的 maxOut / 显式 max_tokens / 常量）。
func ComputeBudget(window, outputReserve int, safetyRatio float64) Budget {
	if window > constants.MaxContextWindowTokens {
		window = constants.MaxContextWindowTokens
	}
	ratio := safetyRatio
	if ratio <= 0 || ratio >= 1 {
		ratio = constants.LoopCompactionSafetyRatio
	}
	usable := window - int(float64(window)*ratio) - outputReserve
	if usable < 0 {
		usable = 0
	}
	fixedHead := int(float64(usable) * constants.DefaultFixedHeadRatio)
	tools := int(float64(usable) * constants.DefaultToolsBudgetRatio)
	history := usable - fixedHead - tools
	return Budget{
		Window: window, Usable: usable,
		FixedHeadCap: fixedHead, ToolsCap: tools, HistoryCap: history,
	}
}
