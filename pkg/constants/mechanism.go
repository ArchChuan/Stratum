// Package constants — mechanism tunable bounds.
package constants

// Matrix benchmark (机制基线设计 §5) tunables.
const (
	// MatrixMaxTokens caps a single matrix-evaluation LLM response. The
	// evaluand runs one bounded prompt-template task per case (抽取/总结/
	// 压缩模板输出），固定上限保证成本可预期且与 provider 无关。
	MatrixMaxTokens = 1024

	// MatrixBenchmarkSuiteName 是矩阵评测基准集套件名；seed 以名称幂等
	// （eval_suites.name 唯一），已存在的同名基准集直接复用其发布 revision。
	MatrixBenchmarkSuiteName = "机制基线基准集"

	// MatrixBenchmarkSuiteDescription 是基准集描述（seed 时写入）。
	MatrixBenchmarkSuiteDescription = "机制基线（模型档案）矩阵评测基准集：覆盖抽取/总结/压缩模板与富化/总结模型档位，judge 断言。"
)
