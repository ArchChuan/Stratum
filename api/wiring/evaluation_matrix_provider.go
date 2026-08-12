package wiring

import (
	"context"
	"errors"
	"fmt"

	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	evalpersist "github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence"
	mechanismapp "github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismport "github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// buildMatrixEvaluation 装配机制矩阵评测引擎（阶段3）：provider 把
// evaluation 基础设施适配给机制矩阵服务；LLM 网关可用时注册 mechanism
// adapter（虚拟指纹 revision + 档案模型/模板执行）。网关不可用 → adapter
// 不注册，矩阵 run 在执行期 fail closed（adapter 不可用错误），工作台
// 只读报告仍可用。独立方法控制 buildEvaluation 复杂度（质量棘轮）。
func (c *Container) buildMatrixEvaluation(
	suiteService *evalapp.SuiteService,
	jobService *evalapp.JobService,
	queryRepo *evalpersist.PgCenterQueryRepository,
	runRepo *evalpersist.PgRunRepository,
	resourceAdapters map[evaldomain.ResourceKind]evalport.ResourceAdapter,
) {
	if c.Mechanism == nil || c.Mechanism.Service == nil {
		return
	}
	c.Mechanism.Matrix = mechanismapp.NewMatrixService(c.Mechanism.Service, &matrixEvaluatorProvider{
		suites: suiteService, jobs: jobService, query: queryRepo, run: runRepo,
		profiles: c.Mechanism.Service,
	})
	if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
		return
	}
	resourceAdapters[evaldomain.ResourceKindMechanism] = &mechanismEvaluationAdapter{
		profiles: c.Mechanism.Service, completer: c.LLMGateway.Gateway,
	}
}

// matrixRunRequestedBy 是矩阵评测 job 的服务账号标识（幂等键之外的身份
// 字段，job 流水线可追踪触发来源）。
const matrixRunRequestedBy = "matrix-run"

// matrixSuiteService 是 provider 对 suite 管理的依赖收窄（消费方定义
// 小接口，*evalapp.SuiteService 天然实现，测试用 stub）。
type matrixSuiteService interface {
	GetActiveRevision(ctx context.Context, tenantID, suiteID string) (evaldomain.EvalSuiteRevision, error)
	Create(ctx context.Context, tenantID string, input evalapp.CreateSuiteInput) (evaldomain.EvalSuite, evaldomain.EvalSuiteRevision, error)
	Publish(ctx context.Context, tenantID, suiteID string) (evaldomain.EvalSuiteRevision, error)
}

// matrixJobService 是 provider 对 job 队列的依赖收窄。
type matrixJobService interface {
	EnqueueRun(ctx context.Context, tenantID string, input evalapp.EnqueueRunInput) (evaldomain.EvaluationJob, error)
}

// matrixEvaluatorProvider 在 wiring 层把 evaluation 基础设施（suite 管理、
// job 队列、查询/读取）适配给机制矩阵服务（实现 mechanism mechanismport.MatrixEvaluator）。
// 矩阵评测是默认租户管理面能力，tenantID 由调用方（默认租户中间件）保证。
type matrixEvaluatorProvider struct {
	suites   matrixSuiteService
	jobs     matrixJobService
	query    evalport.CenterQueryRepository
	run      evalport.RunRepository
	profiles mechanismProfileReader
}

