package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
)

// MatrixService 是评测矩阵工作台（机制基线设计 §5）的应用门面：
// 基准集 × 档案矩阵评测触发、多维报告（fidelity/cost/perf）与帕累托标注、
// 采纳（draft → active）。模型升级 → 指纹变化 → 重评测 → 采纳，闭环驱动。
type MatrixService struct {
	profiles  *Service
	evaluator port.MatrixEvaluator
}

// NewMatrixService 构造矩阵服务。profiles 与 evaluator 均不可为空。
func NewMatrixService(profiles *Service, evaluator port.MatrixEvaluator) *MatrixService {
	return &MatrixService{profiles: profiles, evaluator: evaluator}
}

// ErrMatrixNoProfiles 标记无档案可评测（避免空跑 seed 基准集）。
var ErrMatrixNoProfiles = errors.New("mechanism matrix: no profiles to evaluate")

// ErrAdoptInvalidTransition 标记采纳的状态迁移不合法（仅 draft → active）。
var ErrAdoptInvalidTransition = errors.New("mechanism matrix: adopt requires draft status")

// RunMatrixResult 是一次矩阵评测触发的摘要。
type RunMatrixResult struct {
	SuiteRevisionID string // 基准集已发布 revision（惰性 seed 的产物）
	TriggeredCount  int    // 成功排队的档案数
}

// MatrixCell 是矩阵中一个档案的最近评测单元格。
type MatrixCell struct {
	FamilyKey     string
	DisplayName   string
	Status        string
	Fingerprint   string
	Version       int
	EnrichModel   string // 档案声明的富化模型（执行侧使用的模型档位）
	SummaryModel  string
	RunID         string
	Passed        bool
	PassRate      float64
	TotalCost     float64 // 整轮 run 成本（USD）
	AvgLatency    float64 // 单 case 平均延迟（ms）
	TotalCases    int
	ExecutedCases int  // 真实执行产出结果的 case 数（无执行错误的 case）
	Frontier      bool // 帕累托前沿成员（fidelity/cost/perf 三维非支配）
}

// MatrixReport 是矩阵工作台的完整快照。
type MatrixReport struct {
	Suites       []port.BenchmarkSuite
	Cells        []MatrixCell
	FrontierKeys []string // 前沿档案族键（按报告行序）
}

// RunMatrix 触发全部档案 × 基准集的矩阵评测：
// 惰性 seed 基准集（首次调用幂等创建并发布）→ 逐档案排队 run（异步 worker 执行）。
// 无档案时直接返回错误，不创建空基准集。
func (s *MatrixService) RunMatrix(ctx context.Context, tenantID, requestedBy string) (RunMatrixResult, error) {
	profiles, err := s.profiles.ListProfiles(ctx)
	if err != nil {
		return RunMatrixResult{}, err
	}
	if len(profiles) == 0 {
		return RunMatrixResult{}, ErrMatrixNoProfiles
	}
	revisionID, err := s.evaluator.EnsureBenchmarkSuite(ctx, tenantID)
	if err != nil {
		return RunMatrixResult{}, fmt.Errorf("mechanism matrix: ensure benchmark suite: %w", err)
	}
	triggered := 0
	for _, p := range profiles {
		if err := s.evaluator.StartMatrixRun(ctx, tenantID, p.FamilyKey, revisionID, requestedBy); err != nil {
			return RunMatrixResult{}, fmt.Errorf("mechanism matrix: start run for %s: %w", p.FamilyKey, err)
		}
		triggered++
	}
	return RunMatrixResult{SuiteRevisionID: revisionID, TriggeredCount: triggered}, nil
}

// GetMatrix 汇总各档案最近一次评测 run 的多维指标，并标注帕累托前沿。
// 无基准集或全部档案无 run 时返回空报告（非错误）。
func (s *MatrixService) GetMatrix(ctx context.Context, tenantID string) (MatrixReport, error) {
	profiles, err := s.profiles.ListProfiles(ctx)
	if err != nil {
		return MatrixReport{}, err
	}
	suites, err := s.evaluator.ListBenchmarkSuites(ctx, tenantID)
	if err != nil {
		return MatrixReport{}, fmt.Errorf("mechanism matrix: list benchmark suites: %w", err)
	}
	runByFamily, err := s.latestRunsByFamily(ctx, tenantID, profiles)
	if err != nil {
		return MatrixReport{}, err
	}
	cells := buildMatrixCells(profiles, runByFamily)
	frontierKeys := annotateFrontier(cells)
	return MatrixReport{Suites: suites, Cells: cells, FrontierKeys: frontierKeys}, nil
}

