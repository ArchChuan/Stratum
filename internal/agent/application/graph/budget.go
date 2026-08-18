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
	TaskHint     int // 当前任务（最新用户输入）的 token 估算，WithTask 登记
}

// ComputeBudget 计算执行预算。safetyRatio 是组装侧安全余量比例，传 0 或
// 非法值（≤0 或 ≥1）时回退 constants.ContextSafetyReserveRatio（0.2）——
// 平台默认锁定值，不暴露用户配置。
// outputReserve 是主模型输出预留（Task 3 的 maxOut / 显式 max_tokens / 常量）。
// 任务扣减不在 ComputeBudget 内：TaskHint 由调用侧 WithTask 登记（组装侧
// 是 currentInput，循环侧是最新用户消息），保持 ComputeBudget 纯窗口映射。
func ComputeBudget(window, outputReserve int, safetyRatio float64) Budget {
	if window > constants.MaxContextWindowTokens {
		window = constants.MaxContextWindowTokens
	}
	ratio := safetyRatio
	if ratio <= 0 || ratio >= 1 {
		ratio = constants.ContextSafetyReserveRatio
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

// WithTask 登记当前任务（最新用户输入）的 token 估算并从 history 配额扣减
// （Spec 第 2 节：history = usable − fixedHead − tools − task）。任务永不
// 压缩，其固定成本必须从可压缩区扣除，否则循环阈值高出任务大小、派发总量
// 可超出 usable 一个任务量（I3）。值语义：返回副本，原账本不变。
func (b Budget) WithTask(taskTokens int) Budget {
	if taskTokens < 0 {
		taskTokens = 0
	}
	b.TaskHint = taskTokens
	b.HistoryCap = max(b.Usable-b.FixedHeadCap-b.ToolsCap-taskTokens, 0)
	return b
}