// benchmarkMatrixCases 是代码轨内置基准集（机制基线设计 §5）：三个 judge
// 断言 case 覆盖抽取/总结/压缩模板。input 用 {"template","input"} 结构，
// adapter 据此取档案模板并以档案 EnrichModel 执行；expected_output 是任务
// 说明，judge 按 rubric 裁决。JudgeSpec.Model 留空 = 走平台参数默认。
func benchmarkMatrixCases() []evaldomain.EvalCase {
	return []evaldomain.EvalCase{
		{
			Name:           "记忆抽取：结构化事实",
			Input:          map[string]string{"template": "memory_extraction", "input": "用户说：我在杭州的团队下周发布新版本，主要风险是数据库迁移，需要运维配合。"},
			ExpectedOutput: "以 JSON 输出抽取的关键事实（地点、时间、事件、风险点、行动项），遗漏或结构错误视为不合格",
			AssertionMode:  evaldomain.AssertionJudge,
			Enabled:        true,
			JudgeSpec:      &evaldomain.JudgeSpec{Rubric: "评估输出是否为符合 schema 的 JSON 事实抽取：字段覆盖地点（杭州）、时间（下周）、事件（发布新版本）、风险（数据库迁移）、行动项（运维配合）。字段缺失、JSON 结构错误或混入无关内容均不合格。输出仅 0 或 1。"},
		},
		{
			Name:           "记忆总结：中文归纳",
			Input:          map[string]string{"template": "memory_summary", "input": "会议纪要：周一确认了 Q3 目标为提升检索准确率 15%，周四完成 Milvus 索引参数调优，周五评审新增 3 个 embedding 模型的评测结果。"},
			ExpectedOutput: "输出为一段连贯中文总结，保留决策与关键数字，不引入原文没有的信息",
			AssertionMode:  evaldomain.AssertionJudge,
			Enabled:        true,
			JudgeSpec:      &evaldomain.JudgeSpec{Rubric: "评估输出是否为忠实的中文总结：必须保留 Q3 检索准确率 +15% 目标、Milvus 索引调优、3 个 embedding 模型评测三个要点；不得编造原文未出现的细节；语言为中文且结构连贯。输出仅 0 或 1。"},
		},
		{
			Name:           "历史压缩：保留关键上下文",
			Input:          map[string]string{"template": "compaction", "input": "对话历史：用户抱怨账单金额错误，客服核实后确认多扣 50 元，承诺 3 个工作日内原路退回，用户要求退款进度短信通知。"},
			ExpectedOutput: "输出为压缩后的关键上下文，必须保留退款金额、时限与通知偏好，不遗漏任务相关事实",
			AssertionMode:  evaldomain.AssertionJudge,
			Enabled:        true,
			JudgeSpec:      &evaldomain.JudgeSpec{Rubric: "评估压缩输出是否保留全部任务关键上下文：多扣 50 元、3 个工作日原路退回、用户要求短信通知进度。任一关键事实丢失即不合格；不得新增原文没有的承诺。输出仅 0 或 1。"},
		},
	}
}

func (p *matrixEvaluatorProvider) ListBenchmarkSuites(
	ctx context.Context, tenantID string,
) ([]mechanismport.BenchmarkSuite, error) {
	page, err := p.query.ListSuites(ctx, tenantID, evalport.CenterFilter{
		ResourceKind: string(evaldomain.ResourceKindMechanism), Limit: 100,
	})
	if err != nil {
		return nil, fmt.Errorf("matrix provider: list benchmark suites: %w", err)
	}
	suites := make([]mechanismport.BenchmarkSuite, 0, len(page.Items))
	for _, summary := range page.Items {
		suite := mechanismport.BenchmarkSuite{
			ID: summary.ID, Name: summary.Name, Description: summary.Description,
		}
		revision, err := p.suites.GetActiveRevision(ctx, tenantID, summary.ID)
		if err == nil {
			suite.ActiveRevision = fmt.Sprintf("v%d", revision.VersionNo)
			suite.CaseCount = len(revision.Cases)
		} else if !errors.Is(err, evalapp.ErrSuiteNotFound) {
			return nil, fmt.Errorf("matrix provider: load active revision of %s: %w", summary.ID, err)
		}
		suites = append(suites, suite)
	}
	return suites, nil
}

// EnsureBenchmarkSuite 幂等保证基准集存在：已发布直接复用其 revision；
// 同名未发布或不存在则创建并发布（eval_suites.name 唯一保证同名收敛）。
// 并发创建同名套件会因唯一约束失败并向上传播（调用方重试走复用路径）。
func (p *matrixEvaluatorProvider) EnsureBenchmarkSuite(ctx context.Context, tenantID string) (string, error) {
	page, err := p.query.ListSuites(ctx, tenantID, evalport.CenterFilter{
		ResourceKind: string(evaldomain.ResourceKindMechanism), Limit: 100,
	})
	if err != nil {
		return "", fmt.Errorf("matrix provider: list benchmark suites: %w", err)
	}
	for _, summary := range page.Items {
		if summary.Name != constants.MatrixBenchmarkSuiteName {
			continue
		}
		revision, err := p.suites.GetActiveRevision(ctx, tenantID, summary.ID)
		if err == nil {
			return revision.ID, nil
		}
		if !errors.Is(err, evalapp.ErrSuiteNotFound) {
			return "", fmt.Errorf("matrix provider: load active revision of %s: %w", summary.ID, err)
		}
		// 同名套件存在但从未发布：重新 seed 并发布（幂等语义）。
		suite, _, err := p.suites.Create(ctx, tenantID, evalapp.CreateSuiteInput{
			Name: summary.Name, Description: summary.Description,
			ResourceKind: evaldomain.ResourceKindMechanism, Cases: benchmarkMatrixCases(),
		})
		if err != nil {
			return "", fmt.Errorf("matrix provider: recreate benchmark suite: %w", err)
		}
		published, err := p.suites.Publish(ctx, tenantID, suite.ID)
		if err != nil {
			return "", fmt.Errorf("matrix provider: publish benchmark suite: %w", err)
		}
		return published.ID, nil
	}
	suite, _, err := p.suites.Create(ctx, tenantID, evalapp.CreateSuiteInput{
		Name:         constants.MatrixBenchmarkSuiteName,
		Description:  constants.MatrixBenchmarkSuiteDescription,
		ResourceKind: evaldomain.ResourceKindMechanism,
		Cases:        benchmarkMatrixCases(),
	})
	if err != nil {
		return "", fmt.Errorf("matrix provider: seed benchmark suite: %w", err)
	}
	published, err := p.suites.Publish(ctx, tenantID, suite.ID)
	if err != nil {
		return "", fmt.Errorf("matrix provider: publish benchmark suite: %w", err)
	}
	return published.ID, nil
}

