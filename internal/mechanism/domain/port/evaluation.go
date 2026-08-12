// Package port 定义机制 context 消费 evaluation context 能力的接口
// （跨 context 依赖方向：消费方定义接口，provider 由 wiring 薄适配装配）。
package port

import "context"

// BenchmarkSuite 是矩阵工作台可见的基准集摘要（mechanism 类型评测套件）。
type BenchmarkSuite struct {
	ID             string
	Name           string
	Description    string
	ActiveRevision string
	CaseCount      int
}

// MatrixRun 是单个档案×基准集的一次评测 run 摘要（矩阵单元格）。
type MatrixRun struct {
	FamilyKey  string
	RunID      string
	Passed     bool
	PassRate   float64
	TotalCost  float64
	AvgLatency float64
	TotalCases int
	Status     string
}

// MatrixEvaluator 是矩阵评测引擎对 evaluation 侧的能力依赖：
// 基准集查询/惰性 seed、评测触发（异步队列）、run 结果读取。
type MatrixEvaluator interface {
	// ListBenchmarkSuites 返回机制基准集套件列表（按名称匹配 mechanism kind）。
	ListBenchmarkSuites(ctx context.Context, tenantID string) ([]BenchmarkSuite, error)
	// EnsureBenchmarkSuite 幂等保证基准集存在（首次调用创建并发布），
	// 返回其当前已发布 revision ID。
	EnsureBenchmarkSuite(ctx context.Context, tenantID string) (string, error)
	// StartMatrixRun 为单个档案×基准集触发一次评测 run（幂等键由调用方保证）。
	// requestedBy 为触发者身份；空值由实现方回退到服务账号。
	StartMatrixRun(ctx context.Context, tenantID, familyKey, suiteRevisionID, requestedBy string) error
	// LatestMatrixRuns 返回各档案族键最近一次评测 run（无 run 的族键缺席）。
	LatestMatrixRuns(ctx context.Context, tenantID string, familyKeys []string) ([]MatrixRun, error)
}

// ProfileStatusReader 读取档案状态（矩阵采纳动作校验用）。
type ProfileStatusReader interface {
	ProfileStatus(ctx context.Context, familyKey string) (string, error)
}

// EvalRunRef 是矩阵 run 的持久化引用（job/run 结果对账）。
type EvalRunRef struct {
	FamilyKey string
	RunID     string
}
