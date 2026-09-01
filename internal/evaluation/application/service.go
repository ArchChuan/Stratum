package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ExecutionResult = port.ExecutionResult

var ErrRunNotFound = errors.New("evaluation run not found")

type RunInput struct {
	TenantID    string
	RequestedBy string
	Resource    domain.ResourceRef
	Suite       domain.EvalSuiteRevision
}

type Service struct {
	adapter     port.ResourceAdapter
	repo        port.RunRepository
	suites      port.SuiteRepository
	traceReader port.TraceEvidenceReader
	// judge evaluates assertion_mode=judge cases; nil keeps rule assertions
	// working and makes judge cases fail closed.
	judge port.LLMJudge
	// review 是人工评审池升级入口（P1c §6.6 内联触发）；nil 时评审升级静默跳过
	// （fail-open，评审池未装配不阻断评测执行）。
	review    port.ReviewEscalator
	reviewCfg domain.ReviewConfig
	logger    *zap.Logger
	// metrics 记录平台指标（case_result 升级失败计数，spec §6.6）；nil 时升级
	// 失败仅日志（fail-open，不 panic）。
	metrics observability.MetricsProvider
	// platformVersion 解析平台配置组当前生效版本序号（Phase 2 §4.3 版本锚点）。
	// nil 时 run.metrics.version.platform_seq 记 0（unknown，fail-open）。
	platformVersion func(ctx context.Context) (int64, bool, error)
}

func NewService(
	adapter port.ResourceAdapter,
	repo port.RunRepository,
	traceReader port.TraceEvidenceReader,
	judge port.LLMJudge,
	suites ...port.SuiteRepository,
) *Service {
	var suiteRepo port.SuiteRepository
	if len(suites) > 0 {
		suiteRepo = suites[0]
	}
	return &Service{adapter: adapter, repo: repo, suites: suiteRepo, traceReader: traceReader, judge: judge, logger: zap.NewNop()}
}

// SetReviewEscalator 注入评审池升级器（wiring 在 NewService 之后调用）。
func (s *Service) SetReviewEscalator(e port.ReviewEscalator, cfg domain.ReviewConfig) {
	s.review = e
	s.reviewCfg = cfg
}

// SetObservability 注入真 logger 与平台指标（case_result 升级失败计数，spec §6.6）。
// wiring 在 SetReviewEscalator 后调用；logger 为 nil 保留默认 Nop，metrics 为 nil
// 时升级失败仅日志（fail-open，不 panic）。
func (s *Service) SetObservability(logger *zap.Logger, metrics observability.MetricsProvider) {
	if logger != nil {
		s.logger = logger
	}
	s.metrics = metrics
}

// SetPlatformVersion 注入平台版本读取器（wiring 在 NewService 后调用）；nil
// 表示未装配，run.metrics.version.platform_seq 记 0（unknown，fail-open）。
func (s *Service) SetPlatformVersion(fn func(ctx context.Context) (int64, bool, error)) {
	s.platformVersion = fn
}

// escalateCaseResult 通过评审池升级器判定评测集 judge 结果是否入池并幂等落条目
// （fail-open：失败仅日志，不阻断评测流程）。
func (s *Service) escalateCaseResult(
	ctx context.Context, tenantID, runID string, result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult,
) {
	if s.review == nil {
		return
	}
	if err := s.review.TryEscalateCaseResult(ctx, tenantID, runID, result, c, assertion); err != nil {
		s.logReviewEscalateError(ctx, err)
		if s.metrics != nil {
			s.metrics.IncEvalReviewEscalateFailure()
		}
	}
}

func (s *Service) logReviewEscalateError(_ context.Context, err error) {
	s.logger.Warn("evaluation review escalation failed", zap.Error(err))
}

func (s *Service) RunStored(
	ctx context.Context,
	tenantID, requestedBy string,
	resource domain.ResourceRef,
	suiteRevisionID string,
) (domain.EvalRun, error) {
	if s.suites == nil {
		return domain.EvalRun{}, errors.New("evaluation suite repository not configured")
	}
	suite, ok, err := s.suites.GetRevision(ctx, tenantID, suiteRevisionID)
	if err != nil {
		return domain.EvalRun{}, err
	}
	if !ok || suite.Status != domain.SuiteRevisionPublished {
		return domain.EvalRun{}, ErrSuiteNotFound
	}
	if suite.ResourceKind != resource.Kind {
		return domain.EvalRun{}, fmt.Errorf("evaluation suite resource kind %q does not match %q", suite.ResourceKind, resource.Kind)
	}
	return s.Run(ctx, RunInput{TenantID: tenantID, RequestedBy: requestedBy, Resource: resource, Suite: suite})
}