// StartMatrixRun 为单个档案×基准集排队评测 run。幂等键
// matrix:<familyKey>:<指纹>:<suiteRevisionID>：档案指纹变化或基准集
// 版本变化都会生成新幂等键触发重评测，同键重复排队由 job 仓库幂等去重。
func (p *matrixEvaluatorProvider) StartMatrixRun(
	ctx context.Context, tenantID, familyKey, suiteRevisionID, requestedBy string,
) error {
	profile, err := p.profiles.GetByFamilyKey(ctx, familyKey)
	if err != nil {
		return fmt.Errorf("matrix provider: load profile %s: %w", familyKey, err)
	}
	if profile.Fingerprint == "" {
		return fmt.Errorf("matrix provider: profile %s has no fingerprint", familyKey)
	}
	if requestedBy == "" {
		requestedBy = matrixRunRequestedBy
	}
	_, err = p.jobs.EnqueueRun(ctx, tenantID, evalapp.EnqueueRunInput{
		Resource: evaldomain.ResourceRef{
			Kind:       evaldomain.ResourceKindMechanism,
			ResourceID: familyKey,
			RevisionID: profile.Fingerprint,
		},
		SuiteRevisionID: suiteRevisionID,
		IdempotencyKey:  "matrix:" + familyKey + ":" + profile.Fingerprint + ":" + suiteRevisionID,
		RequestedBy:     requestedBy,
	})
	if err != nil {
		return fmt.Errorf("matrix provider: enqueue run for %s: %w", familyKey, err)
	}
	return nil
}

// LatestMatrixRuns 按档案族键取最近一次评测 run（每档案 2 次查询：列表 +
// 详情；档案个位数，可接受）。无评测的族键缺席（调用方渲染空单元格）。
func (p *matrixEvaluatorProvider) LatestMatrixRuns(
	ctx context.Context, tenantID string, familyKeys []string,
) ([]mechanismport.MatrixRun, error) {
	runs := make([]mechanismport.MatrixRun, 0, len(familyKeys))
	for _, familyKey := range familyKeys {
		page, err := p.query.ListRuns(ctx, tenantID, evalport.CenterFilter{
			ResourceKind: string(evaldomain.ResourceKindMechanism), ResourceID: familyKey, Limit: 1,
		})
		if err != nil {
			return nil, fmt.Errorf("matrix provider: list runs of %s: %w", familyKey, err)
		}
		if len(page.Items) == 0 {
			continue // 该档案尚无评测
		}
		summary := page.Items[0]
		run, ok, err := p.run.GetRun(ctx, tenantID, summary.ID)
		if err != nil {
			return nil, fmt.Errorf("matrix provider: load run %s: %w", summary.ID, err)
		}
		if !ok {
			continue
		}
		runs = append(runs, mechanismport.MatrixRun{
			FamilyKey:  familyKey,
			RunID:      run.ID,
			Passed:     run.Passed,
			PassRate:   metricFloat(run.Metrics, "pass_rate"),
			TotalCost:  metricFloat(run.Metrics, "total_cost_usd"),
			AvgLatency: metricFloat(run.Metrics, "avg_latency_ms"),
			TotalCases: run.TotalCases,
			Status:     summary.Status,
		})
	}
	return runs, nil
}

// metricFloat 从 run.Metrics（JSONB 往返后均为 float64）读数值指标；
// 键缺失返回 0（avg_latency 在全部 case 零耗时时不写入）。
func metricFloat(metrics map[string]any, key string) float64 {
	if metrics == nil {
		return 0
	}
	value, ok := metrics[key].(float64)
	if !ok {
		return 0
	}
	return value
}