// latestRunsByFamily 取各档案最近一次评测 run，按族键索引。
func (s *MatrixService) latestRunsByFamily(ctx context.Context, tenantID string, profiles []domain.Profile) (map[string]port.MatrixRun, error) {
	familyKeys := make([]string, 0, len(profiles))
	for _, p := range profiles {
		familyKeys = append(familyKeys, p.FamilyKey)
	}
	runs, err := s.evaluator.LatestMatrixRuns(ctx, tenantID, familyKeys)
	if err != nil {
		return nil, fmt.Errorf("mechanism matrix: latest runs: %w", err)
	}
	runByFamily := make(map[string]port.MatrixRun, len(runs))
	for _, r := range runs {
		runByFamily[r.FamilyKey] = r
	}
	return runByFamily, nil
}

// buildMatrixCells 组装档案 × 最近 run 的单元格（无 run 的档案指标留空）。
func buildMatrixCells(profiles []domain.Profile, runByFamily map[string]port.MatrixRun) []MatrixCell {
	cells := make([]MatrixCell, 0, len(profiles))
	for _, p := range profiles {
		cell := MatrixCell{
			FamilyKey:    p.FamilyKey,
			DisplayName:  p.DisplayName,
			Status:       p.Status,
			Fingerprint:  p.Fingerprint,
			Version:      p.Version,
			EnrichModel:  p.Baseline.Models.EnrichModel,
			SummaryModel: p.Baseline.Models.SummaryModel,
		}
		if r, ok := runByFamily[p.FamilyKey]; ok {
			cell.RunID = r.RunID
			cell.Passed = r.Passed
			cell.PassRate = r.PassRate
			cell.TotalCost = r.TotalCost
			cell.AvgLatency = r.AvgLatency
			cell.TotalCases = r.TotalCases
			cell.ExecutedCases = r.ExecutedCases
		}
		cells = append(cells, cell)
	}
	return cells
}

// AdoptProfile 采纳档案：仅允许 draft → active 迁移（评测采纳后置 active，
// 机制基线设计 §5）。复用 UpsertProfile：指纹不变（不触发重评测）、版本+1、
// 缓存失效。
func (s *MatrixService) AdoptProfile(ctx context.Context, familyKey, updatedBy string) error {
	p, err := s.profiles.GetByFamilyKey(ctx, familyKey)
	if err != nil {
		return err
	}
	if p.Status != domain.ProfileStatusDraft {
		return fmt.Errorf("%w: %s is %s", ErrAdoptInvalidTransition, familyKey, p.Status)
	}
	p.Status = domain.ProfileStatusActive
	return s.profiles.UpsertProfile(ctx, p, updatedBy)
}

// annotateFrontier 计算帕累托前沿并回写 Frontier 标志：维度为 pass_rate
// （越高越好）、total_cost（越低越好）、avg_latency（越低越好），仅对
// 「有真实评测数据」的档案计算。数据判定 = run 存在 且 至少一个 case 真实
// 执行（ExecutedCases>0）：执行失败的 run（adapter/网关错误、judge 禁用）
// 零指标且无评测证据，不参与前沿——TotalCases 在 case 执行前自增，不能
// 用作判别。前沿 = 不被任何其他有数据档案支配的档案；返回前沿族键。
func annotateFrontier(cells []MatrixCell) []string {
	withData := make([]MatrixCell, 0, len(cells))
	for _, c := range cells {
		if hasFrontierData(c) {
			withData = append(withData, c)
		}
	}
	frontier := make([]string, 0, len(cells))
	for i := range cells {
		if !hasFrontierData(cells[i]) {
			continue
		}
		if isDominated(cells[i], withData) {
			continue
		}
		cells[i].Frontier = true
		frontier = append(frontier, cells[i].FamilyKey)
	}
	return frontier
}

// hasFrontierData 判定单元格携带可进入前沿的真实评测证据。
func hasFrontierData(c MatrixCell) bool {
	return c.RunID != "" && c.ExecutedCases > 0
}

// isDominated 报告 a 是否被 candidates 中任一档案支配。
func isDominated(a MatrixCell, candidates []MatrixCell) bool {
	for _, b := range candidates {
		if a.FamilyKey != b.FamilyKey && dominates(b, a) {
			return true
		}
	}
	return false
}

// dominates 报告 a 在三维上支配 b：pass_rate 不低、cost 不高于、latency
// 不高于，且至少一维严格更优。
func dominates(a, b MatrixCell) bool {
	if a.PassRate < b.PassRate || a.TotalCost > b.TotalCost || a.AvgLatency > b.AvgLatency {
		return false
	}
	return a.PassRate > b.PassRate || a.TotalCost < b.TotalCost || a.AvgLatency < b.AvgLatency
}