func (s *Service) GetRun(ctx context.Context, tenantID, runID string) (domain.EvalRun, error) {
	run, ok, err := s.repo.GetRun(ctx, tenantID, runID)
	if err != nil {
		return domain.EvalRun{}, err
	}
	if !ok {
		return domain.EvalRun{}, ErrRunNotFound
	}
	return run, nil
}

func (s *Service) Run(ctx context.Context, input RunInput) (domain.EvalRun, error) {
	if err := input.Resource.Validate(); err != nil {
		return domain.EvalRun{}, err
	}
	run := domain.EvalRun{
		ID:              uuid.Must(uuid.NewV7()).String(),
		Resource:        input.Resource,
		SuiteRevisionID: input.Suite.ID,
		Passed:          true,
		Results:         make([]domain.EvalCaseResult, 0, len(input.Suite.Cases)),
		CreatedAt:       time.Now().UTC(),
	}
	for _, testCase := range input.Suite.Cases {
		if !testCase.Enabled {
			continue
		}
		run.TotalCases++
		result := s.runCase(ctx, input.TenantID, input.RequestedBy, input.Resource, run.ID, testCase)
		if result.Passed {
			run.PassedCases++
		} else {
			run.Passed = false
		}
		run.Results = append(run.Results, result)
	}
	seq := int64(0)
	if s.platformVersion != nil {
		seqVal, ok, err := s.platformVersion(ctx)
		if err != nil {
			// fail-open：版本锚点 unknown（记 0），不阻断落库；但必须留痕便于诊断。
			if s.logger != nil {
				s.logger.Warn("evaluation run: platform version", zap.Error(err))
			}
		} else if ok {
			seq = seqVal
		}
	}
	run.Metrics = aggregateRunMetrics(run, runVersionAnchor{
		SuiteRevisionID: run.SuiteRevisionID,
		PlatformSeq:     seq,
		ResourceVersion: run.Resource.RevisionID,
	})
	if err := s.repo.SaveRun(ctx, input.TenantID, run); err != nil {
		return domain.EvalRun{}, err
	}
	return run, nil
}

func (s *Service) runCase(
	ctx context.Context, tenantID, requestedBy string, ref domain.ResourceRef, runID string, testCase domain.EvalCase,
) domain.EvalCaseResult {
	execution, err := s.adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
	result := domain.EvalCaseResult{ID: uuid.Must(uuid.NewV7()).String(), CaseID: testCase.ID}
	if err != nil {
		result.Error = err.Error()
		result.FailureReason = "execution"
		return result
	}
	result.Actual = execution.Output
	result.TraceID = execution.TraceID
	result.Tokens = execution.Tokens
	result.CostUSD = execution.CostUSD
	result.DurationMs = execution.DurationMs
	result.RAGEvidence = execution.RAGEvidence
	result.Tools = execution.Tools

	// Resolve trace evidence from Opik (best-effort: Opik unavailability must
	// not block Agent execution or evaluation).
	if execution.TraceID != "" && s.traceReader != nil {
		trace, resolveErr := s.traceReader.Resolve(ctx, tenantID, execution.TraceID)
		if resolveErr != nil {
			// warn-only: trace evidence is supplementary, not critical
			result.Message = "trace evidence unavailable"
		} else {
			result.TraceEvidence = observedTraceToEvidence(trace)
		}
	}

	// 过程断言（§6.5）：tool_spec 确定性规则 + step_judge 可选 LLM 评分。判定
	// 失败（或 judge 基础设施故障）时 fail-closed：置 Error + execution 归因返回，
	// 绝不静默 pass。过程归因单独落 ProcessPass/ProcessFailure，不改输出归因。
	verdict, err := s.evaluateProcess(ctx, testCase, result)
	if err != nil {
		result.Error = err.Error()
		result.FailureReason = "execution"
		return result
	}
	result.ProcessPass = verdict.Passed
	result.ProcessFailure = verdict.Failure
	// 步骤级 judge 维度先于输出断言维度并入结果（judgeCase 在其基础上追加）。
	result.Dimensions = verdict.Dimensions

	// 输出断言按 assertion_mode 分派：judge 分支走 LLM judge 端口，规则分支走
	// domain 纯函数。两种分支都把过程断言与输出断言 AND——任一路失败即 case
	// 失败；FailureReason 保持输出归因，过程归因单独在 ProcessFailure。
	if testCase.AssertionMode == domain.AssertionJudge {
		return s.judgeCaseResult(ctx, tenantID, runID, testCase, result)
	}
	return s.ruleCaseResult(ctx, testCase, execution.Output, result)
}

