package graph

// CostBudgetTerminated 是成本预算超限的业务终止标记（Spec 第 3 节）。
// 属业务终止而非错误：返回已产出部分结果，trace terminated_by=cost_budget。
const CostBudgetTerminated = "cost_budget"

// budgetExceeded 报告累计 token 是否超过执行预算（0 = 不设限）。
// 累计值等于上限不触发终止；负数上限按不设限处理。
func budgetExceeded(total, maxTokensPerExecution int) bool {
	return maxTokensPerExecution > 0 && total > maxTokensPerExecution
}