// judgeCaseResult 走 LLM judge 输出断言并把过程断言并入最终 Passed；随后内联
// 触发评审池升级（P1c §6.6，仅 judge 实际产出判定时）。
func (s *Service) judgeCaseResult(
	ctx context.Context, tenantID, runID string, testCase domain.EvalCase, result domain.EvalCaseResult,
) domain.EvalCaseResult {
	assertion, result := s.judgeCase(ctx, testCase, result)
	result.Passed = assertion.Passed && result.ProcessPass
	if result.Error == "" {
		s.escalateCaseResult(ctx, tenantID, runID, result, testCase, assertion)
	}
	return result
}

// ruleCaseResult 走 domain 纯函数规则断言并把过程断言并入最终 Passed。
func (s *Service) ruleCaseResult(
	ctx context.Context, testCase domain.EvalCase, actual any, result domain.EvalCaseResult,
) domain.EvalCaseResult {
	assertion, err := domain.EvaluateAssertion(testCase.AssertionMode, actual, testCase.ExpectedOutput)
	if err != nil {
		result.Error = err.Error()
		result.FailureReason = "execution"
		return result
	}
	result.Passed = assertion.Passed && result.ProcessPass
	result.Message = assertion.Message
	if !assertion.Passed {
		result.FailureReason = "assert:" + string(testCase.AssertionMode)
	}
	return result
}

// processVerdict 是过程断言（§6.5）的合并判定：tool_spec 确定性规则与 step_judge
// LLM 评分两路独立、逐路收集失败。Failure 是过程归因失败描述（多失败以 "; " 连接）；
// Dimensions 来自步骤级 judge，由调用方并入 result.Dimensions。
type processVerdict struct {
	Passed     bool
	Failure    string
	Dimensions []domain.DimensionScore
}

// evaluateProcess 判定 case 的过程断言：ToolSpec!=nil → EvaluateToolSequence
// 确定性规则；StepJudge!=nil → judgeProcess LLM 步骤级评分。任一失败即过程判定
// 失败；step_judge 基础设施故障（judge nil/disabled/marshal/解析失败）向上返回
// error，由 runCase fail-closed 处理，绝不静默 pass。
func (s *Service) evaluateProcess(
	ctx context.Context, testCase domain.EvalCase, result domain.EvalCaseResult,
) (processVerdict, error) {
	verdict := processVerdict{Passed: true}
	var failures []string

	if testCase.ToolSpec != nil {
		assertion := domain.EvaluateToolSequence(toolNames(result.Tools), *testCase.ToolSpec)
		failures = append(failures, assertion.Failures...)
	}
	if testCase.StepJudge != nil {
		ja, err := s.judgeProcess(ctx, testCase, *testCase.StepJudge, result)
		if err != nil {
			return processVerdict{}, err
		}
		verdict.Dimensions = append(verdict.Dimensions, ja.Dimensions...)
		if !ja.Passed {
			failures = append(failures, judgeFailureReason(ja))
		}
	}

	if len(failures) > 0 {
		verdict.Passed = false
		verdict.Failure = strings.Join(failures, "; ")
	}
	return verdict, nil
}

// judgeProcess 用 LLM 对工具序列做步骤级评分（§6.5 step_judge）。fail-closed：
// judge nil/disabled 或输入 marshal 失败 → 返回 error。Model 为空表示走平台默认
// 模型；Rubric 为空时由 judge 适配层回退平台默认步骤 rubric。
func (s *Service) judgeProcess(
	ctx context.Context, testCase domain.EvalCase, stepJudge domain.StepJudge, result domain.EvalCaseResult,
) (domain.AssertionResult, error) {
	if s.judge == nil || !s.judge.Enabled(ctx) {
		return domain.AssertionResult{}, errors.New("LLM judge disabled")
	}
	inputJSON, err := json.Marshal(testCase.Input)
	if err != nil {
		return domain.AssertionResult{}, fmt.Errorf("step judge: marshal input: %w", err)
	}
	expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
	if err != nil {
		return domain.AssertionResult{}, fmt.Errorf("step judge: marshal expected output: %w", err)
	}
	actualJSON, err := json.Marshal(result.Actual)
	if err != nil {
		return domain.AssertionResult{}, fmt.Errorf("step judge: marshal actual output: %w", err)
	}
	return s.judge.Judge(ctx, port.JudgeRequest{
		Model:          "",
		Rubric:         stepJudge.Criteria,
		Input:          string(inputJSON),
		ExpectedOutput: string(expectedJSON),
		Actual:         string(actualJSON),
		ToolSequence:   domain.FormatToolSequence(result.Tools),
	})
}

// toolNames 从工具观察序列提取工具名列表（EvaluateToolSequence 的输入）。
func toolNames(tools []domain.ToolObservation) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.ToolName)
	}
	return names
}

// judgeCase runs the LLM judge assertion for a judge case. Fail-closed: a
// nil or disabled judge makes the case fail with an explicit error instead
// of a silent pass. It returns the raw assertion (for review-pool escalation)
// alongside the result.
func (s *Service) judgeCase(ctx context.Context, testCase domain.EvalCase, result domain.EvalCaseResult) (domain.AssertionResult, domain.EvalCaseResult) {
	var zero domain.AssertionResult
	if s.judge == nil || !s.judge.Enabled(ctx) {
		result.Error = "LLM judge disabled"
		result.FailureReason = "execution"
		return zero, result
	}
	inputJSON, err := json.Marshal(testCase.Input)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal input: %w", err).Error()
		result.FailureReason = "execution"
		return zero, result
	}
	expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal expected output: %w", err).Error()
		result.FailureReason = "execution"
		return zero, result
	}
	actualJSON, err := json.Marshal(result.Actual)
	if err != nil {
		result.Error = fmt.Errorf("judge: marshal actual output: %w", err).Error()
		result.FailureReason = "execution"
		return zero, result
	}
	var spec domain.JudgeSpec
	if testCase.JudgeSpec != nil {
		spec = *testCase.JudgeSpec
	}
	assertion, err := s.judge.Judge(ctx, port.JudgeRequest{
		Model:          spec.Model,
		Rubric:         spec.Rubric,
		Input:          string(inputJSON),
		ExpectedOutput: string(expectedJSON),
		Actual:         string(actualJSON),
	})
	if err != nil {
		result.Error = err.Error()
		result.FailureReason = "execution"
		return zero, result
	}
	result.Passed = assertion.Passed
	result.Message = assertion.Message
	// 步骤级 judge 维度已由 runCase 预置到 result.Dimensions；输出维度追加合并，
	// 保持单一路径的既有语义（nil 起点 → 仅输出维度）。
	result.Dimensions = append(result.Dimensions, assertion.Dimensions...)
	result.FailureReason = judgeFailureReason(assertion)
	return assertion, result
}

// judgeFailureReason 从 judge 判定推导主要失败维度（spec §6.2）：优先取显式
// 判负的维度，否则取 score 最低维度；无维度信息时回退 "judge"（保持归因可见）。
func judgeFailureReason(assertion domain.AssertionResult) string {
	if assertion.Passed {
		return ""
	}
	for _, d := range assertion.Dimensions {
		if !d.Passed {
			return "dimension:" + d.Name
		}
	}
	if len(assertion.Dimensions) > 0 {
		worst := assertion.Dimensions[0]
		for _, d := range assertion.Dimensions[1:] {
			if d.Score < worst.Score {
				worst = d
			}
		}
		return "dimension:" + worst.Name
	}
	return "judge"
}

func observedTraceToEvidence(t port.ObservedTrace) *domain.ObservedTraceEvidence {
	return &domain.ObservedTraceEvidence{
		CostUSD:           t.CostUSD,
		LatencyMs:         t.LatencyMs,
		Success:           t.Success,
		SecurityViolation: t.SecurityViolation,
	}
}
